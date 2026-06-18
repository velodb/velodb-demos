//! Symmetric clickstream load generator for the Iggy-vs-Kafka → VeloDB benchmark.
//!
//! One binary, one code path; only `--broker` swaps the client. Generates
//! clickstream JSON identical on both sides, batches it, paces to a target rate,
//! and reports throughput. The `event_timestamp` field is the origin stamp for
//! the downstream event-to-queryable latency measurement.

mod broker;
mod event;

use anyhow::Result;
use broker::{Broker, IggyBroker, KafkaBroker};
use clap::{Parser, ValueEnum};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tracing::info;
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt, EnvFilter, Registry};

#[derive(Copy, Clone, Debug, PartialEq, Eq, ValueEnum)]
enum BrokerKind {
    Iggy,
    Kafka,
}

#[derive(Parser, Debug)]
#[command(name = "iggy-kafka-bench", about = "Symmetric clickstream load generator")]
struct Args {
    /// Which broker to drive.
    #[arg(long, value_enum, default_value = "iggy")]
    broker: BrokerKind,

    /// Iggy connection string (creds included).
    #[arg(long, default_value = "iggy://iggy:iggy@127.0.0.1:8090")]
    iggy_connection: String,

    /// Kafka bootstrap servers.
    #[arg(long, default_value = "127.0.0.1:9092")]
    kafka_brokers: String,

    /// Stream (Iggy) — created if absent.
    #[arg(long, default_value = "bench")]
    stream: String,

    /// Topic name (Iggy topic / Kafka topic). Create the Kafka topic up front.
    #[arg(long, default_value = "clickstream")]
    topic: String,

    /// Partitions to create (Iggy). Match the Kafka topic's partition count.
    #[arg(long, default_value_t = 1)]
    partitions: u32,

    /// Messages per batch / send.
    #[arg(long, default_value_t = 1000)]
    batch: usize,

    /// Total messages to send. 0 = run for `--duration-secs` instead.
    #[arg(long, default_value_t = 0)]
    total: u64,

    /// Run duration in seconds when `--total` is 0.
    #[arg(long, default_value_t = 30)]
    duration_secs: u64,

    /// Target aggregate rate in messages/sec. 0 = unthrottled.
    #[arg(long, default_value_t = 0)]
    rate: u64,

    /// Warm-up seconds excluded from the reported figures.
    #[arg(long, default_value_t = 5)]
    warmup_secs: u64,
}

#[tokio::main]
async fn main() -> Result<()> {
    Registry::default()
        .with(tracing_subscriber::fmt::layer())
        .with(EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("INFO")))
        .init();

    let args = Args::parse();
    info!(?args.broker, "connecting");

    let broker: Arc<dyn Broker> = match args.broker {
        BrokerKind::Iggy => Arc::new(
            IggyBroker::connect(
                &args.iggy_connection,
                &args.stream,
                &args.topic,
                args.batch,
                args.partitions,
            )
            .await?,
        ),
        BrokerKind::Kafka => Arc::new(KafkaBroker::connect(&args.kafka_brokers, &args.topic)?),
    };

    run(args, broker).await
}

async fn run(args: Args, broker: Arc<dyn Broker>) -> Result<()> {
    let start = Instant::now();
    let warmup = Duration::from_secs(args.warmup_secs);
    let stop_at = (args.total == 0)
        .then(|| start + warmup + Duration::from_secs(args.duration_secs));

    // Figures are accumulated only after warm-up so JVM/cache warmup on either
    // side doesn't skew the comparison.
    let mut measured_msgs: u64 = 0;
    let mut measured_bytes: u64 = 0;
    let mut measured_start: Option<Instant> = None;
    let mut total_sent: u64 = 0;
    let mut last_report = start;

    loop {
        if args.total != 0 && total_sent >= args.total {
            break;
        }
        if let Some(deadline) = stop_at {
            if Instant::now() >= deadline {
                break;
            }
        }

        let mut batch = Vec::with_capacity(args.batch);
        let mut batch_bytes = 0u64;
        for _ in 0..args.batch {
            let payload = serde_json::to_vec(&event::generate())?;
            batch_bytes += payload.len() as u64;
            // Key by session so related events land on one partition, same on
            // both brokers.
            let key = format!("{:016x}", fxhash(&payload));
            batch.push((key, payload));
        }

        broker.send_batch(batch).await?;
        total_sent += args.batch as u64;

        let warmed_up = start.elapsed() >= warmup;
        if warmed_up {
            if measured_start.is_none() {
                measured_start = Some(Instant::now());
                info!("warm-up complete, measuring");
            }
            measured_msgs += args.batch as u64;
            measured_bytes += batch_bytes;
        }

        // Pace to the target rate over the whole run (warm-up included so the
        // offered load is steady from t=0).
        if args.rate > 0 {
            let elapsed = start.elapsed().as_secs_f64();
            let expected = total_sent as f64 / args.rate as f64;
            if expected > elapsed {
                tokio::time::sleep(Duration::from_secs_f64(expected - elapsed)).await;
            }
        }

        if last_report.elapsed() >= Duration::from_secs(5) {
            if let Some(t0) = measured_start {
                report("progress", measured_msgs, measured_bytes, t0.elapsed());
            }
            last_report = Instant::now();
        }
    }

    match measured_start {
        Some(t0) => report("final", measured_msgs, measured_bytes, t0.elapsed()),
        None => info!("run ended during warm-up; nothing measured (raise --duration-secs)"),
    }
    Ok(())
}

fn report(label: &str, msgs: u64, bytes: u64, elapsed: Duration) {
    let secs = elapsed.as_secs_f64().max(f64::MIN_POSITIVE);
    info!(
        "{label}: {msgs} msgs in {:.1}s | {:.0} msg/s | {:.1} MB/s",
        secs,
        msgs as f64 / secs,
        (bytes as f64 / (1024.0 * 1024.0)) / secs,
    );
}

/// Cheap non-cryptographic hash for a stable per-event partition key. Avoids a
/// uuid dependency; only needs to be deterministic and well-distributed.
fn fxhash(bytes: &[u8]) -> u64 {
    let mut hash: u64 = 0xcbf2_9ce4_8422_2325;
    for &b in bytes {
        hash ^= b as u64;
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    hash
}
