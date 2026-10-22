//! The generation pipeline: producers on every generator thread generate
//! and encode rows in batches; sink threads pull batches off a bounded
//! queue, apply the shared rate, row and time limits, and deliver them.
//!
//! Its output is meant to be piped, so everything diagnostic goes to stderr
//! and nothing but rows goes to stdout.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

use anyhow::{bail, Result};
use rand::{rngs::StdRng, SeedableRng};
use tokio::sync::{mpsc, Mutex};

use crate::compile::{CompiledSpec, RowContext};
use crate::encode::{Framing, RecordEncoder};
use crate::sink::{Encoded, Sink};
use crate::spec::ContextReset;

pub struct StreamOptions {
    /// Generator threads. Use [`effective_threads`] to honour stateful
    /// generators, which need exactly one.
    pub threads: usize,
    /// Sink threads, each with its own sink from the factory.
    pub sink_threads: usize,
    /// Batches that may wait between generators and sinks.
    pub queue_depth: usize,
    /// Rows per batch; the spec's `batch.rows` when absent.
    pub batch_rows: Option<usize>,
    pub rows: Option<u64>,
    pub time_limit: Option<Duration>,
    pub rows_per_second: Option<u64>,
    pub bytes_per_second: Option<u64>,
    /// The field whose text becomes each record's key, for keyed sinks.
    pub key_field: Option<String>,
}

/// Builds one sink per sink thread, given the thread's index from 0.
pub type SinkFactory = Box<dyn Fn(usize) -> Result<Box<dyn Sink>> + Send + Sync>;

/// What a run produced.
#[derive(Debug, Clone, Copy, Default)]
pub struct Summary {
    pub rows: u64,
    pub bytes: u64,
    pub elapsed: Duration,
}

/// How many producers a spec allows: stateful generators must run alone.
pub fn effective_threads(spec: &CompiledSpec, requested: usize) -> usize {
    if spec.has_stateful_ordered {
        1
    } else {
        requested.max(1)
    }
}

type BatchMessage = std::result::Result<Vec<Encoded>, String>;

/// Generate and deliver until a limit is hit or the spec runs out of rows.
pub async fn run(
    mut spec: CompiledSpec,
    encoder: Arc<RecordEncoder>,
    framing: Framing,
    sinks: SinkFactory,
    options: StreamOptions,
) -> Result<Summary> {
    let threads = effective_threads(&spec, options.threads);
    spec.check_sequence_capacity(options.rows, threads)?;
    if let Some(key_field) = &options.key_field {
        if !spec.fields.iter().any(|field| &field.name == key_field) {
            bail!("key_field `{}` is not a field of the spec", key_field);
        }
    }
    if let Some(batch_rows) = options.batch_rows {
        spec.batch_rows = batch_rows.max(1);
    }

    let (tx, rx) = mpsc::channel::<BatchMessage>(options.queue_depth.max(1));
    let remaining_rows = options.rows.map(AtomicU64::new).map(Arc::new);
    let key_field: Option<Arc<str>> = options.key_field.as_deref().map(Arc::from);
    for _ in 0..threads {
        let tx = tx.clone();
        let spec = spec.clone();
        let encoder = encoder.clone();
        let framing = framing.clone();
        let remaining_rows = remaining_rows.clone();
        let key_field = key_field.clone();
        tokio::spawn(async move {
            let _ = producer_loop(spec, encoder, framing, key_field, tx, remaining_rows).await;
        });
    }
    drop(tx);

    let shared = Arc::new(Shared {
        rx: Mutex::new(rx),
        limiter: RateLimiter::new(options.rows_per_second, options.bytes_per_second),
        started: Instant::now(),
        time_limit: options.time_limit,
        stop: std::sync::atomic::AtomicBool::new(false),
    });
    let mut workers = Vec::new();
    for index in 0..options.sink_threads.max(1) {
        let sink = sinks(index)?;
        let shared = shared.clone();
        workers.push(tokio::spawn(async move { consumer_loop(shared, sink).await }));
    }

    let mut summary = Summary::default();
    let mut first_error = None;
    for worker in workers {
        match worker.await {
            Ok(Ok(part)) => {
                summary.rows += part.rows;
                summary.bytes += part.bytes;
            }
            Ok(Err(error)) => {
                // One failure stops the others at their next batch.
                shared.stop.store(true, Ordering::Relaxed);
                first_error.get_or_insert(error);
            }
            Err(join) => {
                shared.stop.store(true, Ordering::Relaxed);
                first_error.get_or_insert(anyhow::anyhow!("a sink thread panicked: {}", join));
            }
        }
    }
    if let Some(error) = first_error {
        return Err(error);
    }
    summary.elapsed = shared.started.elapsed();
    Ok(summary)
}

