mod config;
mod geohash;
mod sim;
mod stations;

use anyhow::{Context, Result};
use arrow::array::{ArrayRef, Float64Array, Int32Array, Int64Array, StringBuilder};
use arrow::datatypes::{DataType, Field, Schema};
use arrow::record_batch::RecordBatch;
use clap::{Parser, Subcommand};
use config::Config;
use parquet::arrow::ArrowWriter;
use parquet::basic::{Compression, ZstdLevel};
use parquet::file::properties::WriterProperties;
use rayon::prelude::*;
use serde::Serialize;
use sim::{Fleet, Ping};
use std::fs::File;
use std::io::IsTerminal;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Instant;

/// Columns written to every parquet file, in order. Broker Load maps parquet columns by name.
pub const COLUMNS: [&str; 11] = [
    "offset_ms", "taxi_id", "plate", "company", "geohash6", "geohash8", "lat", "lon", "status", "speed_kmh", "heading",
];

#[derive(Parser, Debug)]
#[command(
    name = "taxi-gen",
    version,
    about = "Generate a relative-time taxi ping history as Parquet for the Doris hyper-search demo",
    long_about = "Timestamps are stored as offset_ms <= 0 (milliseconds before load time), so the same files \
                  can be loaded again and again: Broker Load computes ts = milliseconds_add('<now>', offset_ms)."
)]
struct Args {
    /// TOML config file (all keys optional). CLI flags override it.
    #[arg(long, default_value = "generator.toml")]
    config: PathBuf,
    #[arg(long)]
    rows: Option<u64>,
    #[arg(long)]
    days: Option<u32>,
    #[arg(long)]
    taxis: Option<u32>,
    #[arg(long)]
    ping_secs: Option<u32>,
    #[arg(long)]
    seed: Option<u64>,
    #[arg(long)]
    out: Option<PathBuf>,
    #[arg(long)]
    threads: Option<usize>,
    #[arg(long)]
    zstd_level: Option<i32>,
    #[command(subcommand)]
    cmd: Option<Cmd>,
}

#[derive(Subcommand, Debug)]
enum Cmd {
    /// Generate parquet files (default). Existing files are skipped unless --force.
    Generate {
        /// First day to generate (inclusive, 0 = most recent).
        #[arg(long, default_value_t = 0)]
        from_day: u32,
        /// Last day to generate (exclusive). Default: all days.
        #[arg(long)]
        to_day: Option<u32>,
        /// Regenerate files that already exist.
        #[arg(long)]
        force: bool,
        /// Print one line per finished file in addition to the progress bar.
        #[arg(long, short)]
        verbose: bool,
    },
    /// Dry run: print what would be generated (rows/day, shifts, chunking, memory, size estimate).
    Plan,
    /// Print the effective configuration as TOML.
    PrintConfig,
    /// Print Doris DDL + BROKER LOAD template matching the parquet schema.
    Sql {
        /// s3.yaml with Endpoint/access_key/secret_key/Bucket/Prefix.
        #[arg(long, default_value = "../s3.yaml")]
        s3_yaml: PathBuf,
        /// Load-time base timestamp literal (UTC). Placeholder if omitted.
        #[arg(long)]
        base: Option<String>,
        #[arg(long, default_value = "demo_hyper_search")]
        database: String,
        #[arg(long, default_value = "taxi_ping")]
        table: String,
    },
}

#[derive(Serialize, Clone)]
struct FileInfo {
    file: String,
    day: u32,
    part: u32,
    rows: u64,
    shifts: u32,
    window_start_ms: i64,
    window_end_ms: i64,
    min_offset_ms: i64,
    max_offset_ms: i64,
    bytes: u64,
    skipped_existing: bool,
}

#[derive(Serialize)]
struct Manifest {
    generator: &'static str,
    config: Config,
    columns: Vec<&'static str>,
    ts_expression: &'static str,
    total_rows: u64,
    total_bytes: u64,
    total_files: usize,
    files: Vec<FileInfo>,
}

