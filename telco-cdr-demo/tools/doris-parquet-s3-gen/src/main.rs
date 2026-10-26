
mod derive;
// format_bytes and group_digits are for calcsize; this binary uses neither.
#[allow(dead_code)]
mod parquet_out;
mod probe;
// Fields become live when the S3 sink lands.
#[allow(dead_code)]
mod s3;
mod schema;
#[allow(dead_code)]
mod sink;
#[allow(dead_code)]
mod status;

use std::{
    collections::HashSet,
    io::{self, Write},
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicU64, Ordering},
        Arc,
    },
    time::{Duration, Instant},
};

use anyhow::{anyhow, bail, Context, Result};
use chrono::Utc;
use clap::{ArgGroup, Parser};
use rand::{prelude::*, rngs::StdRng, SeedableRng};
use datagen::compile::*;
use datagen::generators::*;
use datagen::spec::*;
use datagen::units;

use crate::units::{parse_byte_size, parse_duration};

#[derive(Parser, Debug)]
#[command(version, about = "Generate data from a Doris schema and stream it to S3 as Parquet")]
#[command(group(
    ArgGroup::new("rate")
        ))]
struct Args {
    #[arg(long)]
    spec: Option<PathBuf>,

    /// Stop after this many rows. Overrides run.rows and run.target_size in
    /// the config for this run.
    #[arg(long)]
    rows: Option<u64>,

    /// Stop after this long, e.g. 30m. Overrides run.time in the config.
    #[arg(long, value_parser = parse_duration)]
    time: Option<Duration>,

    /// Doris CREATE TABLE file. Supplies Parquet column types and order.
    /// Combine with --spec to keep the schema's types and your own generators.
    #[arg(long, value_name = "FILE")]
    schema: Option<PathBuf>,

    /// Table to use when --schema declares more than one.
    #[arg(long, requires = "schema", value_name = "NAME")]
    table: Option<String>,

    /// Pre-flight: write the field spec derived from --schema, then exit.
    /// Edit the result and pass it back with --spec. Use `-` for stdout.
    #[arg(long, requires = "schema", value_name = "FILE")]
    emit_spec: Option<PathBuf>,

    /// Where the files go: a TOML naming an S3 bucket or a local directory,
    /// plus layout, upload and Parquet settings. See --emit-config.
    #[arg(long, value_name = "FILE")]
    config: Option<PathBuf>,

    /// Pre-flight: write a commented starter config, then exit. `-` for stdout.
    #[arg(long, value_name = "FILE")]
    emit_config: Option<PathBuf>,

    /// Stop once this much Parquet has been produced, e.g. 10GiB. Overrides
    /// run.target_size and run.rows in the config for this run.
    #[arg(long, value_parser = parse_byte_size, conflicts_with = "rows", value_name = "SIZE")]
    target_size: Option<u64>,

    /// Shorthand for a config whose destination is this local directory,
    /// with every other setting at its default.
    #[arg(long, conflicts_with = "config", value_name = "DIR")]
    out_dir: Option<PathBuf>,

    /// Suppress the live status display.
    #[arg(long)]
    no_progress: bool,
}


/// Short byte rendering for startup messages.
fn fmt_bytes_short(value: u64) -> String {
    const UNITS: [(u64, &str); 3] = [(1u64 << 30, "GiB"), (1u64 << 20, "MiB"), (1u64 << 10, "KiB")];
    for (scale, suffix) in UNITS {
        if value >= scale {
            return format!("{:.0}{}", value as f64 / scale as f64, suffix);
        }
    }
    format!("{}B", value)
}

/// Write a pre-flight file for the user to edit. Refuses to clobber.
fn write_text_file(dest: &Path, text: &str, what: &str) -> Result<()> {
    if dest == Path::new("-") {
        let mut stdout = io::stdout().lock();
        stdout.write_all(text.as_bytes())?;
        stdout.flush()?;
        return Ok(());
    }
    if dest.exists() {
        bail!(
            "{} already exists; delete it or choose another path",
            dest.display()
        );
    }
    std::fs::write(dest, text)
        .with_context(|| format!("failed to write {}", dest.display()))?;
    eprintln!("wrote {} {}", what, dest.display());
    Ok(())
}

/// Write a derived spec for the user to edit. Refuses to clobber an existing file.
fn write_emitted_spec(dest: &Path, spec_text: &str) -> Result<()> {
    if dest == Path::new("-") {
        let mut stdout = io::stdout().lock();
        stdout.write_all(spec_text.as_bytes())?;
        stdout.flush()?;
        return Ok(());
    }
    if dest.exists() {
        bail!(
            "{} already exists; delete it or choose another path",
            dest.display()
        );
    }
    std::fs::write(dest, spec_text)
        .with_context(|| format!("failed to write {}", dest.display()))?;
    eprintln!("wrote {}", dest.display());
    Ok(())
}

/// Load the Doris DDL and pick the table to generate for.
fn load_schema_table(path: &Path, table: Option<&str>) -> Result<schema::Table> {
    let sql = std::fs::read_to_string(path)
        .with_context(|| format!("failed to read schema {}", path.display()))?;
    let tables = schema::parse_schema(&sql)
        .with_context(|| format!("failed to parse schema {}", path.display()))?;
    schema::select_table(tables, table)
}

/// Every schema column needs a generator. Extra spec fields are allowed:
/// they act as hidden intermediates and are simply not written.
fn validate_spec_covers(spec: &CompiledSpec, columns: &[schema::Column]) -> Result<()> {
    let generated: HashSet<&str> = spec.fields.iter().map(|f| f.name.as_str()).collect();
    let missing: Vec<&str> = columns
        .iter()
        .map(|column| column.name.as_str())
        .filter(|name| !generated.contains(name))
        .collect();
    if !missing.is_empty() {
        bail!(
            "the spec has no field for these schema columns: {}",
            missing.join(", ")
        );
    }
    Ok(())
}


/// Generate rows into record batches and hand them to the queue.
///
/// This never touches the network. Upload work happens in `upload_worker`, so
/// a row-group encode or a slow part upload cannot stall generation; the queue
/// absorbs the difference.
#[allow(clippy::too_many_arguments)]
async fn generator_loop(
    spec: CompiledSpec,
    columns: Vec<schema::Column>,
    queue: async_channel::Sender<arrow::record_batch::RecordBatch>,
    batch_rows: usize,
    remaining_rows: Option<Arc<AtomicU64>>,
    target_bytes: Option<u64>,
    stats: Arc<sink::Stats>,
    deadline: Option<Instant>,
) -> Result<()> {
    let mut rng = StdRng::from_entropy();
    let mut fields = (*spec.fields).clone();
    let mut ctx = RowContext::with_capacity(spec.fields.len());
    let mut carry = RowContext::new();
    let mut builder = parquet_out::BatchBuilder::new(columns, batch_rows)?;

    loop {
        if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
            break;
        }
        if target_bytes.is_some_and(|target| reached_size_target(&stats, target)) {
            break;
        }
        let batch_target = datagen::stream::reserve_batch(batch_rows, remaining_rows.as_ref());
        if batch_target == 0 {
            break;
        }

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
                let value = field
                    .generator
                    .generate(&ctx, &mut rng)
                    .with_context(|| format!("failed to generate field `{}`", field.name))?;
                ctx.insert(field.name.clone(), value);
            }
            if !matches!(spec.context_reset, ContextReset::Row) {
                carry.clone_from(&ctx);
            }
            builder.append_row(|name| ctx.get(name))?;
        }

        let rows = builder.rows() as u64;
        let batch = builder.finish()?;
        // Blocks only when the queue is full, which is the backpressure we want.
        if queue.send(batch).await.is_err() {
            // Every upload worker is gone; nothing left to do.
            break;
        }
        stats
            .rows_generated
            .fetch_add(rows, Ordering::Relaxed);
    }
    Ok(())
}