async fn producer_loop(
    spec: CompiledSpec,
    encoder: Arc<RecordEncoder>,
    framing: Framing,
    key_field: Option<Arc<str>>,
    tx: mpsc::Sender<BatchMessage>,
    remaining_rows: Option<Arc<AtomicU64>>,
) -> Result<()> {
    let mut rng = StdRng::from_entropy();
    let mut fields = (*spec.fields).clone();
    // Reused across every row: cleared or cloned into rather than reallocated.
    let mut ctx = RowContext::with_capacity(spec.fields.len());
    // Carry context for the batch and never reset modes.
    let mut carry = RowContext::new();

    loop {
        let batch_target = reserve_batch(spec.batch_rows, remaining_rows.as_ref());
        if batch_target == 0 {
            break;
        }
        let mut batch: Vec<Encoded> = Vec::with_capacity(batch_target);

        if matches!(spec.context_reset, ContextReset::Batch) {
            carry.clear();
        }

        for _ in 0..batch_target {
            match spec.context_reset {
                ContextReset::Row => ctx.clear(),
                ContextReset::Batch | ContextReset::Never => ctx.clone_from(&carry),
            }

            for &field_index in spec.generation_order.iter() {
                let field = &mut fields[field_index];
                let value = match field.generator.generate(&ctx, &mut rng) {
                    Ok(value) => value,
                    Err(error) => {
                        let message =
                            format!("failed to generate field '{}': {error:#}", field.name);
                        let _ = tx.send(Err(message)).await;
                        return Ok(());
                    }
                };
                ctx.insert(field.name.clone(), value);
            }

            if !matches!(spec.context_reset, ContextReset::Row) {
                carry.clone_from(&ctx);
            }

            let mut payload = Vec::new();
            if let Err(error) = encoder.encode(&ctx, &framing, &mut payload) {
                let _ = tx.send(Err(format!("{error:#}"))).await;
                return Ok(());
            }
            let key = key_field
                .as_deref()
                .and_then(|name| ctx.get(name))
                .map(|value| value.csv_string("").into_bytes());
            batch.push(Encoded { key, payload });
        }

        if tx.send(Ok(batch)).await.is_err() {
            break;
        }
    }

    Ok(())
}

/// Claim up to `batch_rows` rows from the shared budget, or all of them when
/// there is no budget.
pub fn reserve_batch(batch_rows: usize, remaining_rows: Option<&Arc<AtomicU64>>) -> usize {
    let batch_rows = batch_rows.max(1);
    let Some(remaining) = remaining_rows else {
        return batch_rows;
    };

    loop {
        let current = remaining.load(Ordering::Relaxed);
        if current == 0 {
            return 0;
        }
        let take = current.min(batch_rows as u64);
        if remaining
            .compare_exchange(current, current - take, Ordering::SeqCst, Ordering::SeqCst)
            .is_ok()
        {
            return take as usize;
        }
    }
}

/// What every sink thread shares: the queue, the limits, the clock.
struct Shared {
    rx: Mutex<mpsc::Receiver<BatchMessage>>,
    limiter: RateLimiter,
    started: Instant,
    time_limit: Option<Duration>,
    stop: std::sync::atomic::AtomicBool,
}

impl Shared {
    fn out_of_time(&self) -> bool {
        self.time_limit.is_some_and(|limit| self.started.elapsed() >= limit)
    }
}