#[derive(Serialize)]
struct State {
    /// offset_ms of "now" at load time; the realtime feeder starts here.
    base_offset_ms: i64,
    ping_secs: u32,
    taxis: u32,
    active_count: usize,
    active: Vec<sim::ActiveTaxi>,
}

fn load_config(args: &Args) -> Result<Config> {
    let mut cfg = if args.config.exists() {
        let text = std::fs::read_to_string(&args.config)
            .with_context(|| format!("reading {}", args.config.display()))?;
        toml::from_str::<Config>(&text).with_context(|| format!("parsing {}", args.config.display()))?
    } else {
        Config::default()
    };
    if let Some(v) = args.rows { cfg.rows = v; }
    if let Some(v) = args.days { cfg.days = v; }
    if let Some(v) = args.taxis { cfg.taxis = v; }
    if let Some(v) = args.ping_secs { cfg.ping_secs = v; }
    if let Some(v) = args.seed { cfg.seed = v; }
    if let Some(v) = &args.out { cfg.out = v.clone(); }
    if let Some(v) = args.threads { cfg.threads = v; }
    if let Some(v) = args.zstd_level { cfg.zstd_level = v; }
    cfg.validate()?;
    Ok(cfg)
}

fn main() -> Result<()> {
    let args = Args::parse();
    let cfg = load_config(&args)?;
    match args.cmd.unwrap_or(Cmd::Generate { from_day: 0, to_day: None, force: false, verbose: false }) {
        Cmd::Generate { from_day, to_day, force, verbose } => {
            generate(&cfg, from_day, to_day.unwrap_or(cfg.days), force, verbose)
        }
        Cmd::Plan => { print_plan(&cfg); Ok(()) }
        Cmd::PrintConfig => { print!("{}", toml::to_string_pretty(&cfg)?); Ok(()) }
        Cmd::Sql { s3_yaml, base, database, table } => print_sql(&cfg, &s3_yaml, base, &database, &table),
    }
}

const BYTES_PER_ROW_PARQUET: f64 = 24.0; // estimate: zstd-3 + dictionary encoding (geohash8 is high-cardinality)
const BYTES_PER_ROW_MEMORY: f64 = 60.0; // Ping struct + arrow builders while writing

fn threads(cfg: &Config) -> usize {
    if cfg.threads > 0 { cfg.threads } else { std::thread::available_parallelism().map(|n| n.get()).unwrap_or(1) }
}