/// Drain the queue, writing batches to this worker's own Parquet files.
/// Runs until the queue is closed and empty, then closes the file in flight.
/// Drain the queue into one Parquet writer.
///
/// A failure here has already been through every HTTP-level retry the store
/// was configured with, so the question is what a run measured in days should
/// do about it. Taking the whole job down over one file is the wrong answer:
/// the writer abandons that file, counts it, and keeps draining. `file_retries`
/// bounds how much of that is tolerable before the run really does fail, and
/// zero restores failing on the first error.
async fn upload_worker(
    queue: async_channel::Receiver<arrow::record_batch::RecordBatch>,
    mut sink: sink::ParquetSink,
    file_retries: u32,
    writer_id: usize,
    remaining_rows: Option<Arc<AtomicU64>>,
) -> Result<()> {
    let mut abandoned = 0u32;
    while let Ok(batch) = queue.recv().await {
        let Err(error) = sink.write(batch).await else {
            continue;
        };
        abandoned += 1;
        if abandoned > file_retries {
            return Err(error).with_context(|| {
                format!(
                    "writer {} gave up after {} failed files; raise upload.file_retries \
                     to tolerate more, or upload.retry_timeout to retry each request longer",
                    writer_id, abandoned
                )
            });
        }
        eprintln!(
            "writer {}: upload failed ({} of {} tolerated), abandoning this file \
             and starting a new one: {:#}",
            writer_id, abandoned, file_retries, error
        );
        let lost = sink.abandon_active().await;
        // A --rows run spends its quota when a row is generated, not when it
        // lands, so without this the run would finish short by exactly the
        // rows in the file just thrown away. Putting them back has the
        // generators make up the difference, provided they are still running.
        // A --target-size run needs no such help: abandoned bytes never reach
        // the counter the target is measured against.
        if let Some(remaining) = remaining_rows.as_ref() {
            remaining.fetch_add(lost, Ordering::Relaxed);
        }
    }
    // Always close: an unclosed multipart upload never becomes an object.
    if let Err(error) = sink.finish().await {
        abandoned += 1;
        if abandoned > file_retries {
            return Err(error)
                .with_context(|| format!("writer {} failed to close its last file", writer_id));
        }
        eprintln!(
            "writer {}: could not close the last file ({} of {} tolerated): {:#}",
            writer_id, abandoned, file_retries, error
        );
    }
    Ok(())
}

/// Redraw the status block once a second while generation runs.
#[allow(clippy::too_many_arguments)]
async fn status_loop(
    stats: Arc<sink::Stats>,
    threads: usize,
    writers: usize,
    queue: async_channel::Receiver<arrow::record_batch::RecordBatch>,
    target_rows: Option<u64>,
    target_bytes: Option<u64>,
    file_cap: Option<u64>,
    started: Instant,
    draining: Arc<std::sync::atomic::AtomicBool>,
    done: Arc<std::sync::atomic::AtomicBool>,
) {
    use std::io::IsTerminal;
    let interactive = io::stderr().is_terminal();
    let mut painted = 0usize;
    // Bytes only materialise when a row group finishes encoding, which happens
    // every few seconds. A one-second delta therefore reads 0, then a spike.
    // Averaging over a short window reports the real throughput instead.
    const RATE_WINDOW: Duration = Duration::from_secs(5);
    let mut history: std::collections::VecDeque<(Instant, u64, u64)> =
        std::collections::VecDeque::new();

    loop {
        let finished = done.load(Ordering::Relaxed);
        let now = Instant::now();
        let rows = stats.rows_generated.load(Ordering::Relaxed);
        let bytes = stats.total_bytes();
        history.push_back((now, rows, bytes));
        while history.len() > 1
            && now.duration_since(history.front().expect("non-empty").0) > RATE_WINDOW
        {
            history.pop_front();
        }
        let (oldest, base_rows, base_bytes) = *history.front().expect("just pushed");
        let seconds = now.duration_since(oldest).as_secs_f64();
        // A single sample carries no interval, so report nothing rather than
        // dividing by an arbitrary epsilon and printing a wild number.
        let (rows_per_sec, bytes_per_sec) = if seconds < 0.5 {
            (0.0, 0.0)
        } else {
            (
                rows.saturating_sub(base_rows) as f64 / seconds,
                bytes.saturating_sub(base_bytes) as f64 / seconds,
            )
        };

        let snapshot = status::Snapshot {
            elapsed: now.duration_since(started),
            threads,
            writers,
            queued: queue.len(),
            queue_capacity: queue.capacity().unwrap_or(0),
            rows,
            target_rows,
            bytes_generated: bytes,
            bytes_uploaded: bytes,
            target_bytes,
            buffered_bytes: stats.buffered_bytes.load(Ordering::Relaxed),
            files_completed: stats.files_completed.load(Ordering::Relaxed),
            active_files: Vec::new(),
            rows_per_sec,
            // Generation produces rows, not bytes: nothing weighs anything
            // until a row group is encoded. Only the upload side has a real
            // byte rate, so do not invent a second one from the same delta.
            gen_bytes_per_sec: 0.0,
            upload_bytes_per_sec: bytes_per_sec,
            file_cap,
            draining: draining.load(Ordering::Relaxed) && !finished,
            finished,
        };
        let width = terminal_width();
        let lines = status::render(&snapshot, width);
        if interactive {
            let mut out = String::new();
            if painted > 0 {
                // Move back over the previous block and clear each line.
                out.push_str(&format!("\x1b[{}A", painted));
            }
            for line in &lines {
                out.push_str("\x1b[2K");
                out.push_str(line);
                out.push('\n');
            }
            eprint!("{}", out);
            let _ = io::stderr().flush();
            painted = lines.len();
        } else if finished || now.duration_since(started).as_secs() % 10 == 0 {
            // Non-interactive output would scroll, so keep it sparse.
            eprintln!("{}", lines.join(" | "));
        }

        if finished {
            break;
        }
        tokio::time::sleep(Duration::from_secs(1)).await;
    }
}

