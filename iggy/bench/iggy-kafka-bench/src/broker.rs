//! The one abstraction that makes the comparison fair: a single `Broker` trait
//! with two implementations. The generator, batching, rate limiting, and timing
//! are identical across brokers — only the client behind this trait swaps. Iggy
//! uses its native Rust SDK; Kafka uses librdkafka (rdkafka), both best-in-class
//! for their broker, so we measure brokers and not client-library maturity.

use anyhow::{anyhow, Result};
use async_trait::async_trait;

/// A batch is a list of (partition key, JSON payload) pairs. The key drives
/// partitioning so both brokers spread load the same way.
pub type Batch = Vec<(String, Vec<u8>)>;

#[async_trait]
pub trait Broker: Send + Sync {
    /// Send one batch and wait for the broker to acknowledge it (so latency and
    /// back-pressure are real, not hidden in a client-side queue).
    async fn send_batch(&self, batch: Batch) -> Result<()>;
}

// ---------------------------------------------------------------------------
// Iggy
// ---------------------------------------------------------------------------

pub struct IggyBroker {
    producer: iggy::prelude::IggyProducer,
}

impl IggyBroker {
    pub async fn connect(
        connection_string: &str,
        stream: &str,
        topic: &str,
        batch_length: usize,
        partitions: u32,
    ) -> Result<Self> {
        use iggy::prelude::*;

        // Credentials live in the connection string (e.g.
        // `iggy://iggy:iggy@127.0.0.1:8090`); connect() authenticates from it.
        let client = IggyClientBuilder::from_connection_string(connection_string)?.build()?;
        client.connect().await?;

        let producer = client
            .producer(stream, topic)?
            .direct(
                DirectConfig::builder()
                    .batch_length(batch_length as u32)
                    .build(),
            )
            .partitioning(Partitioning::balanced())
            .create_topic_if_not_exists(
                partitions,
                None,
                IggyExpiry::ServerDefault,
                MaxTopicSize::ServerDefault,
            )
            .build();
        producer.init().await?;
        Ok(Self { producer })
    }
}

#[async_trait]
impl Broker for IggyBroker {
    async fn send_batch(&self, batch: Batch) -> Result<()> {
        use iggy::prelude::IggyMessage;
        use std::str::FromStr;

        let mut messages = Vec::with_capacity(batch.len());
        for (_key, payload) in batch {
            // Payload is UTF-8 JSON; from_str is the SDK's proven construction
            // path (see examples/rust/.../producer/main.rs).
            let json = String::from_utf8(payload)?;
            messages.push(IggyMessage::from_str(&json)?);
        }
        self.producer.send(messages).await?;
        Ok(())
    }
}

// ---------------------------------------------------------------------------
// Kafka
// ---------------------------------------------------------------------------

pub struct KafkaBroker {
    producer: rdkafka::producer::FutureProducer,
    topic: String,
}

impl KafkaBroker {
    pub fn connect(brokers: &str, topic: &str) -> Result<Self> {
        use rdkafka::config::ClientConfig;
        use rdkafka::producer::FutureProducer;

        // Match the Iggy/Doris-plan defaults: no compression, light linger, acks=1.
        // Keep these identical to the Kafka perf-test config used in Layer 1.
        let producer: FutureProducer = ClientConfig::new()
            .set("bootstrap.servers", brokers)
            .set("acks", "1")
            .set("linger.ms", "5")
            .set("batch.size", "262144")
            .set("compression.type", "none")
            .set("queue.buffering.max.messages", "2000000")
            .create()?;
        Ok(Self {
            producer,
            topic: topic.to_string(),
        })
    }
}

#[async_trait]
impl Broker for KafkaBroker {
    async fn send_batch(&self, batch: Batch) -> Result<()> {
        use futures::future::join_all;
        use rdkafka::producer::FutureRecord;
        use rdkafka::util::Timeout;

        // Enqueue the whole batch (librdkafka copies each record on send), then
        // await all delivery acks together so we measure batch-level latency.
        let mut deliveries = Vec::with_capacity(batch.len());
        for (key, payload) in &batch {
            let record = FutureRecord::to(&self.topic).key(key).payload(payload);
            deliveries.push(self.producer.send(record, Timeout::Never));
        }
        for result in join_all(deliveries).await {
            result.map_err(|(err, _msg)| anyhow!("kafka delivery failed: {err}"))?;
        }
        Ok(())
    }
}