fn print_plan(cfg: &Config) {
    let tasks = sim::tasks(cfg);
    let rows_per_day = cfg.rows as f64 / cfg.days as f64;
    let shift_avg_min = (cfg.shift_min_minutes + cfg.shift_max_minutes) as f64 / 2.0;
    let pings_per_shift = shift_avg_min * 60.0 / cfg.ping_secs as f64;
    let shifts_per_day = rows_per_day / pings_per_shift;
    let concurrent = shifts_per_day * shift_avg_min / 1440.0;
    let shifts_per_taxi = shifts_per_day * cfg.days as f64 / cfg.taxis as f64;
    let parts_day0 = tasks[0].parts;
    let nthreads = threads(cfg);
    println!("taxi-gen plan");
    println!("  rows total          : {}", cfg.rows);
    println!("  days                : {}  (offset range [-{} d, 0))", cfg.days, cfg.days);
    println!("  taxis / plates      : {}  ({}A0000? .. )", cfg.taxis, cfg.plate_prefix);
    println!("  ping interval       : {} s +/- {} s", cfg.ping_secs, cfg.ping_jitter_secs);
    println!("  shift length        : {}..{} min", cfg.shift_min_minutes, cfg.shift_max_minutes);
    println!("  rows / day          : {:.0}", rows_per_day);
    println!("  pings / shift (avg) : {:.0}", pings_per_shift);
    println!("  shifts / day        : {:.0}", shifts_per_day);
    println!("  taxis online at once: ~{:.0}  (=> ~{:.1} pings/s citywide)", concurrent, rows_per_day / 86400.0);
    println!("  shifts / taxi total : ~{:.1}  (=> ~{:.0} rows per plate)", shifts_per_taxi, cfg.rows as f64 / cfg.taxis as f64);
    println!("  chunking            : {} tasks = {} days x {} parts/day, ~{} rows/file ({:.0} MB)",
        tasks.len(), cfg.days, parts_day0, tasks[0].rows_est, tasks[0].rows_est as f64 * BYTES_PER_ROW_PARQUET / 1e6);
    println!("  window / task       : {:.1} h", (tasks[0].window_end_ms - tasks[0].window_start_ms) as f64 / 3.6e6);
    println!("  threads             : {}  (peak memory ~{:.1} GB)", nthreads,
        nthreads as f64 * cfg.rows_per_file as f64 * BYTES_PER_ROW_MEMORY / 1e9);
    println!("  files               : day_DDD_PP.parquet x {} + state.json + manifest.json in {}", tasks.len(), cfg.out.display());
    println!("  size estimate       : ~{:.1} GB parquet (zstd-{})", cfg.rows as f64 * BYTES_PER_ROW_PARQUET / 1e9, cfg.zstd_level);
    println!("  columns             : {}", COLUMNS.join(", "));
    println!("  seed                : {}", cfg.seed);
    println!("  first tasks         :");
    for t in tasks.iter().take(3) {
        println!("    #{:<5} {}  window [{:>14}, {:>14}) ms  ~{} rows", t.index, t.file_name(), t.window_start_ms, t.window_end_ms, t.rows_est);
    }
    if tasks.len() > 3 {
        let t = tasks.last().unwrap();
        println!("    ...");
        println!("    #{:<5} {}  window [{:>14}, {:>14}) ms  ~{} rows", t.index, t.file_name(), t.window_start_ms, t.window_end_ms, t.rows_est);
    }
}

/// Shared counters for the progress reporter.
#[derive(Default)]
struct Progress {
    rows_generated: AtomicU64,
    rows_written: AtomicU64,
    bytes_written: AtomicU64,
    tasks_done: AtomicU64,
    tasks_skipped: AtomicU64,
}

fn fmt_eta(secs: f64) -> String {
    if !secs.is_finite() || secs < 0.0 {
        return "--:--".into();
    }
    let s = secs.round() as u64;
    if s >= 3600 { format!("{}h{:02}m", s / 3600, (s % 3600) / 60) } else { format!("{:02}:{:02}", s / 60, s % 60) }
}

fn progress_line(p: &Progress, total_tasks: u64, total_rows: u64, started: Instant) -> String {
    let done = p.tasks_done.load(Ordering::Relaxed);
    let skipped = p.tasks_skipped.load(Ordering::Relaxed);
    let gen = p.rows_generated.load(Ordering::Relaxed);
    let written = p.rows_written.load(Ordering::Relaxed);
    let bytes = p.bytes_written.load(Ordering::Relaxed);
    let el = started.elapsed().as_secs_f64().max(1e-3);
    let rate = written as f64 / el;
    let eta = if written > 0 { (total_rows.saturating_sub(written)) as f64 / rate } else { f64::INFINITY };
    format!(
        "files {done}/{total_tasks}{}  rows {:.1}M/{:.0}M ({:.1}%)  gen {:.1}M  {:.2} GB  {:.2} M rows/s  elapsed {}  eta {}",
        if skipped > 0 { format!(" (+{skipped} skipped)") } else { String::new() },
        written as f64 / 1e6,
        total_rows as f64 / 1e6,
        100.0 * written as f64 / total_rows.max(1) as f64,
        gen as f64 / 1e6,
        bytes as f64 / 1e9,
        rate / 1e6,
        fmt_eta(el),
        fmt_eta(eta),
    )
}