/// Has enough data been produced to satisfy a size target?
///
/// Bytes only become known when a row group is encoded, so rows sitting in the
/// queue or in a half-built row group weigh nothing yet. Projecting the
/// observed bytes-per-row over every row generated counts them, which stops
/// generation at the right moment instead of long past it.
fn reached_size_target(stats: &sink::Stats, target: u64) -> bool {
    let produced = stats.total_bytes();
    if produced >= target {
        return true;
    }
    // Pair the byte total with the rows those bytes actually came from.
    // Using every row handed to a writer would divide known bytes by more rows
    // than produced them, understating bytes-per-row and overshooting.
    let flushed = stats.rows_flushed.load(Ordering::Relaxed);
    if flushed == 0 || produced == 0 {
        return false;
    }
    let generated = stats.rows_generated.load(Ordering::Relaxed);
    let projected = (produced as u128) * (generated as u128) / (flushed as u128);
    projected >= target as u128
}

/// Generation threads default to the machine's CPU count.
fn default_threads() -> usize {
    std::thread::available_parallelism()
        .map(|count| count.get())
        .unwrap_or(4)
}

/// Rough peak memory one upload worker holds: the row group it is building
/// plus the multipart parts it has in flight.
fn estimate_writer_bytes(
    row_group_rows: usize,
    columns: &[schema::Column],
    part_size: usize,
) -> u64 {
    // A crude per-value figure; string columns dominate real schemas.
    let bytes_per_value = 24u64;
    let row_group = row_group_rows as u64 * columns.len() as u64 * bytes_per_value;
    row_group + part_size as u64
}

/// Terminal width, falling back to 80 when it cannot be determined.
fn terminal_width() -> usize {
    std::env::var("COLUMNS")
        .ok()
        .and_then(|value| value.parse::<usize>().ok())
        .or_else(|| {
            let output = std::process::Command::new("stty")
                .arg("size")
                .stdin(std::process::Stdio::inherit())
                .output()
                .ok()?;
            let text = String::from_utf8_lossy(&output.stdout);
            text.split_whitespace().nth(1)?.parse::<usize>().ok()
        })
        .filter(|width| *width >= 20)
        .unwrap_or(80)
}

/// Spawn one writer per thread, run them to completion, then report.
async fn run_parquet(
    args: Args,
    compiled: CompiledSpec,
    table: schema::Table,
    config: s3::OutputConfig,
) -> Result<()> {
    use std::sync::atomic::AtomicBool;

    let requested = if config.run.threads == 0 { default_threads() } else { config.run.threads };
    let mut gen_threads = requested.max(1);
    if compiled.has_stateful_ordered && gen_threads > 1 {
        eprintln!(
            "stateful ordered generators detected; generation threads reduced from {} to 1",
            gen_threads
        );
        gen_threads = 1;
    }
    compiled.check_sequence_capacity(config.run.rows, gen_threads)?;
    let upload_threads = config.run.upload_threads.max(1);
    let queue_depth = config.run.queue_depth.max(1);

    // A local run writes straight to a file, so there is no upload to give up
    // on and nothing to tolerate.
    let file_retries = if config.is_s3() { config.upload.file_retries } else { 0 };
    let destination = if config.is_s3() {
        sink::Destination::S3 { config: Box::new(config.clone()) }
    } else {
        sink::Destination::Local { directory: config.local_directory()?.to_path_buf() }
    };
    // The command line's level wins over the config's for this run.
    let compression =
        sink::parse_compression(&config.parquet.compression, config.parquet.compression_level)?;

    let columns = table.columns.clone();
    let schema_ref = parquet_out::arrow_schema(&columns)?;
    // One id for the whole run, shared by every writer through the settings.
    let run_id = config.layout.run_id.clone().unwrap_or_else(default_run_id);
    let files_per_folder = config.layout.files_per_folder;
    let file_size = config.layout.file_size;
    let settings = Arc::new(sink::SinkSettings {
        run_id: run_id.clone(),
        files_per_folder,
        file_counter: Arc::new(AtomicU64::new(0)),
        schema: schema_ref,
        row_group_rows: config.parquet.row_group_rows,
        part_size: config.upload.part_size as usize,
        max_concurrent_parts: config.upload.max_concurrent_parts,
        file_cap: Some(file_size),
        compression,
        dictionary: config.parquet.dictionary,
    });
    let row_group_rows = config.parquet.row_group_rows;
    let part_size = config.upload.part_size as usize;

    let (store, prefix) = sink::build_store(&destination)?;
    let stats = Arc::new(sink::Stats::default());

    if config.is_s3() {
        let s3 = config.s3()?;
        eprintln!(
            "writing Parquet to s3://{}/{}  files roll at {}  part {} x {} in flight",
            s3.bucket,
            config.normalised_prefix(),
            fmt_bytes_short(file_size),
            fmt_bytes_short(config.upload.part_size),
            config.upload.max_concurrent_parts,
        );
    } else {
        eprintln!(
            "writing Parquet to {}  files roll at {}",
            config.local_directory()?.display(),
            fmt_bytes_short(file_size),
        );
    }
    // Sketch columns carry source values; the load must build the sketches.
    let sketches = parquet_out::sketch_load_expressions(&columns);
    if !sketches.is_empty() {
        eprintln!("sketch columns carry source values; load them with:");
        for (name, expression) in &sketches {
            eprintln!("  `{}` <- {}", name, expression);
        }
    }
    // Say where this run's objects will land, since the run folder is what a
    // Doris load or a cleanup has to point at.
    if !run_id.is_empty() {
        if files_per_folder > 0 {
            eprintln!(
                "run folder {}/  batches of {} files (~{} each) in batch-NNNNN/",
                run_id,
                files_per_folder,
                fmt_bytes_short(file_size.saturating_mul(files_per_folder)),
            );
        } else {
            eprintln!("run folder {}/", run_id);
        }
    }
    let batch_rows = config.run.batch_rows.max(1).min(row_group_rows.max(1));
    eprintln!(
        "table `{}`  {} columns  {} generator + {} upload thread(s)  queue {} x {} rows",
        table.name,
        columns.len(),
        gen_threads,
        upload_threads,
        queue_depth,
        batch_rows,
    );
    // Peak memory is dominated by what each upload worker holds open.
    let per_writer = estimate_writer_bytes(row_group_rows, &columns, part_size);
    eprintln!(
        "estimated peak memory ~{} ({} per upload worker x {})",
        fmt_bytes_short(per_writer * upload_threads as u64),
        fmt_bytes_short(per_writer),
        upload_threads,
    );

    let started = Instant::now();
    let deadline = config.run.time.map(|limit| started + limit);
    let remaining_rows = config.run.rows.map(AtomicU64::new).map(Arc::new);

    let (tx, rx) = async_channel::bounded(queue_depth);

    let done = Arc::new(AtomicBool::new(false));
    let draining = Arc::new(AtomicBool::new(false));
    let status = if args.no_progress {
        None
    } else {
        Some(tokio::spawn(status_loop(
            stats.clone(),
            gen_threads,
            upload_threads,
            rx.clone(),
            config.run.rows,
            config.run.target_size,
            Some(file_size),
            started,
            draining.clone(),
            done.clone(),
        )))
    };

    // Upload workers start first so they are ready to drain immediately.
    let mut uploaders = Vec::with_capacity(upload_threads);
    for writer_id in 0..upload_threads {
        let sink = sink::ParquetSink::new(
            store.clone(),
            prefix.clone(),
            writer_id,
            settings.clone(),
            stats.clone(),
        );
        uploaders.push(tokio::spawn(upload_worker(
            rx.clone(),
            sink,
            file_retries,
            writer_id,
            remaining_rows.clone(),
        )));
    }
    // Only the workers should hold receivers, so the queue can close cleanly.
    drop(rx);

    let mut generators = Vec::with_capacity(gen_threads);
    for _ in 0..gen_threads {
        generators.push(tokio::spawn(generator_loop(
            compiled.clone(),
            columns.clone(),
            tx.clone(),
            batch_rows,
            remaining_rows.clone(),
            config.run.target_size,
            stats.clone(),
            deadline,
        )));
    }
    // Dropping the last sender is what eventually closes the queue.
    drop(tx);

    let mut failure: Option<anyhow::Error> = None;
    for generator in generators {
        match generator.await {
            Ok(Ok(())) => {}
            Ok(Err(error)) => {
                failure.get_or_insert(error);
            }
            Err(error) => {
                failure.get_or_insert(anyhow!("generator task panicked: {}", error));
            }
        }
    }
    // Generators are done and their senders dropped, so the workers now drain
    // whatever is left in the queue and finish their files.
    draining.store(true, Ordering::Relaxed);
    for uploader in uploaders {
        match uploader.await {
            Ok(Ok(())) => {}
            Ok(Err(error)) => {
                failure.get_or_insert(error);
            }
            Err(error) => {
                failure.get_or_insert(anyhow!("upload worker panicked: {}", error));
            }
        }
    }

    done.store(true, Ordering::Relaxed);
    if let Some(status) = status {
        let _ = status.await;
    }

    if let Some(error) = failure {
        return Err(error);
    }

    let elapsed = started.elapsed();
    let rows = stats.rows.load(Ordering::Relaxed);
    let bytes = stats.bytes_written.load(Ordering::Relaxed);
    debug_assert_eq!(rows, stats.rows_generated.load(Ordering::Relaxed));
    let abandoned = stats.files_abandoned.load(Ordering::Relaxed);
    eprintln!(
        "done: {} rows in {} files, {} in {:.1}s ({} rows/s){}",
        rows,
        stats.files_completed.load(Ordering::Relaxed),
        fmt_bytes_short(bytes),
        elapsed.as_secs_f64(),
        (rows as f64 / elapsed.as_secs_f64()) as u64,
        if abandoned == 0 {
            String::new()
        } else {
            format!(
                "  [{} file(s) abandoned after repeated upload failures, \
                 {} rows lost]",
                abandoned,
                stats.rows_lost.load(Ordering::Relaxed)
            )
        },
    );
    Ok(())
}

