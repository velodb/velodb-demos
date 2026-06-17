# iggy-kafka-bench

Symmetric clickstream load generator for the **Iggy vs Kafka → VeloDB**
benchmark. One Rust binary; only `--broker` swaps the client:

- **Iggy** → native Iggy Rust SDK (`IggyProducer`)
- **Kafka** → `rdkafka` (librdkafka, the reference non-JVM client)

The generator, batching, rate limiting, and timing are identical across brokers,
so you measure **brokers**, not client-library maturity. The clickstream JSON
matches `backend/internal/datagen/service/clickstream_service.go`, and
`event_timestamp` is the **origin stamp** for event-to-queryable latency.

## What it is and isn't for

- **Layer 2 (E2E into VeloDB):** this is the right tool: identical JSON on both
  sides, so the Stream Load path is byte-for-byte comparable.
- **Layer 1 (broker-only):** you *can* use this (`--broker` both ways, no VeloDB),
  and it's more symmetric than `iggy-bench` vs Kafka `*-perf-test` because it's
  one codebase. Run `iggy-bench` alongside if you also want Iggy's native scenarios.

## Build

```bash
cd iggy/bench/iggy-kafka-bench
cargo build --release
```

Notes:
- `Cargo.toml` uses the published `iggy = "0.10"` crate (crates.io) so this builds
  from a fresh clone. If you want the API to match a specific server build exactly,
  point it at a local Iggy checkout instead (see the comment in `Cargo.toml`).
- `rdkafka` builds a vendored librdkafka via cmake (needs a C toolchain + cmake).
  First build is slow.

## Run

```bash
# Iggy
./target/release/iggy-kafka-bench --broker iggy \
  --iggy-connection iggy://iggy:iggybench@127.0.0.1:8090 \
  --stream bench --topic clickstream --partitions 8 \
  --batch 1000 --rate 200000 --duration-secs 60 --warmup-secs 5

# Kafka: same knobs, only the broker changes
./target/release/iggy-kafka-bench --broker kafka \
  --kafka-brokers 127.0.0.1:9092 \
  --topic clickstream --partitions 8 \
  --batch 1000 --rate 200000 --duration-secs 60 --warmup-secs 5
```

Keep `--batch`, `--rate`, `--partitions`, and duration identical across the two
runs. Create the Kafka topic (with the matching partition count) up front; the
Iggy stream/topic are auto-created.

## Fairness knobs already wired

- No compression on either side; Kafka `acks=1`, `linger.ms=5`,
  `batch.size=262144` (match these to your Kafka perf-test config).
- Warm-up window excluded from reported throughput.
- Same partition key derivation (hash of payload) so load spreads the same way.

## Status

`cargo check` passes against the local Iggy SDK (`0.10.1-edge.2`) and
`rdkafka 0.36`: the producer API, `IggyMessage::from_str`, and the rdkafka
`FutureProducer`/`FutureRecord` path all type-check. The one thing left to
confirm **against a live server** (not a compile concern):

- **Iggy auth:** verify `connect()` authenticates from the connection-string
  credentials. If a run fails unauthenticated, add an explicit
  `login_user(DEFAULT_ROOT_USERNAME, DEFAULT_ROOT_PASSWORD)` after `connect()`.

## Next steps

- For E2E latency: after the run, join `event_timestamp` (origin) against the
  row's VeloDB ingest time to get event-to-queryable percentiles. Mind
  cross-machine clock skew (NTP/PTP).
- Add a `--mode consume` path if you want a symmetric consumer for broker-only
  read benchmarks.