fn generate(cfg: &Config, from_day: u32, to_day: u32, force: bool, verbose: bool) -> Result<()> {
    anyhow::ensure!(from_day < to_day && to_day <= cfg.days, "day range must satisfy from < to <= days");
    if cfg.threads > 0 {
        rayon::ThreadPoolBuilder::new().num_threads(cfg.threads).build_global()?;
    }
    std::fs::create_dir_all(&cfg.out)?;
    print_plan(cfg);

    let all_tasks = sim::tasks(cfg);
    let tasks: Vec<sim::Task> = all_tasks.iter().filter(|t| t.day >= from_day && t.day < to_day).cloned().collect();
    let total_rows: u64 = (from_day..to_day).map(|d| cfg.rows_for_day(d)).sum();
    let total_tasks = tasks.len() as u64;
    println!(
        "generating days {from_day}..{to_day}: {} files, {} rows, {} threads{}",
        total_tasks, total_rows, rayon::current_num_threads(),
        if force { " (force: overwriting existing files)" } else { " (existing files are skipped)" }
    );

    let fleet = Arc::new(Fleet::new(cfg));
    let schema = Arc::new(parquet_schema());
    let progress = Arc::new(Progress::default());
    let started = Instant::now();

    // Progress reporter: single updating line on a TTY, one line every 10 s otherwise.
    let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let reporter = {
        let (p, stop, tty) = (progress.clone(), stop.clone(), std::io::stderr().is_terminal());
        std::thread::spawn(move || {
            let mut tick = 0u64;
            while !stop.load(Ordering::Relaxed) {
                std::thread::sleep(std::time::Duration::from_millis(500));
                tick += 1;
                if tty {
                    eprint!("\r\x1b[2K{}", progress_line(&p, total_tasks, total_rows, started));
                } else if tick % 20 == 0 {
                    eprintln!("{}", progress_line(&p, total_tasks, total_rows, started));
                }
            }
            if tty { eprint!("\r\x1b[2K"); }
        })
    };

    let result: Result<Vec<FileInfo>> = tasks
        .par_iter()
        .map(|task| -> Result<FileInfo> {
            let path = cfg.out.join(task.file_name());
            if !force && path.exists() {
                let rows = parquet_row_count(&path)?;
                progress.rows_written.fetch_add(rows, Ordering::Relaxed);
                progress.rows_generated.fetch_add(rows, Ordering::Relaxed);
                progress.bytes_written.fetch_add(std::fs::metadata(&path)?.len(), Ordering::Relaxed);
                progress.tasks_skipped.fetch_add(1, Ordering::Relaxed);
                return Ok(FileInfo {
                    file: task.file_name(), day: task.day, part: task.part, rows, shifts: 0,
                    window_start_ms: task.window_start_ms, window_end_ms: task.window_end_ms,
                    min_offset_ms: 0, max_offset_ms: 0, bytes: std::fs::metadata(&path)?.len(),
                    skipped_existing: true,
                });
            }
            let t0 = Instant::now();
            let schedule = sim::day_schedule(task.day, cfg.rows_for_day(task.day), cfg);
            let res = sim::generate_task(task, &schedule, cfg, &fleet, &progress.rows_generated);
            let tmp = cfg.out.join(format!("{}.tmp", task.file_name()));
            let bytes = write_parquet(&tmp, &schema, &res.pings, &fleet, cfg, &progress)?;
            std::fs::rename(&tmp, &path)?;
            if !res.active.is_empty() {
                // Only day-0 tasks produce active shifts; merge them into state.json under a lock-free
                // per-task file, combined after the run.
                let f = File::create(cfg.out.join(format!(".active_{}.json", task.file_name())))?;
                serde_json::to_writer(f, &res.active)?;
            }
            progress.tasks_done.fetch_add(1, Ordering::Relaxed);
            if verbose {
                eprintln!(
                    "\r\x1b[2K{} rows={} shifts={} {:.1} MB in {:.1}s",
                    task.file_name(), res.pings.len(), res.shifts, bytes as f64 / 1e6, t0.elapsed().as_secs_f64()
                );
            }
            Ok(FileInfo {
                file: task.file_name(), day: task.day, part: task.part,
                rows: res.pings.len() as u64, shifts: res.shifts,
                window_start_ms: task.window_start_ms, window_end_ms: task.window_end_ms,
                min_offset_ms: res.pings.first().map(|p| p.offset_ms).unwrap_or(0),
                max_offset_ms: res.pings.last().map(|p| p.offset_ms).unwrap_or(0),
                bytes, skipped_existing: false,
            })
        })
        .collect();
    stop.store(true, Ordering::Relaxed);
    let _ = reporter.join();
    let mut files = result?;
    files.sort_by_key(|f| (f.day, f.part));

    // Merge active shifts (day 0) into state.json.
    if from_day == 0 {
        let mut active: Vec<sim::ActiveTaxi> = Vec::new();
        for t in tasks.iter().filter(|t| t.day == 0) {
            let p = cfg.out.join(format!(".active_{}.json", t.file_name()));
            if p.exists() {
                let v: Vec<sim::ActiveTaxi> = serde_json::from_reader(File::open(&p)?)?;
                active.extend(v);
                std::fs::remove_file(&p)?;
            }
        }
        let state_path = cfg.out.join("state.json");
        if !active.is_empty() || !state_path.exists() {
            active.sort_by_key(|a| a.taxi_id);
            let state = State { base_offset_ms: 0, ping_secs: cfg.ping_secs, taxis: cfg.taxis, active_count: active.len(), active };
            serde_json::to_writer_pretty(File::create(&state_path)?, &state)?;
            println!("state.json: {} shifts active at offset 0", state.active_count);
        }
    }

    let manifest = Manifest {
        generator: concat!("taxi-gen ", env!("CARGO_PKG_VERSION")),
        config: cfg.clone(),
        columns: COLUMNS.to_vec(),
        ts_expression: "ts = milliseconds_add('<BASE>', offset_ms)",
        total_rows: files.iter().map(|f| f.rows).sum(),
        total_bytes: files.iter().map(|f| f.bytes).sum(),
        total_files: files.len(),
        files,
    };
    let name = if from_day == 0 && to_day == cfg.days { "manifest.json".to_string() } else { format!("manifest_{from_day}_{to_day}.json") };
    serde_json::to_writer_pretty(File::create(cfg.out.join(&name))?, &manifest)?;
    let skipped = manifest.files.iter().filter(|f| f.skipped_existing).count();
    println!(
        "done: {} rows, {:.2} GB, {} files ({} skipped as existing) in {} -> {}",
        manifest.total_rows, manifest.total_bytes as f64 / 1e9, manifest.total_files, skipped,
        fmt_eta(started.elapsed().as_secs_f64()), cfg.out.join(name).display()
    );
    if manifest.total_rows != total_rows {
        eprintln!("WARNING: manifest rows {} != expected {}", manifest.total_rows, total_rows);
    }
    Ok(())
}