fn main() -> Result<()> {
    let args = Args::parse();

    if let Some(dest) = args.emit_config.as_ref() {
        return write_text_file(dest, s3::SAMPLE_TOML, "config");
    }

    // Fail on a bad config before starting anything, and size the runtime
    // from it: Parquet encoding is synchronous CPU work that occupies a
    // worker for its duration, so the pool holds generators plus upload
    // workers with room to spare.
    let config = match (args.config.as_ref(), args.out_dir.as_ref()) {
        (Some(path), _) => s3::load(path)?,
        (None, Some(directory)) => s3::OutputConfig::local(directory.clone()),
        (None, None) => {
            use clap::CommandFactory;
            Args::command().print_help()?;
            eprintln!("\nerror: a destination is required: --config <FILE> or --out-dir <DIR>");
            std::process::exit(2);
        }
    };
    let generators = if config.run.threads == 0 { default_threads() } else { config.run.threads };
    let workers = generators.max(1) + config.run.upload_threads.max(1) + 2;
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(workers.min(64))
        .enable_all()
        .build()
        .context("failed to start the async runtime")?;
    runtime.block_on(run(args, config))
}

async fn run(args: Args, config: s3::OutputConfig) -> Result<()> {
    // The command line's stop conditions replace the config's for this run.
    let mut config = config;
    match (args.rows, args.target_size) {
        (Some(rows), _) => {
            config.run.rows = Some(rows);
            config.run.target_size = None;
        }
        (None, Some(size)) => {
            config.run.rows = None;
            config.run.target_size = Some(size);
        }
        (None, None) => {}
    }
    if let Some(limit) = args.time {
        config.run.time = Some(limit);
    }

    // The schema supplies Parquet column types, order and nullability.
    let Some(schema_path) = args.schema.as_ref() else {
        use clap::CommandFactory;
        Args::command().print_help()?;
        eprintln!("\nerror: --schema <FILE> is required: column types come from the Doris DDL");
        std::process::exit(2);
    };
    let table = load_schema_table(schema_path, args.table.as_deref())?;

    if let Some(dest) = args.emit_spec.as_ref() {
        let yaml = derive::spec_yaml_from_table(&table)?;
        return write_emitted_spec(dest, &yaml);
    }

    // A hand-written spec wins over one derived from the schema.
    let spec_text = if let Some(spec_path) = args.spec.as_ref() {
        std::fs::read_to_string(spec_path)
            .with_context(|| format!("failed to read spec {}", spec_path.display()))?
    } else {
        eprintln!("deriving a spec for table `{}`: {} columns", table.name, table.columns.len());
        derive::spec_yaml_from_table(&table)?
    };
    let raw: RawSpec = serde_yaml::from_str(&spec_text).context("failed to parse YAML spec")?;
    let compiled = compile_spec(raw)?;

    validate_spec_covers(&compiled, &table.columns)?;
    // Push every generator's extreme values through the real conversion now,
    // so a spec that cannot fit fails here and not an hour in.
    probe::validate_against_schema(&compiled, &table.columns)?;
    run_parquet(args, compiled, table, config).await
}


/// A UTC timestamp plus a few random characters. The timestamp alone would
/// collide between two generators started against the same prefix in the same
/// second, which is exactly how a large run gets split across machines.
fn default_run_id() -> String {
    let mut suffix = [0u8; 3];
    rand::thread_rng().fill_bytes(&mut suffix);
    format!(
        "{}-{}",
        Utc::now().format("%Y%m%dT%H%M%SZ"),
        hex_encode(&suffix)
    )
}



#[cfg(test)]
mod tests {
    use super::*;
    use datagen::{FieldGenerator, Value};
    use rand::SeedableRng;
    use std::collections::HashSet;

    fn compile_yaml(yaml: &str) -> Result<CompiledSpec> {
        let raw = serde_yaml::from_str::<RawSpec>(yaml)?;
        compile_spec(raw)
    }

    fn generate_one_row(spec: &CompiledSpec) -> Result<RowContext> {
        let mut rng = StdRng::seed_from_u64(42);
        let mut fields = (*spec.fields).clone();
        let mut ctx = RowContext::new();
        for &field_index in spec.generation_order.iter() {
            let field = &mut fields[field_index];
            let value = field.generator.generate(&ctx, &mut rng)?;
            ctx.insert(field.name.clone(), value);
        }
        Ok(ctx)
    }