async fn consumer_loop(shared: Arc<Shared>, mut sink: Box<dyn Sink>) -> Result<Summary> {
    let mut summary = Summary::default();
    let result = async {
        'batches: loop {
            if shared.stop.load(Ordering::Relaxed) || shared.out_of_time() {
                break;
            }
            // Hold the lock only to take a batch, never while writing it.
            let message = shared.rx.lock().await.recv().await;
            let Some(message) = message else { break };
            let batch = match message {
                Ok(batch) => batch,
                Err(message) => bail!("{message}"),
            };
            for encoded in &batch {
                if shared.out_of_time() {
                    break 'batches;
                }
                shared.limiter.limit_row().await;
                shared.limiter.limit_bytes(encoded.payload.len() as u64).await;
                sink.write(encoded).await?;
                summary.rows += 1;
                summary.bytes += encoded.payload.len() as u64;
            }
            sink.flush().await?;
        }
        Ok(())
    }
    .await;
    // Finish even after an error so files are flushed and Kafka is drained.
    let finished = sink.finish().await;
    result?;
    finished?;
    Ok(summary)
}

struct RateLimiter {
    rows: Option<TokenQuota>,
    bytes: Option<TokenQuota>,
}

impl RateLimiter {
    fn new(rows_per_second: Option<u64>, bytes_per_second: Option<u64>) -> Self {
        Self {
            rows: rows_per_second.map(TokenQuota::new),
            bytes: bytes_per_second.map(TokenQuota::new),
        }
    }

    async fn limit_row(&self) {
        if let Some(quota) = &self.rows {
            quota.acquire(1).await;
        }
    }

    async fn limit_bytes(&self, bytes: u64) {
        if let Some(quota) = &self.bytes {
            quota.acquire(bytes).await;
        }
    }
}

struct TokenQuota {
    quota: Arc<precise_rate_limiter::FastQuota>,
    max_acquire: usize,
}

impl TokenQuota {
    fn new(rate_per_second: u64) -> Self {
        let rate = rate_per_second.max(1) as usize;
        // At 100 or more per second, refill a hundredth every 10ms; below
        // that, one token at a time at the exact interval. Two seconds of
        // burst either way.
        let (refill_amount, interval_ms): (usize, u64) = if rate >= 100 {
            (rate / 100, 10)
        } else {
            (1, 1000 / rate as u64)
        };
        let burst = rate * 2;
        let quota = precise_rate_limiter::FastQuota::new(
            burst,
            refill_amount,
            Duration::from_millis(interval_ms),
        );
        Self { quota, max_acquire: burst }
    }