fn parquet_row_count(path: &Path) -> Result<u64> {
    let f = File::open(path).with_context(|| format!("opening {}", path.display()))?;
    let b = parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder::try_new(f)?;
    Ok(b.metadata().file_metadata().num_rows() as u64)
}

fn parquet_schema() -> Schema {
    Schema::new(vec![
        Field::new("offset_ms", DataType::Int64, false),
        Field::new("taxi_id", DataType::Int32, false),
        Field::new("plate", DataType::Utf8, false),
        Field::new("company", DataType::Utf8, false),
        Field::new("geohash6", DataType::Utf8, false),
        Field::new("geohash8", DataType::Utf8, false),
        Field::new("lat", DataType::Float64, false),
        Field::new("lon", DataType::Float64, false),
        Field::new("status", DataType::Utf8, false),
        Field::new("speed_kmh", DataType::Int32, false),
        Field::new("heading", DataType::Int32, false),
    ])
}

fn write_parquet(path: &Path, schema: &Arc<Schema>, pings: &[Ping], fleet: &Fleet, cfg: &Config, progress: &Progress) -> Result<u64> {
    let props = WriterProperties::builder()
        .set_compression(Compression::ZSTD(ZstdLevel::try_new(cfg.zstd_level)?))
        .set_max_row_group_size(cfg.row_group)
        .set_created_by(concat!("taxi-gen ", env!("CARGO_PKG_VERSION")).to_string())
        .build();
    let file = File::create(path).with_context(|| format!("creating {}", path.display()))?;
    let mut writer = ArrowWriter::try_new(file, schema.clone(), Some(props))?;
    let mut flushed_bytes = 0u64;
    for chunk in pings.chunks(cfg.row_group) {
        writer.write(&build_batch(schema, chunk, fleet)?)?;
        progress.rows_written.fetch_add(chunk.len() as u64, Ordering::Relaxed);
        let now = writer.bytes_written() as u64;
        progress.bytes_written.fetch_add(now - flushed_bytes, Ordering::Relaxed);
        flushed_bytes = now;
    }
    writer.close()?;
    let len = std::fs::metadata(path)?.len();
    progress.bytes_written.fetch_add(len.saturating_sub(flushed_bytes), Ordering::Relaxed);
    Ok(len)
}