    #[test]
    fn parse_byte_size_variants() {
        assert_eq!(parse_byte_size("1000").unwrap(), 1000);
        assert_eq!(parse_byte_size("1k").unwrap(), 1024);
        assert_eq!(parse_byte_size("1K").unwrap(), 1024);
        assert_eq!(parse_byte_size("1KB").unwrap(), 1024);
        assert_eq!(parse_byte_size("1kb").unwrap(), 1024);
        assert_eq!(parse_byte_size("1KiB").unwrap(), 1024);
        assert_eq!(parse_byte_size("1kib").unwrap(), 1024);
        assert_eq!(parse_byte_size("1M").unwrap(), 1024 * 1024);
        assert_eq!(parse_byte_size("1MB").unwrap(), 1024 * 1024);
        assert_eq!(parse_byte_size("1MiB").unwrap(), 1024 * 1024);
        assert_eq!(parse_byte_size("1G").unwrap(), 1024 * 1024 * 1024);
        assert_eq!(parse_byte_size("1GB").unwrap(), 1024 * 1024 * 1024);
        assert_eq!(parse_byte_size("1GiB").unwrap(), 1024 * 1024 * 1024);
        assert_eq!(
            parse_byte_size("23.3KB").unwrap(),
            (23.3f64 * 1024.0).ceil() as u64
        );
        assert_eq!(parse_byte_size("233KiB").unwrap(), 233 * 1024);
        assert_eq!(parse_byte_size("1T").unwrap(), 1024u64.pow(4));
        assert_eq!(parse_byte_size("1TB").unwrap(), 1024u64.pow(4));
        assert_eq!(parse_byte_size("1TiB").unwrap(), 1024u64.pow(4));
        assert_eq!(parse_byte_size("40TiB").unwrap(), 40 * 1024u64.pow(4));
        // 40TiB and 40960GiB have to be the same number of bytes.
        assert_eq!(
            parse_byte_size("40TiB").unwrap(),
            parse_byte_size("40960GiB").unwrap()
        );
        assert_eq!(parse_byte_size("1P").unwrap(), 1024u64.pow(5));
        // Every unit here is a power of 1024, so the B spellings are aliases
        // rather than the decimal units they look like.
        assert_eq!(
            parse_byte_size("1TB").unwrap(),
            parse_byte_size("1TiB").unwrap()
        );
        assert!(parse_byte_size("abc").is_err());
        assert!(parse_byte_size("-1").is_err());
        assert!(parse_byte_size("1XB").is_err());
    }


    #[test]
    fn fuzzy_output_order_uses_i64_default_zero() {
        let spec = compile_yaml(
            r#"
version: 1
fields:
  - name: default_a
    gen:
      type: constant
      value: a
  - name: early
    order: -1
    gen:
      type: constant
      value: early
  - name: default_b
    gen:
      type: constant
      value: b
  - name: late
    order: 1
    gen:
      type: constant
      value: late
"#,
        )
        .expect("spec should compile");

        let output_names = spec
            .output_order
            .iter()
            .map(|&index| spec.fields[index].name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(
            output_names,
            vec!["early", "default_a", "default_b", "late"]
        );
    }

    #[test]
    fn derived_spec_from_doris_schema_compiles_and_generates() {
        let sql = r#"
            CREATE TABLE IF NOT EXISTS tracking_db.vehicle
            (
                `vin`        CHAR(17)       NOT NULL,
                `id`         BIGINT         NOT NULL,
                `email`      VARCHAR(255),
                `speed`      DOUBLE,
                `amount`     DECIMAL(20, 4),
                `day`        DATE,
                `ts`         DATETIME(3),
                `notes`      STRING,
                `flag`       BOOLEAN
            )
            ENGINE=OLAP
            UNIQUE KEY(`vin`)
            DISTRIBUTED BY HASH(`vin`) BUCKETS 32
            PROPERTIES ("replication_num" = "1");
        "#;

        let tables = schema::parse_schema(sql).expect("parse schema");
        let table = schema::select_table(tables, None).expect("select table");
        let yaml = derive::spec_yaml_from_table(&table).expect("derive spec");

        let spec = compile_yaml(&yaml).expect("derived spec must compile");
        assert_eq!(spec.fields.len(), table.columns.len());

        let row = generate_one_row(&spec).expect("generate row");
        for column in &table.columns {
            let value = row
                .get(&column.name)
                .unwrap_or_else(|| panic!("missing generated value for `{}`", column.name));
            assert!(
                !matches!(value, Value::Null),
                "column `{}` generated null unexpectedly",
                column.name
            );
        }

        // A CHAR(17) column must never produce more characters than declared.
        let vin = row.get("vin").unwrap().csv_string("");
        assert!(vin.len() <= 17, "vin `{}` exceeds CHAR(17)", vin);

        // Output column order must follow the DDL.
        let names: Vec<&str> = spec
            .output_order
            .iter()
            .map(|&index| spec.fields[index].name.as_str())
            .collect();
        let expected: Vec<&str> = table.columns.iter().map(|c| c.name.as_str()).collect();
        assert_eq!(names, expected, "output order must match DDL order");
    }

    fn temp_dir(tag: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("dpsg-{}-{}", tag, std::process::id()));
        std::fs::create_dir_all(&dir).expect("create temp dir");
        dir
    }

    #[test]
    fn emit_spec_never_clobbers_an_existing_file() {
        let dir = temp_dir("emit");
        let path = dir.join("spec.yaml");

        write_emitted_spec(&path, "version: 1\n").expect("first write");
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "version: 1\n");

        let err = write_emitted_spec(&path, "version: 2\n")
            .expect_err("second write must fail")
            .to_string();
        assert!(err.contains("already exists"), "unexpected error: {}", err);
        assert_eq!(
            std::fs::read_to_string(&path).unwrap(),
            "version: 1\n",
            "existing spec must survive"
        );

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn schema_file_becomes_a_compilable_spec() {
        let dir = temp_dir("schema");
        let path = dir.join("schema.sql");
        std::fs::write(
            &path,
            "CREATE TABLE db.a (id BIGINT NOT NULL, vin CHAR(17)) ENGINE=OLAP;\n\
             CREATE TABLE db.b (x INT) ENGINE=OLAP;",
        )
        .expect("write schema");

        // Two tables, so an explicit choice is required.
        assert!(load_schema_table(&path, None).is_err());

        let table = load_schema_table(&path, Some("a")).expect("select table a");
        let yaml = derive::spec_yaml_from_table(&table).expect("derive spec");
        let spec = compile_yaml(&yaml).expect("derived spec compiles");
        assert_eq!(spec.fields.len(), 2);

        // A spec covering every column passes validation; a short one fails.
        validate_spec_covers(&spec, &table.columns).expect("derived spec covers the table");
        let partial = compile_yaml("version: 1\nfields:\n  - name: id\n    gen:\n      type: uuid\n")
            .expect("compiles");
        assert!(validate_spec_covers(&partial, &table.columns).is_err());

        assert!(load_schema_table(&path, Some("nope")).is_err(), "unknown table must fail");

        std::fs::remove_dir_all(&dir).ok();
    }

