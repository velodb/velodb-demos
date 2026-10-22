//! Rows to a Kafka topic, one record per message.
//!
//! Sends are asynchronous: up to `in_flight` messages wait for their
//! acknowledgement at once, and the first delivery failure fails the run.

use std::collections::BTreeMap;
use std::time::Duration;

use anyhow::{anyhow, bail, Context, Result};
use async_trait::async_trait;
use futures::stream::{FuturesUnordered, StreamExt};
use rdkafka::config::ClientConfig;
use rdkafka::error::{KafkaError, RDKafkaErrorCode};
use rdkafka::producer::future_producer::DeliveryFuture;
use rdkafka::producer::{FutureProducer, FutureRecord, Producer};
use rdkafka::util::Timeout;

use super::{Encoded, Sink};

pub struct KafkaSinkOptions {
    pub brokers: String,
    pub topic: String,
    pub in_flight: usize,
    /// Extra librdkafka properties, passed through verbatim.
    pub properties: BTreeMap<String, String>,
}

pub struct KafkaSink {
    producer: FutureProducer,
    topic: String,
    max_in_flight: usize,
    in_flight: FuturesUnordered<DeliveryFuture>,
    pub delivered: u64,
}

impl KafkaSink {
    pub fn new(options: KafkaSinkOptions) -> Result<Self> {
        let mut config = ClientConfig::new();
        config.set("bootstrap.servers", &options.brokers);
        for (key, value) in &options.properties {
            config.set(key, value);
        }
        let producer: FutureProducer = config
            .create()
            .with_context(|| format!("failed to create a Kafka producer for {}", options.brokers))?;
        Ok(Self {
            producer,
            topic: options.topic,
            max_in_flight: options.in_flight.max(1),
            in_flight: FuturesUnordered::new(),
            delivered: 0,
        })
    }

    async fn settle_one(&mut self) -> Result<()> {
        match self.in_flight.next().await {
            Some(Ok(Ok(_))) => {
                self.delivered += 1;
                Ok(())
            }
            Some(Ok(Err((error, _)))) => Err(anyhow!("Kafka delivery failed: {}", error)),
            Some(Err(_)) => Err(anyhow!("Kafka delivery report was dropped before it arrived")),
            None => Ok(()),
        }
    }
}

#[async_trait]
impl Sink for KafkaSink {
    async fn write(&mut self, message: &Encoded) -> Result<()> {
        loop {
            let mut record = FutureRecord::<[u8], [u8]>::to(&self.topic).payload(&message.payload);
            if let Some(key) = &message.key {
                record = record.key(key);
            }
            match self.producer.send_result(record) {
                Ok(future) => {
                    self.in_flight.push(future);
                    break;
                }
                Err((KafkaError::MessageProduction(RDKafkaErrorCode::QueueFull), _)) => {
                    // The local queue is full: let acknowledgements drain it.
                    if self.in_flight.is_empty() {
                        tokio::time::sleep(Duration::from_millis(10)).await;
                    } else {
                        self.settle_one().await?;
                    }
                }
                Err((error, _)) => bail!("failed to enqueue a Kafka message: {}", error),
            }
        }
        while self.in_flight.len() >= self.max_in_flight {
            self.settle_one().await?;
        }
        Ok(())
    }

    async fn flush(&mut self) -> Result<()> {
        Ok(())
    }

    async fn finish(&mut self) -> Result<()> {
        while !self.in_flight.is_empty() {
            self.settle_one().await?;
        }
        let producer = self.producer.clone();
        tokio::task::spawn_blocking(move || producer.flush(Timeout::After(Duration::from_secs(60))))
            .await
            .context("Kafka flush task failed")?
            .context("Kafka flush failed")?;
        Ok(())
    }
}