fn build_batch(schema: &Arc<Schema>, rows: &[Ping], fleet: &Fleet) -> Result<RecordBatch> {
    let n = rows.len();
    let mut offset = Vec::with_capacity(n);
    let mut taxi = Vec::with_capacity(n);
    let mut plate = StringBuilder::with_capacity(n, n * 8);
    let mut company = StringBuilder::with_capacity(n, n * 10);
    let mut gh6 = StringBuilder::with_capacity(n, n * 6);
    let mut gh8 = StringBuilder::with_capacity(n, n * 8);
    let mut lat = Vec::with_capacity(n);
    let mut lon = Vec::with_capacity(n);
    let mut status = StringBuilder::with_capacity(n, n * 9);
    let mut speed = Vec::with_capacity(n);
    let mut heading = Vec::with_capacity(n);
    let mut buf = [0u8; 8];
    for p in rows {
        offset.push(p.offset_ms);
        taxi.push(p.taxi_id as i32);
        plate.append_value(&fleet.plates[p.taxi_id as usize]);
        company.append_value(fleet.company(p.taxi_id));
        geohash::encode(p.lat, p.lon, &mut buf);
        gh8.append_value(std::str::from_utf8(&buf)?);
        gh6.append_value(std::str::from_utf8(&buf[..6])?); // geohash6 is a prefix of geohash8
        lat.push(p.lat);
        lon.push(p.lon);
        status.append_value(p.status.as_str());
        speed.push(p.speed_kmh as i32);
        heading.push(p.heading as i32);
    }
    let cols: Vec<ArrayRef> = vec![
        Arc::new(Int64Array::from(offset)),
        Arc::new(Int32Array::from(taxi)),
        Arc::new(plate.finish()),
        Arc::new(company.finish()),
        Arc::new(gh6.finish()),
        Arc::new(gh8.finish()),
        Arc::new(Float64Array::from(lat)),
        Arc::new(Float64Array::from(lon)),
        Arc::new(status.finish()),
        Arc::new(Int32Array::from(speed)),
        Arc::new(Int32Array::from(heading)),
    ];
    Ok(RecordBatch::try_new(schema.clone(), cols)?)
}