    /// Every Doris column type, from DDL through a derived spec and generated
    /// rows into a real Parquet file, read back and checked against the
    /// mapping and against Doris's own limits.
    #[test]
    fn every_doris_type_round_trips_through_parquet() {
        use arrow::array::{Array, AsArray};
        use arrow::datatypes::{Int32Type, Int8Type, TimestampMicrosecondType};
        use parquet::basic::{LogicalType, Type as Physical};

        let sql = "CREATE TABLE all_types (
            c_bool BOOLEAN NOT NULL, c_tiny TINYINT, c_small SMALLINT, c_int INT, c_big BIGINT,
            c_large LARGEINT, c_float FLOAT, c_double DOUBLE,
            c_dec9 DECIMAL(9,2), c_dec18 DECIMAL(18,4), c_dec38 DECIMAL(38,10), c_dec76 DECIMAL(76,20),
            c_date DATE, c_dt0 DATETIME, c_dt3 DATETIME(3), c_dt6 DATETIME(6),
            c_char CHAR(17), c_varchar VARCHAR(64), c_string STRING,
            c_json JSON, c_variant VARIANT, c_ipv4 IPV4, c_ipv6 IPV6,
            c_array ARRAY<INT>, c_nested ARRAY<ARRAY<VARCHAR(20)>>,
            c_map MAP<VARCHAR(16), DECIMAL(10,2)>,
            c_struct STRUCT<city:VARCHAR(80), zip:INT, at:DATETIME(3), tags:ARRAY<STRING>>,
            c_bitmap BITMAP, c_hll HLL, c_qs QUANTILE_STATE
        ) ENGINE=OLAP";
        let table = schema::select_table(schema::parse_schema(sql).unwrap(), None).unwrap();
        let yaml = derive::spec_yaml_from_table(&table).unwrap();
        let spec = compile_yaml(&yaml).expect("derived spec compiles");

        let mut rng = StdRng::seed_from_u64(7);
        let mut fields = (*spec.fields).clone();
        let mut builder = parquet_out::BatchBuilder::new(table.columns.clone(), 256).unwrap();
        const ROWS: usize = 3000;
        for _ in 0..ROWS {
            let mut ctx = RowContext::new();
            for &index in spec.generation_order.iter() {
                let field = &mut fields[index];
                let value = field.generator.generate(&ctx, &mut rng).unwrap();
                ctx.insert(field.name.clone(), value);
            }
            builder.append_row(|name| ctx.get(name)).expect("row converts");
        }
        let batch = builder.finish().expect("batch builds");

        let dir = temp_dir("alltypes");
        let path = dir.join("all.parquet");
        let mut writer = parquet::arrow::ArrowWriter::try_new(
            std::fs::File::create(&path).unwrap(),
            batch.schema(),
            None,
        )
        .unwrap();
        writer.write(&batch).unwrap();
        writer.close().unwrap();

        let reader = parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder::try_new(
            std::fs::File::open(&path).unwrap(),
        )
        .unwrap();

        // --- the Parquet types, leaf by leaf ---
        let descr = reader.metadata().file_metadata().schema_descr_ptr();
        let leaf = |path: &str| {
            descr
                .columns()
                .iter()
                .find(|column| column.path().string() == path)
                .unwrap_or_else(|| {
                    let all: Vec<String> = descr.columns().iter().map(|c| c.path().string()).collect();
                    panic!("no Parquet leaf `{}`; have {:?}", path, all)
                })
                .clone()
        };
        let integer = |bits| Some(LogicalType::integer(bits, true));
        // The borrowing accessor; logical_type() clones and is deprecated.
        let logical = |path: &str| leaf(path).logical_type_ref().cloned();
        assert_eq!(leaf("c_tiny").physical_type(), Physical::INT32);
        assert_eq!(logical("c_tiny"), integer(8));
        assert_eq!(logical("c_small"), integer(16));
        assert_eq!(leaf("c_big").physical_type(), Physical::INT64);
        assert_eq!(leaf("c_large").physical_type(), Physical::BYTE_ARRAY);
        assert_eq!(logical("c_large"), Some(LogicalType::String));
        assert_eq!(leaf("c_float").physical_type(), Physical::FLOAT);
        assert_eq!(leaf("c_dec9").physical_type(), Physical::INT32);
        assert_eq!(leaf("c_dec18").physical_type(), Physical::INT64);
        assert_eq!(leaf("c_dec38").physical_type(), Physical::FIXED_LEN_BYTE_ARRAY);
        assert_eq!(leaf("c_dec76").physical_type(), Physical::FIXED_LEN_BYTE_ARRAY);
        assert_eq!(leaf("c_dec76").type_length(), 32);
        assert_eq!(
            logical("c_dec76"), Some(LogicalType::decimal(20, 76))
        );
        assert_eq!(logical("c_date"), Some(LogicalType::Date));
        for column in ["c_dt0", "c_dt3", "c_dt6"] {
            assert_eq!(leaf(column).physical_type(), Physical::INT64);
            assert_eq!(
                logical(column), Some(LogicalType::timestamp(false, parquet::basic::TimeUnit::MICROS)),
                "{} must be a zone-less microsecond timestamp",
                column
            );
        }
        for column in ["c_char", "c_json", "c_variant", "c_ipv4", "c_ipv6", "c_hll"] {
            assert_eq!(logical(column), Some(LogicalType::String), "{}", column);
        }
        assert_eq!(leaf("c_bitmap").physical_type(), Physical::INT64);
        assert_eq!(leaf("c_qs").physical_type(), Physical::DOUBLE);
        // Nested shapes use the Parquet spec's own group names.
        leaf("c_array.list.element");
        leaf("c_nested.list.element.list.element");
        leaf("c_map.key_value.key");
        leaf("c_map.key_value.value");
        leaf("c_struct.city");
        leaf("c_struct.tags.list.element");

        // --- the values, against Doris's limits ---
        let read: Vec<arrow::record_batch::RecordBatch> =
            reader.build().unwrap().map(|batch| batch.unwrap()).collect();
        assert_eq!(read.iter().map(|b| b.num_rows()).sum::<usize>(), ROWS);
        let batch = arrow::compute::concat_batches(&read[0].schema(), &read).unwrap();
        let column = |name: &str| batch.column_by_name(name).unwrap().clone();

        let large = column("c_large");
        let mut beyond_bigint = 0;
        for value in large.as_string::<i32>().iter().flatten() {
            let number: i128 = value.parse().expect("LARGEINT text is an integer");
            assert!(number > -i128::MAX, "{}", value);
            if number > i64::MAX as i128 {
                beyond_bigint += 1;
            }
        }
        assert!(beyond_bigint > 0, "LARGEINT never exceeded BIGINT's range");

        for (name, unit) in [("c_dt0", 1_000_000i64), ("c_dt3", 1_000)] {
            let values = column(name);
            for micros in values.as_primitive::<TimestampMicrosecondType>().iter().flatten() {
                assert_eq!(micros.rem_euclid(unit), 0, "{} carries digits its scale drops", name);
            }
        }