    async fn acquire(&self, mut tokens: u64) {
        while tokens > 0 {
            let chunk = tokens.min(self.max_acquire as u64) as usize;
            self.quota.acquire(chunk).await;
            tokens -= chunk as u64;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::compile_yaml;
    use crate::sink::{FileLayout, FileSink};
    use crate::text::{OutputColumn, TextEncoder, TextFormat};

    fn spec(extra: &str) -> CompiledSpec {
        compile_yaml(&format!(
            "version: 1\nfields:\n  - name: id\n    gen: {{type: int_range, min: 1, max: 9}}\n  - name: word\n    gen: {{type: choice, values: [a, b]}}\n{}",
            extra
        ))
        .unwrap()
    }

    fn text_encoder(spec: &CompiledSpec, format: TextFormat) -> Arc<RecordEncoder> {
        let columns = OutputColumn::from_spec(spec);
        Arc::new(RecordEncoder::Text(TextEncoder::new(format, &columns, spec.csv.clone())))
    }

    fn options(threads: usize, rows: u64) -> StreamOptions {
        StreamOptions {
            threads,
            sink_threads: 1,
            queue_depth: 3,
            batch_rows: None,
            rows: Some(rows),
            time_limit: None,
            rows_per_second: None,
            bytes_per_second: None,
            key_field: None,
        }
    }

    fn file_sinks(dir: &std::path::Path, prelude: Option<Vec<u8>>, file_size: Option<u64>, files_per_folder: u64, extension: &str, tagged: bool) -> SinkFactory {
        let dir = dir.to_path_buf();
        let extension = extension.to_string();
        Box::new(move |index| {
            let layout = FileLayout {
                directory: dir.clone(),
                file_name_prefix: "part-".into(),
                folder_prefix: "batch-".into(),
                files_per_folder,
                file_size,
                extension: extension.clone(),
                writer: tagged.then_some(index + 1),
            };
            Ok(Box::new(FileSink::new(layout, prelude.clone())) as Box<dyn Sink>)
        })
    }

    fn walk(dir: &std::path::Path) -> Vec<std::path::PathBuf> {
        let mut out = Vec::new();
        for entry in std::fs::read_dir(dir).unwrap() {
            let path = entry.unwrap().path();
            if path.is_dir() {
                out.extend(walk(&path));
            } else {
                out.push(path);
            }
        }
        out.sort();
        out
    }

    #[tokio::test]
    async fn writes_exactly_the_requested_rows_with_one_header_per_file() {
        let dir = std::env::temp_dir().join(format!("datagen-stream-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let spec = spec("");
        let encoder = text_encoder(&spec, TextFormat::Csv);
        let prelude = encoder.prelude(&Framing::Lines).unwrap();
        let sinks = file_sinks(&dir, prelude, Some(2000), 2, "csv", false);
        let summary = run(spec, encoder, Framing::Lines, sinks, options(4, 1000)).await.unwrap();
        assert_eq!(summary.rows, 1000);

        let files = walk(&dir);
        assert!(files.len() > 1, "2000-byte pieces should split 1000 rows: {:?}", files);
        let mut rows = 0;
        for file in &files {
            let text = std::fs::read_to_string(file).unwrap();
            assert!(text.starts_with("id,word\n"), "{:?}", file);
            rows += text.lines().count() - 1;
        }
        assert_eq!(rows, 1000, "every row must be written exactly once across the pieces");
        std::fs::remove_dir_all(&dir).ok();
    }

    #[tokio::test]
    async fn several_sink_threads_share_the_rows_without_collisions() {
        let dir = std::env::temp_dir().join(format!("datagen-stream-multi-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let spec = spec("");
        let encoder = text_encoder(&spec, TextFormat::Csv);
        let prelude = encoder.prelude(&Framing::Lines).unwrap();
        let sinks = file_sinks(&dir, prelude, Some(500), 0, "csv", true);
        let mut options = options(4, 3000);
        options.sink_threads = 3;
        options.batch_rows = Some(100);
        let summary = run(spec, encoder, Framing::Lines, sinks, options).await.unwrap();
        assert_eq!(summary.rows, 3000);
        let files = walk(&dir);
        let writers: std::collections::HashSet<String> = files
            .iter()
            .map(|f| f.file_name().unwrap().to_string_lossy()[5..8].to_string())
            .collect();
        assert_eq!(writers.len(), 3, "every writer tagged its files: {:?}", files);
        let rows: usize = files.iter().map(|f| std::fs::read_to_string(f).unwrap().lines().count() - 1).sum();
        assert_eq!(rows, 3000);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[tokio::test]
    async fn json_lines_have_no_header_and_parse() {
        let dir = std::env::temp_dir().join(format!("datagen-stream-json-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let spec = spec("");
        let encoder = text_encoder(&spec, TextFormat::Json);
        let prelude = encoder.prelude(&Framing::Lines).unwrap();
        assert!(prelude.is_none());
        let sinks = file_sinks(&dir, prelude, None, 0, "json", false);
        run(spec, encoder, Framing::Lines, sinks, options(2, 50)).await.unwrap();
        let text = std::fs::read_to_string(dir.join("part-000001.json")).unwrap();
        assert_eq!(text.lines().count(), 50);
        for line in text.lines() {
            let value: serde_json::Value = serde_json::from_str(line).expect("valid JSON line");
            assert!(value["id"].is_number() && value["word"].is_string(), "{}", line);
        }
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn stateful_specs_get_one_thread() {
        assert_eq!(effective_threads(&spec(""), 8), 8);
        let stateful = spec("  - name: seq\n    gen: {type: sequence, start: 1, step: 1}\n");
        assert_eq!(effective_threads(&stateful, 8), 1);
        assert_eq!(effective_threads(&spec(""), 0), 1, "zero threads means one");
    }

    #[tokio::test]
    async fn an_unknown_key_field_is_refused() {
        let spec = spec("");
        let encoder = text_encoder(&spec, TextFormat::Json);
        let sinks: SinkFactory = Box::new(|_| Ok(Box::new(crate::sink::ConsoleSink::new(None)) as Box<dyn Sink>));
        let mut options = options(1, 1);
        options.key_field = Some("nope".into());
        let error = run(spec, encoder, Framing::Lines, sinks, options).await.expect_err("bad key");
        assert!(error.to_string().contains("key_field"), "{}", error);
    }
}