fn read_s3_yaml(path: &Path) -> Result<std::collections::HashMap<String, String>> {
    let text = std::fs::read_to_string(path).with_context(|| format!("reading {}", path.display()))?;
    Ok(text
        .lines()
        .filter_map(|l| l.split_once(':'))
        .map(|(k, v)| (k.trim().to_ascii_lowercase(), v.trim().trim_matches(|c| c == '"' || c == '\'').to_string()))
        .collect())
}

fn print_sql(cfg: &Config, s3_yaml: &Path, base: Option<String>, database: &str, table: &str) -> Result<()> {
    let s3 = read_s3_yaml(s3_yaml).unwrap_or_default();
    let get = |k: &str, ph: &str| s3.get(k).cloned().unwrap_or_else(|| ph.to_string());
    let endpoint = get("endpoint", "<ENDPOINT>");
    let bucket = get("bucket", "<BUCKET>");
    let prefix = get("prefix", "<PREFIX>");
    let base = base.unwrap_or_else(|| "<BASE e.g. 2026-08-26 10:00:00>".to_string());
    let label = format!("{table}_{}", base.chars().filter(|c| c.is_ascii_digit()).collect::<String>());
    println!("-- taxi-gen: schema for {} rows / {} days / {} taxis", cfg.rows, cfg.days, cfg.taxis);
    println!(
        r#"CREATE DATABASE IF NOT EXISTS {database};
DROP TABLE IF EXISTS {database}.{table};
CREATE TABLE {database}.{table} (
  ts         DATETIME(3)  NOT NULL COMMENT 'ping time',
  taxi_id    INT          NOT NULL,
  geohash6   VARCHAR(6)   NOT NULL COMMENT '~1.2km x 0.6km cell; bucket key',
  geohash8   VARCHAR(8)   NOT NULL COMMENT '~38m x 19m cell; prefix/range lookups',
  lat        DOUBLE       NOT NULL,
  lon        DOUBLE       NOT NULL,
  status     VARCHAR(16)  NOT NULL COMMENT 'AVAILABLE | ON_CALL | HIRED | OFFLINE',
  speed_kmh  SMALLINT     NOT NULL,
  heading    SMALLINT     NOT NULL,
  plate      VARCHAR(12)  NOT NULL,
  company    VARCHAR(32)  NOT NULL,
  INDEX idx_geohash6 (geohash6) USING INVERTED,
  INDEX idx_geohash8 (geohash8) USING INVERTED,
  INDEX idx_taxi_id  (taxi_id)  USING INVERTED,
  INDEX idx_status   (status)   USING INVERTED,
  INDEX idx_plate    (plate)    USING INVERTED
)
DUPLICATE KEY(ts, taxi_id)
AUTO PARTITION BY RANGE (date_trunc(ts, 'day')) ()
DISTRIBUTED BY HASH(geohash6) BUCKETS 32
PROPERTIES ("replication_num" = "1");

-- One-shot bulk load. BASE is computed ONCE by the caller and used for every file,
-- so the history has no seams; the realtime feeder then continues from BASE.
LOAD LABEL {database}.{label}
(
  DATA INFILE("s3://{bucket}/{prefix}/day_*.parquet")
  INTO TABLE {table}
  FORMAT AS "parquet"
  ({cols})
  SET (ts = milliseconds_add('{base}', offset_ms))
)
WITH S3 (
  "s3.endpoint"   = "{endpoint}",
  "s3.region"     = "us-east-1",
  "s3.access_key" = "{ak}",
  "s3.secret_key" = "{sk}"
)
PROPERTIES ("timeout" = "14400", "max_filter_ratio" = "0");

-- SHOW LOAD FROM {database} WHERE LABEL = '{label}'\G"#,
        cols = COLUMNS.join(", "),
        ak = get("access_key", "<ACCESS_KEY>"),
        sk = get("secret_key", "<SECRET_KEY>"),
    );
    Ok(())
}