        for value in column("c_varchar").as_string::<i32>().iter().flatten() {
            assert!(value.len() <= 64, "VARCHAR(64) got {} bytes", value.len());
        }
        for value in column("c_char").as_string::<i32>().iter().flatten() {
            assert!(value.len() <= 17);
        }
        for name in ["c_json", "c_variant"] {
            for value in column(name).as_string::<i32>().iter().flatten() {
                let parsed: serde_json::Value = serde_json::from_str(value).expect("valid JSON");
                assert!(parsed.get("id").is_some(), "{}", value);
            }
        }
        for value in column("c_ipv4").as_string::<i32>().iter().flatten() {
            let address: std::net::Ipv4Addr = value.parse().unwrap();
            assert_eq!(address.octets()[0], 10);
        }
        for value in column("c_ipv6").as_string::<i32>().iter().flatten() {
            value.parse::<std::net::Ipv6Addr>().unwrap();
        }
        let bitmap = column("c_bitmap");
        assert!(bitmap
            .as_primitive::<arrow::datatypes::Int64Type>()
            .iter()
            .flatten()
            .all(|member| member >= 0));
        assert!(column("c_tiny").as_primitive::<Int8Type>().iter().flatten().all(|v| v >= 0));

        let arrays = column("c_array");
        let lists = arrays.as_list::<i32>();
        assert!(lists.iter().flatten().any(|items| !items.is_empty()), "arrays are all empty");
        assert!(lists.iter().flatten().all(|items| items.len() <= 3));
        assert!(lists.value(0).as_primitive::<Int32Type>().len() <= 3);

        let maps = column("c_map");
        let maps = maps.as_map();
        for index in 0..maps.len() {
            let keys = maps.value(index).column(0).as_string::<i32>().clone();
            let unique: std::collections::HashSet<_> = keys.iter().collect();
            assert_eq!(unique.len(), keys.len(), "map {} repeats a key", index);
        }

        let structs = column("c_struct");
        let structs = structs.as_struct();
        assert_eq!(structs.num_columns(), 4);
        assert!(structs.column_by_name("tags").is_some());

        std::fs::remove_dir_all(&dir).ok();
    }

    /// Validate a one-field spec against a one-column table.
    fn check_fit(column_ddl: &str, generator_yaml: &str) -> Result<()> {
        let sql = format!("CREATE TABLE t (c {}) ENGINE=OLAP", column_ddl);
        let table = schema::select_table(schema::parse_schema(&sql)?, None)?;
        let indented: String = generator_yaml
            .lines()
            .map(|line| format!("      {}\n", line))
            .collect();
        let spec = compile_yaml(&format!("version: 1\nfields:\n  - name: c\n    gen:\n{}", indented))?;
        probe::validate_against_schema(&spec, &table.columns)
    }

    fn expect_misfit(column_ddl: &str, generator_yaml: &str, needle: &str) {
        let error = check_fit(column_ddl, generator_yaml)
            .expect_err(&format!("{} should reject:\n{}", column_ddl, generator_yaml));
        let text = format!("{:#}", error);
        assert!(text.contains(needle), "expected `{}` in:\n{}", needle, text);
        assert!(text.contains("`c`"), "error must name the column:\n{}", text);
    }

    #[test]
    fn startup_validation_catches_each_kind_of_misfit() {
        expect_misfit("TINYINT", "type: int_range\nmin: 0\nmax: 1000", "TINYINT's range");
        expect_misfit("VARCHAR(20)", "type: lorem\nwords_min: 1\nwords_max: 12", "bytes");
        expect_misfit("INT NOT NULL", "type: int_range\nmin: 1\nmax: 9\nnull_rate: 0.1", "NOT NULL");
        expect_misfit("BOOLEAN", "type: choice\nvalues: [\"true\", maybe]", "not a boolean");
        expect_misfit(
            "DECIMAL(10,2)",
            "type: decimal_range\nmin: \"1.0000\"\nmax: \"9.9999\"\nscale: 4",
            "decimal places",
        );
        // The case a per-row check only finds an hour in: a slow drift
        // towards a bound the column cannot hold.
        expect_misfit(
            "SMALLINT",
            "type: fluctuating\ndata_type: int\nstart: 100\nmin: 0\nmax: 100000\ninitial_direction: up\nstep_min: 1\nstep_max: 2\nflip_chance: 0.0",
            "SMALLINT's range",
        );
        expect_misfit(
            "ARRAY<TINYINT>",
            "type: array\nelement:\n  type: int_range\n  min: 0\n  max: 999",
            "TINYINT's range",
        );
        expect_misfit(
            "STRUCT<city:VARCHAR(20)>",
            "type: struct\nfields:\n  - name: town\n    gen:\n      type: constant\n      value: x",
            "no field `town`",
        );
        expect_misfit("IPV6", "type: ipv4", "not an IPv6 address");
        expect_misfit("JSON", "type: constant\nvalue: not json", "not valid JSON");
    }

    #[test]
    fn startup_validation_passes_specs_that_fit() {
        check_fit("TINYINT", "type: int_range\nmin: -128\nmax: 127").unwrap();
        check_fit("VARCHAR(95)", "type: lorem\nwords_min: 1\nwords_max: 12").unwrap();
        check_fit("DECIMAL(10,4)", "type: decimal_range\nmin: \"0.01\"\nmax: \"99.99\"\nscale: 2").unwrap();
        check_fit(
            "LARGEINT",
            "type: int_range\nmin: \"-170141183460469231731687303715884105727\"\nmax: \"170141183460469231731687303715884105727\"",
        )
        .unwrap();
        check_fit("MAP<VARCHAR(8), INT>", "type: map\nkey:\n  type: lorem\n  words_min: 1\n  words_max: 1\nvalue:\n  type: int_range\n  min: 1\n  max: 9").unwrap();
        // The very last second Doris's DATETIME can hold still fits.
        check_fit(
            "DATETIME",
            "type: datetime_range\nstart: \"9999-12-31T00:00:00Z\"\nend: \"9999-12-31T23:59:59Z\"",
        )
        .unwrap();
    }

    #[test]
    fn largeint_bounds_past_i128_are_rejected_when_compiling() {
        let error = check_fit(
            "LARGEINT",
            "type: int_range\nmin: 0\nmax: \"999999999999999999999999999999999999999999\"",
        )
        .unwrap_err();
        assert!(format!("{:#}", error).contains("not an integer"), "{:#}", error);
    }

    #[test]
    fn the_derived_all_types_spec_passes_validation() {
        let sql = "CREATE TABLE t (
            a BOOLEAN NOT NULL, b LARGEINT, c DECIMAL(76,20), d DATETIME(3), e VARCHAR(64),
            f JSON, g IPV6, h ARRAY<ARRAY<VARCHAR(20)>>, i MAP<VARCHAR(16), DECIMAL(10,2)>,
            j STRUCT<city:VARCHAR(80), zip:INT, tags:ARRAY<STRING>>, k BITMAP, l HLL,
            m QUANTILE_STATE, n CHAR(1), o VARCHAR(7)
        ) ENGINE=OLAP";
        let table = schema::select_table(schema::parse_schema(sql).unwrap(), None).unwrap();
        let spec = compile_yaml(&derive::spec_yaml_from_table(&table).unwrap()).unwrap();
        probe::validate_against_schema(&spec, &table.columns).expect("derived spec fits its own schema");
    }

    /// Generate `rows` rows from a compiled spec with a fixed seed.
    fn generate_rows(spec: &CompiledSpec, rows: usize) -> Vec<RowContext> {
        let mut rng = StdRng::seed_from_u64(19);
        let mut fields = (*spec.fields).clone();
        let mut out = Vec::with_capacity(rows);
        for _ in 0..rows {
            let mut ctx = RowContext::new();
            for &index in spec.generation_order.iter() {
                let field = &mut fields[index];
                let value = field
                    .generator
                    .generate(&ctx, &mut rng)
                    .unwrap_or_else(|error| panic!("field `{}`: {:#}", field.name, error));
                ctx.insert(field.name.clone(), value);
            }
            out.push(ctx);
        }
        out
    }

    fn example_table() -> schema::Table {
        let sql = std::fs::read_to_string("examples/all_types.sql").expect("examples/all_types.sql");
        schema::select_table(schema::parse_schema(&sql).expect("schema parses"), None).unwrap()
    }

    fn example_spec() -> CompiledSpec {
        // `file:` paths in a spec resolve against the working directory,
        // which for a test is the crate root, same as the documented usage.
        let yaml = std::fs::read_to_string("examples/all_types.spec").expect("examples/all_types.spec");
        compile_yaml(&yaml).expect("the shipped example spec compiles")
    }

    /// Every generator must appear in the shipped example. A new generator
    /// without one fails here, which is the point.
    #[test]
    fn the_example_spec_demonstrates_every_generator() {
        const GENERATORS: [&str; 24] = [
            "constant", "sequence", "sequence_string", "name", "email", "lorem", "address",
            "template", "int_range", "float_range", "decimal_range", "fluctuating",
            "datetime_around", "datetime_range", "choice", "weighted_choice", "uuid",
            "random_bytes", "javascript", "array", "map", "struct", "ipv4", "ipv6",
        ];
        let yaml = std::fs::read_to_string("examples/all_types.spec").unwrap();
        for generator in GENERATORS {
            assert!(
                yaml.contains(&format!("type: {}\n", generator)),
                "examples/all_types.spec never uses `{}`",
                generator
            );
        }
        // And the other spec modes worth showing.
        for feature in ["hidden: true", "null_rate:", "order:", "deps:", "parallel: true", "reset: row"] {
            assert!(yaml.contains(feature), "example never shows `{}`", feature);
        }
    }

    #[test]
    fn the_example_spec_fits_the_example_schema_and_writes_parquet() {
        let table = example_table();
        let spec = example_spec();
        validate_spec_covers(&spec, &table.columns).expect("example covers every column");
        probe::validate_against_schema(&spec, &table.columns).expect("example fits the schema");

        const ROWS: usize = 4000;
        let rows = generate_rows(&spec, ROWS);
        let mut builder = parquet_out::BatchBuilder::new(table.columns.clone(), 512).unwrap();
        for ctx in &rows {
            builder.append_row(|name| ctx.get(name)).expect("example row converts");
        }
        let batch = builder.finish().expect("example batch builds");
        assert_eq!(batch.num_rows(), ROWS);
        assert_eq!(batch.num_columns(), table.columns.len());

        // Hidden helpers must not reach the file.
        assert!(batch.schema().column_with_name("h_sku").is_none());
    }

    /// The corner cases the example is built to reach, asserted rather than
    /// assumed: empty containers, nulls, key collisions, both JavaScript
    /// branches, and the counter's fixed width.
    #[test]
    fn the_example_reaches_its_corner_cases() {
        let rows = generate_rows(&example_spec(), 4000);
        let values = |name: &str| -> Vec<&Value> {
            rows.iter().map(|ctx| ctx.get(name).expect("field present")).collect()
        };

        let mut empty_arrays = 0;
        let mut full_arrays = 0;
        for value in values("c_array") {
            let Value::List(items) = value else { panic!("c_array is not a list") };
            assert!(items.len() <= 4, "max_len is 4");
            if items.is_empty() {
                empty_arrays += 1;
            }
            if items.len() == 4 {
                full_arrays += 1;
            }
        }
        assert!(empty_arrays > 0 && full_arrays > 0, "arrays never hit both bounds");

        // A nested array whose inner array is empty is the awkward shape.
        assert!(
            values("c_nested").into_iter().any(|value| matches!(value, Value::List(outer)
                if outer.iter().any(|inner| matches!(inner, Value::List(items) if items.is_empty())))),
            "no empty inner array"
        );

        assert!(
            values("c_string").into_iter().filter(|value| matches!(value, Value::Null)).count() > 0,
            "null_rate never produced a null"
        );

        // Keys come from a 20-word list, so a 4-entry map must sometimes
        // collide and come back shorter. Every map must still be unique.
        let mut shortened = 0;
        for value in values("c_map") {
            let Value::Map(entries) = value else { panic!("c_map is not a map") };
            let unique: HashSet<String> =
                entries.iter().map(|(key, _)| key.csv_string("")).collect();
            assert_eq!(unique.len(), entries.len(), "duplicate map key");
            if entries.len() < 4 {
                shortened += 1;
            }
        }
        assert!(shortened > 0, "map keys never collided");

        // Both JavaScript functions ran, and the dependent branches were hit.
        let tiers: HashSet<String> = values("h_tier").into_iter().map(|value| value.csv_string("")).collect();
        assert!(tiers.len() > 1, "tier_label never branched: {:?}", tiers);
        assert!(tiers.contains("platinum"), "the high branch never ran: {:?}", tiers);

        let mut seen_rows = HashSet::new();
        for value in values("c_variant") {
            let doc: serde_json::Value =
                serde_json::from_str(&value.csv_string("")).expect("variant_doc emits JSON");
            // JavaScript globals persist across rows, so the counter advances.
            assert!(seen_rows.insert(doc["row"].as_u64().unwrap()), "row counter repeated");
            // A LARGEINT keeps every digit: small values arrive as numbers,
            // and anything past 2^53 as text, which JavaScript cannot round.
            let large = &doc["largeint"];
            let digits = large.as_str().map(str::to_string).unwrap_or_else(|| large.to_string());
            let number: u128 = digits.trim_matches('"').parse().expect("largeint is an integer");
            assert!(number <= u64::MAX as u128);
            if number > (1u128 << 53) {
                assert!(large.is_string(), "{} needs text to stay exact", number);
            }
        }
        assert_eq!(seen_rows.len(), 4000);

        // sequence_string pads to a fixed width, so ids sort lexically.
        let skus: Vec<String> = values("h_sku").into_iter().map(|value| value.csv_string("")).collect();
        assert!(skus.iter().all(|sku| sku.len() == 14), "width is not fixed: {:?}", &skus[..2]);
        let mut sorted = skus.clone();
        sorted.sort();
        assert_eq!(skus, sorted, "zero padding must keep ids in order");

        // The template pulled in every hidden field it references.
        let sample = rows[0].get("c_varchar").unwrap().csv_string("");
        assert!(sample.contains(&skus[0]) && sample.contains('@') && sample.contains("WEB"), "{}", sample);
    }


}
