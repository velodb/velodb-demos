//! Streaming Parquet output to S3 or a local directory.
//!
//! Nothing is staged on disk and no whole file is held in memory. Each writer
//! encodes one row group at a time and hands the bytes to an `object_store`
//! `BufWriter`, which uploads them as multipart parts as soon as each part
//! fills. Closing the writer completes the upload, so the object appears
//! atomically and Doris never sees a partial file.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use anyhow::{Context, Result};
use arrow::datatypes::SchemaRef;
use arrow::record_batch::RecordBatch;
use object_store::buffered::BufWriter;
use object_store::local::LocalFileSystem;
use object_store::path::Path as ObjectPath;
use object_store::ObjectStore;
use parquet::arrow::AsyncArrowWriter;
use parquet::basic::{Compression, ZstdLevel};
use parquet::file::properties::WriterProperties;

use crate::s3::OutputConfig;

/// Counters shared by every writer, sampled by the status display.
#[derive(Debug, Default)]
pub struct Stats {
    /// Rows produced by the generator threads.
    pub rows_generated: AtomicU64,
    /// Rows handed to a Parquet writer. Trails `rows_generated` by the queue.
    pub rows: AtomicU64,
    /// Rows whose bytes are actually known, i.e. encoded into a row group.
    /// Rows sitting in a half-built row group are counted by `rows` but have
    /// contributed no bytes yet, so only this figure pairs with the byte
    /// counters when deriving bytes-per-row.
    pub rows_flushed: AtomicU64,
    /// Bytes in files that have been closed and fully uploaded.
    pub bytes_written: AtomicU64,
    /// Files abandoned after their upload kept failing. Rows in them are lost.
    pub files_abandoned: AtomicU64,
    /// Rows that went into those files and so never reached storage.
    pub rows_lost: AtomicU64,
    /// Bytes written so far into files still open, across all writers.
    pub active_bytes: AtomicU64,
    pub files_completed: AtomicU64,
    pub buffered_bytes: AtomicU64,
}

impl Stats {
    pub fn add_rows(&self, count: u64) {
        self.rows.fetch_add(count, Ordering::Relaxed);
    }

    /// Everything produced so far, closed files plus files in progress.
    pub fn total_bytes(&self) -> u64 {
        self.bytes_written.load(Ordering::Relaxed) + self.active_bytes.load(Ordering::Relaxed)
    }
}

/// Where generated files land.
pub enum Destination {
    S3 { config: Box<OutputConfig> },
    Local { directory: std::path::PathBuf },
}

pub struct SinkSettings {
    /// Distinguishes one run's objects from another's in the same prefix, as
    /// a folder of its own. Without it every run starts its file numbering at
    /// one again and overwrites the last run's output. A folder per run also
    /// means one run can be loaded or deleted by prefix alone. Empty writes
    /// straight into the prefix, the older flat layout.
    pub run_id: String,
    /// Group a run's files into `batch-NNNNN/` folders of this many files, so
    /// each folder is a ready-made unit for one Doris load job. Zero disables.
    pub files_per_folder: u64,
    /// Files opened so far across every writer. Batch folders are assigned
    /// from this shared count, so each folder fills to exactly
    /// `files_per_folder` regardless of which writer produced its files.
    pub file_counter: Arc<AtomicU64>,
    pub schema: SchemaRef,
    pub row_group_rows: usize,
    pub part_size: usize,
    /// Multipart parts uploaded concurrently per file.
    pub max_concurrent_parts: usize,
    pub file_cap: Option<u64>,
    pub compression: Compression,
    pub dictionary: bool,
}

impl SinkSettings {
    #[cfg(test)]
    fn clone_for_test(&self) -> Self {
        Self {
            run_id: self.run_id.clone(),
            files_per_folder: self.files_per_folder,
            file_counter: self.file_counter.clone(),
            schema: self.schema.clone(),
            row_group_rows: self.row_group_rows,
            part_size: self.part_size,
            max_concurrent_parts: self.max_concurrent_parts,
            file_cap: self.file_cap,
            compression: self.compression,
            dictionary: self.dictionary,
        }
    }
}

/// `level` is the codec effort; `None` uses the codec's own default.
/// Note the parquet crate defaults zstd to level 1, its weakest setting.
pub fn parse_compression(name: &str, level: Option<i32>) -> Result<Compression> {
    let codec = match name.to_ascii_lowercase().as_str() {
        "zstd" => Compression::ZSTD(match level {
            Some(level) => ZstdLevel::try_new(level)?,
            None => ZstdLevel::default(),
        }),
        "snappy" => Compression::SNAPPY,
        "gzip" => Compression::GZIP(match level {
            Some(level) => parquet::basic::GzipLevel::try_new(level as u32)?,
            None => Default::default(),
        }),
        "lz4" => Compression::LZ4,
        "lz4_raw" => Compression::LZ4_RAW,
        "none" | "uncompressed" => Compression::UNCOMPRESSED,
        other => anyhow::bail!("unknown parquet compression `{}`", other),
    };
    Ok(codec)
}

/// Build the object store and the key prefix every file is written under.
pub fn build_store(destination: &Destination) -> Result<(Arc<dyn ObjectStore>, String)> {
    match destination {
        Destination::S3 { config } => config.build_object_store(),
        Destination::Local { directory } => {
            std::fs::create_dir_all(directory).with_context(|| {
                format!("failed to create output directory {}", directory.display())
            })?;
            let store = LocalFileSystem::new_with_prefix(directory).with_context(|| {
                format!("failed to open output directory {}", directory.display())
            })?;
            Ok((Arc::new(store), String::new()))
        }
    }
}


/// Move a shared gauge by the difference, tracking what this writer reported.
fn apply_delta(gauge: &AtomicU64, reported: &mut u64, current: u64) {
    if current >= *reported {
        gauge.fetch_add(current - *reported, Ordering::Relaxed);
    } else {
        gauge.fetch_sub(*reported - current, Ordering::Relaxed);
    }
    *reported = current;
}

struct ActiveFile {
    writer: Box<AsyncArrowWriter<BufWriter>>,
    path: String,
}

impl ActiveFile {
    /// Bytes handed to the object store so far.
    fn bytes_written(&self) -> u64 {
        self.writer.bytes_written() as u64
    }

    /// Bytes held in memory, and rows not yet encoded into a row group.
    fn in_progress(&self) -> (u64, u64) {
        (self.writer.in_progress_size() as u64, self.writer.in_progress_rows() as u64)
    }
}

/// One writer, owned by a single producer task. Files roll at the size cap.
pub struct ParquetSink {
    store: Arc<dyn ObjectStore>,
    prefix: String,
    writer_id: usize,
    file_index: u64,
    settings: Arc<SinkSettings>,
    stats: Arc<Stats>,
    active: Option<ActiveFile>,
    /// Bytes this writer has contributed to `stats.buffered_bytes`.
    reported_buffer: u64,
    /// Bytes this writer has contributed to `stats.active_bytes`.
    reported_active: u64,
    /// Rows this writer has contributed to `stats.rows_flushed`.
    reported_flushed: u64,
    /// Every row this writer has been handed, across all its files.
    rows_seen: u64,
    /// Rows written into the file currently open. Cumulative `rows_seen` cannot
    /// answer this, and abandoning a file loses every row in it, not just the
    /// ones still sitting in an unfinished row group.
    rows_in_active: u64,
}

impl ParquetSink {
    pub fn new(
        store: Arc<dyn ObjectStore>,
        prefix: String,
        writer_id: usize,
        settings: Arc<SinkSettings>,
        stats: Arc<Stats>,
    ) -> Self {
        Self {
            store,
            prefix,
            writer_id,
            file_index: 0,
            settings,
            stats,
            active: None,
            reported_buffer: 0,
            reported_active: 0,
            reported_flushed: 0,
            rows_seen: 0,
            rows_in_active: 0,
        }
    }

    /// `<prefix>part-<run>-w<writer>-<index>.parquet`, or without the run
    /// segment when no run id is set. The writer id keeps concurrent writers
    /// from colliding and the index orders one writer's files; both pad rather
    /// than truncate, so a run wider or longer than the padding still produces
    /// distinct names.
    fn next_path(&mut self) -> String {
        self.file_index += 1;
        let mut key = self.prefix.clone();
        if !self.settings.run_id.is_empty() {
            key.push_str(&self.settings.run_id);
            key.push('/');
        }
        let opened = self.settings.file_counter.fetch_add(1, Ordering::Relaxed);
        // Zero files per folder means no batch folders; checked_div yields None.
        if let Some(batch) = opened.checked_div(self.settings.files_per_folder) {
            key.push_str(&format!("batch-{:05}/", batch + 1));
        }
        key.push_str(&format!(
            "part-w{:02}-{:06}.parquet",
            self.writer_id, self.file_index
        ));
        key
    }

    fn writer_properties(&self) -> WriterProperties {
        WriterProperties::builder()
            .set_compression(self.settings.compression)
            .set_dictionary_enabled(self.settings.dictionary)
            .set_max_row_group_row_count(Some(self.settings.row_group_rows))
            .build()
    }

    async fn open(&mut self) -> Result<()> {
        let path = self.next_path();
        let object_path = ObjectPath::parse(&path)
            .with_context(|| format!("invalid object key `{}`", path))?;
        // Without with_max_concurrency the writer silently uses the default,
        // so the configured value would be ignored.
        let buffered = BufWriter::with_capacity(
            self.store.clone(),
            object_path,
            self.settings.part_size,
        )
        .with_max_concurrency(self.settings.max_concurrent_parts);

        let writer = Box::new(
            AsyncArrowWriter::try_new(
                buffered,
                self.settings.schema.clone(),
                Some(self.writer_properties()),
            )
            .with_context(|| format!("failed to start Parquet file `{}`", path))?,
        );
        self.active = Some(ActiveFile { writer, path });
        self.rows_in_active = 0;
        Ok(())
    }

    /// Write one record batch, rolling to a new file when the cap is reached.
    pub async fn write(&mut self, batch: RecordBatch) -> Result<()> {
        if self.active.is_none() {
            self.open().await?;
        }
        let rows = batch.num_rows() as u64;
        {
            let active = self.active.as_mut().expect("writer is open");
            active
                .writer
                .write(&batch)
                .await
                .with_context(|| format!("failed writing to `{}`", active.path))?;
        }
        self.rows_seen += rows;
        self.rows_in_active += rows;
        self.stats.add_rows(rows);
        self.sync_buffer_gauge();

        if let Some(cap) = self.settings.file_cap {
            let written = self.reported_active;
            // Files can only roll on a row group boundary, so a file lands
            // between the cap and the cap plus one row group.
            if written >= cap {
                self.close_active().await?;
            }
        }
        Ok(())
    }

    /// Give up on the file being written, aborting its multipart upload so it
    /// does not linger as billable parts, and reset so the next write opens a
    /// fresh file. Used when an upload has exhausted its HTTP-level retries:
    /// losing one file beats losing a run that has been going for days.
    pub async fn abandon_active(&mut self) -> u64 {
        let rows_lost = self.rows_in_active;
        if let Some(active) = self.active.take() {
            let mut buffered = active.writer.into_inner();
            if let Err(error) = buffered.abort().await {
                // Nothing further to do: the parts expire under the bucket's
                // own lifecycle rule for incomplete multipart uploads.
                eprintln!(
                    "warning: could not abort the multipart upload for `{}`: {}",
                    active.path, error
                );
            }
        }
        self.stats.files_abandoned.fetch_add(1, Ordering::Relaxed);
        self.stats.rows_lost.fetch_add(rows_lost, Ordering::Relaxed);
        // The gauges describe an open file, so they reset exactly as they do on
        // a clean close. Rolling `rows_seen` back past the whole file also
        // takes its finished row groups out of `rows_flushed`: those rows were
        // encoded, but they went into an object that will never exist, and
        // `reached_size_target` divides bytes by that figure.
        apply_delta(&self.stats.buffered_bytes, &mut self.reported_buffer, 0);
        apply_delta(&self.stats.active_bytes, &mut self.reported_active, 0);
        self.rows_seen = self.rows_seen.saturating_sub(rows_lost);
        self.rows_in_active = 0;
        apply_delta(
            &self.stats.rows_flushed,
            &mut self.reported_flushed,
            self.rows_seen,
        );
        rows_lost
    }

    /// Publish this writer's in-flight numbers into the shared gauges.
    /// Both are delta-updated so the totals stay correct across writers.
    fn sync_buffer_gauge(&mut self) {
        let (buffered, file_bytes, pending_rows) = match self.active.as_ref() {
            Some(active) => {
                let (buffered, pending_rows) = active.in_progress();
                (buffered, active.bytes_written(), pending_rows)
            }
            // With no file open, everything this writer holds has been flushed.
            None => (0, 0, 0),
        };
        apply_delta(&self.stats.buffered_bytes, &mut self.reported_buffer, buffered);
        apply_delta(&self.stats.active_bytes, &mut self.reported_active, file_bytes);
        apply_delta(
            &self.stats.rows_flushed,
            &mut self.reported_flushed,
            self.rows_seen.saturating_sub(pending_rows),
        );
    }

    async fn close_active(&mut self) -> Result<()> {
        let Some(active) = self.active.take() else {
            return Ok(());
        };
        let path = active.path.clone();
        // close() writes the footer and completes the multipart upload.
        let metadata = active
            .writer
            .close()
            .await
            .with_context(|| format!("failed to finish Parquet file `{}`", path))?;
        let bytes: u64 = metadata
            .row_groups()
            .iter()
            .map(|group| group.compressed_size().max(0) as u64)
            .sum();
        self.stats.bytes_written.fetch_add(bytes, Ordering::Relaxed);
        self.stats.files_completed.fetch_add(1, Ordering::Relaxed);
        // Those rows are in storage now, so a later abandon cannot lose them.
        self.rows_in_active = 0;
        // Both byte gauges describe an open file, so they reset when it closes.
        apply_delta(&self.stats.buffered_bytes, &mut self.reported_buffer, 0);
        apply_delta(&self.stats.active_bytes, &mut self.reported_active, 0);
        // Closing flushes everything this writer was still holding.
        apply_delta(
            &self.stats.rows_flushed,
            &mut self.reported_flushed,
            self.rows_seen,
        );
        Ok(())
    }

    /// Finish the file in flight. Must be called or the object never appears.
    pub async fn finish(mut self) -> Result<()> {
        self.close_active().await
    }

    pub fn current_file(&self) -> Option<(String, u64)> {
        self.active
            .as_ref()
            .map(|active| (active.path.clone(), active.bytes_written()))
    }
}

#[cfg(test)]
mod tests {

    use super::*;
    use crate::parquet_out::BatchBuilder;
    use crate::schema::parse_schema;
    use datagen::Value;

    fn settings(schema: SchemaRef, cap: Option<u64>) -> Arc<SinkSettings> {
        Arc::new(SinkSettings {
            // Tests assert on file names, so keep them free of a timestamp.
            run_id: String::new(),
            files_per_folder: 0,
            file_counter: Arc::new(AtomicU64::new(0)),
            schema,
            row_group_rows: 500,
            part_size: 5 << 20,
            max_concurrent_parts: 4,
            file_cap: cap,
            compression: Compression::UNCOMPRESSED,
            dictionary: true,
        })
    }

    #[test]
    fn maps_compression_names() {
        assert!(matches!(parse_compression("zstd", None).unwrap(), Compression::ZSTD(_)));
        assert!(matches!(parse_compression("SNAPPY", None).unwrap(), Compression::SNAPPY));
        assert!(matches!(
            parse_compression("none", None).unwrap(),
            Compression::UNCOMPRESSED
        ));
        assert!(parse_compression("brotli", None).is_err());

        // The level must reach the codec, not be silently dropped.
        let Compression::ZSTD(level) = parse_compression("zstd", Some(9)).unwrap() else {
            panic!("expected zstd");
        };
        assert_eq!(level.compression_level(), 9);
        assert!(parse_compression("zstd", Some(99)).is_err());
    }

    #[tokio::test]
    async fn writes_a_readable_parquet_file_locally() {
        let dir = std::env::temp_dir().join(format!("dpsg-sink-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let destination = Destination::Local { directory: dir.clone() };
        let (store, prefix) = build_store(&destination).expect("store");

        let tables = parse_schema(
            "CREATE TABLE t (id BIGINT NOT NULL, amount DECIMAL(19,4), ts DATETIME(6), note VARCHAR(64)) ENGINE=OLAP",
        )
        .unwrap();
        let columns = tables[0].columns.clone();
        let mut builder = BatchBuilder::new(columns, 256).unwrap();
        let stats = Arc::new(Stats::default());
        let mut sink = ParquetSink::new(
            store,
            prefix,
            0,
            settings(builder.schema(), None),
            stats.clone(),
        );

        for index in 0..1000i64 {
            let row: Vec<(String, Value)> = vec![
                ("id".into(), Value::I64(index)),
                ("amount".into(), Value::String(format!("{}.25", index % 90 + 1))),
                ("ts".into(), Value::String("2026-09-10 12:00:00.500000".into())),
                ("note".into(), Value::String(format!("row {}", index))),
            ];
            builder
                .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, v)| v))
                .unwrap();
        }
        let batch = builder.finish().unwrap();
        sink.write(batch).await.expect("write batch");
        sink.finish().await.expect("finish");

        assert_eq!(stats.rows.load(Ordering::Relaxed), 1000);
        assert_eq!(stats.files_completed.load(Ordering::Relaxed), 1);
        assert_eq!(
            stats.buffered_bytes.load(Ordering::Relaxed),
            0,
            "buffer gauge must return to zero"
        );

        // Read the file back to prove it is valid Parquet with the right rows.
        let written: Vec<_> = std::fs::read_dir(&dir)
            .unwrap()
            .filter_map(|entry| entry.ok())
            .filter(|entry| entry.path().extension().is_some_and(|ext| ext == "parquet"))
            .collect();
        assert_eq!(written.len(), 1, "expected exactly one parquet file");

        let file = std::fs::File::open(written[0].path()).unwrap();
        let reader = parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder::try_new(file)
            .unwrap()
            .build()
            .unwrap();
        let total: usize = reader.map(|batch| batch.unwrap().num_rows()).sum();
        assert_eq!(total, 1000, "row count must survive the round trip");

        std::fs::remove_dir_all(&dir).ok();
    }

    /// The typed `Value::Timestamp` / `Value::Decimal` path skips the format
    /// and reparse the text path does, so it needs its own round trip: write
    /// typed values, read the Parquet back, and check the numbers landed.
    #[tokio::test]
    async fn typed_timestamps_and_decimals_survive_the_round_trip() {
        use arrow::array::{Decimal128Array, TimestampMicrosecondArray};

        let dir = std::env::temp_dir().join(format!("dpsg-typed-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let destination = Destination::Local { directory: dir.clone() };
        let (store, prefix) = build_store(&destination).expect("store");

        let tables = parse_schema(
            "CREATE TABLE t (id BIGINT NOT NULL, amount DECIMAL(19,4), ts DATETIME(6)) ENGINE=OLAP",
        )
        .unwrap();
        let mut builder = BatchBuilder::new(tables[0].columns.clone(), 256).unwrap();
        let stats = Arc::new(Stats::default());
        let mut sink = ParquetSink::new(
            store,
            prefix,
            0,
            settings(builder.schema(), None),
            stats.clone(),
        );

        let format: Arc<str> = Arc::from("%Y-%m-%d %H:%M:%S%.6f");
        // Scale 2 into a scale-4 column, so the rescale path is exercised too.
        let expected: Vec<(i64, i128, i64)> = (0..500i64)
            .map(|index| {
                let units = (index % 9000) + 100; // 1.00 .. 91.99 at scale 2
                let micros = 1_757_500_000_000_000 + index * 1_000_037;
                (index, units as i128, micros)
            })
            .collect();

        for (index, units, micros) in &expected {
            let row: Vec<(String, Value)> = vec![
                ("id".into(), Value::I64(*index)),
                (
                    "amount".into(),
                    Value::Decimal {
                        units: *units,
                        scale: 2,
                    },
                ),
                (
                    "ts".into(),
                    Value::Timestamp {
                        micros: *micros,
                        format: format.clone(),
                    },
                ),
            ];
            builder
                .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, v)| v))
                .unwrap();
        }
        let batch = builder.finish().unwrap();
        sink.write(batch).await.expect("write batch");
        sink.finish().await.expect("finish");

        let written: Vec<_> = std::fs::read_dir(&dir)
            .unwrap()
            .filter_map(|entry| entry.ok())
            .filter(|entry| entry.path().extension().is_some_and(|ext| ext == "parquet"))
            .collect();
        assert_eq!(written.len(), 1);

        let file = std::fs::File::open(written[0].path()).unwrap();
        let reader = parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder::try_new(file)
            .unwrap()
            .build()
            .unwrap();

        let mut seen = 0usize;
        for batch in reader {
            let batch = batch.unwrap();
            let amounts = batch
                .column(1)
                .as_any()
                .downcast_ref::<Decimal128Array>()
                .expect("decimal column");
            let stamps = batch
                .column(2)
                .as_any()
                .downcast_ref::<TimestampMicrosecondArray>()
                .expect("timestamp column");
            for row in 0..batch.num_rows() {
                let (_, units, micros) = expected[seen];
                // Scale 2 stored into a scale-4 column is x100.
                assert_eq!(amounts.value(row), units * 100, "amount at row {}", seen);
                assert_eq!(stamps.value(row), micros, "timestamp at row {}", seen);
                seen += 1;
            }
        }
        assert_eq!(seen, expected.len());

        std::fs::remove_dir_all(&dir).ok();
    }

    /// Abandoning a file has to leave the writer usable: the point is that a
    /// run of several days survives one bad upload, so the next batch must
    /// land in a fresh file and the count must say a file was lost.
    #[tokio::test]
    async fn a_writer_keeps_going_after_abandoning_a_file() {
        let dir = std::env::temp_dir().join(format!("dpsg-abandon-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let destination = Destination::Local { directory: dir.clone() };
        let (store, prefix) = build_store(&destination).expect("store");

        let tables =
            parse_schema("CREATE TABLE t (id BIGINT NOT NULL, note VARCHAR(64)) ENGINE=OLAP")
                .unwrap();
        let mut builder = BatchBuilder::new(tables[0].columns.clone(), 256).unwrap();
        let stats = Arc::new(Stats::default());
        let mut sink = ParquetSink::new(
            store,
            prefix,
            0,
            settings(builder.schema(), None),
            stats.clone(),
        );

        let batch_of = |builder: &mut BatchBuilder, count: i64| {
            for index in 0..count {
                let row: Vec<(String, Value)> = vec![
                    ("id".into(), Value::I64(index)),
                    ("note".into(), Value::String(format!("row {}", index))),
                ];
                builder
                    .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, v)| v))
                    .unwrap();
            }
            builder.finish().unwrap()
        };

        let first = batch_of(&mut builder, 500);
        sink.write(first).await.expect("first batch");
        // Simulate the upload having exhausted its retries.
        let lost = sink.abandon_active().await;
        assert_eq!(stats.files_abandoned.load(Ordering::Relaxed), 1);
        // Every row in the file is lost, including the ones whose row group had
        // already been encoded. A --rows run credits this back to its quota.
        assert_eq!(lost, 500);
        assert_eq!(stats.rows_lost.load(Ordering::Relaxed), 500);
        assert_eq!(
            stats.rows_flushed.load(Ordering::Relaxed),
            0,
            "rows in an object that will never exist must not count as flushed"
        );

        let second = batch_of(&mut builder, 500);
        sink.write(second).await.expect("writer still usable");
        sink.finish().await.expect("finish");

        // Exactly one file: the abandoned one never completed.
        let written: Vec<_> = std::fs::read_dir(&dir)
            .unwrap()
            .filter_map(|entry| entry.ok())
            .filter(|entry| entry.path().extension().is_some_and(|ext| ext == "parquet"))
            .collect();
        assert_eq!(written.len(), 1, "abandoned file must not appear");
        assert_eq!(stats.files_completed.load(Ordering::Relaxed), 1);

        let file = std::fs::File::open(written[0].path()).unwrap();
        let reader = parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder::try_new(file)
            .unwrap()
            .build()
            .unwrap();
        let total: usize = reader.map(|batch| batch.unwrap().num_rows()).sum();
        assert_eq!(total, 500, "only the second batch survives");

        std::fs::remove_dir_all(&dir).ok();
    }

    /// The run id is what stops a restart from overwriting the last run, so
    /// pin both spellings of the name.
    #[test]
    fn object_names_carry_the_writer_and_the_run() {
        let tables = parse_schema("CREATE TABLE t (id BIGINT NOT NULL) ENGINE=OLAP").unwrap();
        let builder = BatchBuilder::new(tables[0].columns.clone(), 8).unwrap();
        let stats = Arc::new(Stats::default());
        let store: Arc<dyn ObjectStore> = Arc::new(object_store::memory::InMemory::new());

        let plain = settings(builder.schema(), None);
        let mut sink = ParquetSink::new(store.clone(), "out/".into(), 7, plain, stats.clone());
        assert_eq!(sink.next_path(), "out/part-w07-000001.parquet");
        assert_eq!(sink.next_path(), "out/part-w07-000002.parquet");

        // A run id becomes a folder of its own, so a restart adds a sibling
        // folder instead of replacing the last run's files.
        let mut tagged = settings(builder.schema(), None).clone_for_test();
        tagged.run_id = "20260910T143803Z-79ee76".into();
        let mut sink =
            ParquetSink::new(store.clone(), "out/".into(), 0, Arc::new(tagged), stats.clone());
        assert_eq!(
            sink.next_path(),
            "out/20260910T143803Z-79ee76/part-w00-000001.parquet"
        );
        assert_ne!(sink.next_path(), "out/part-w00-000002.parquet");

        // Batch folders nest inside the run folder and count files across
        // every writer that shares the settings.
        let mut batched = settings(builder.schema(), None).clone_for_test();
        batched.run_id = "r1".into();
        batched.files_per_folder = 2;
        let batched = Arc::new(batched);
        let mut a = ParquetSink::new(store.clone(), "out/".into(), 0, batched.clone(), stats.clone());
        let mut b = ParquetSink::new(store, "out/".into(), 1, batched, stats);
        assert_eq!(a.next_path(), "out/r1/batch-00001/part-w00-000001.parquet");
        assert_eq!(b.next_path(), "out/r1/batch-00001/part-w01-000001.parquet");
        assert_eq!(a.next_path(), "out/r1/batch-00002/part-w00-000002.parquet");
    }

    #[tokio::test]
    async fn rolls_to_a_new_file_at_the_size_cap() {
        let dir = std::env::temp_dir().join(format!("dpsg-roll-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let destination = Destination::Local { directory: dir.clone() };
        let (store, prefix) = build_store(&destination).expect("store");

        let tables =
            parse_schema("CREATE TABLE t (id BIGINT NOT NULL, note VARCHAR(64)) ENGINE=OLAP").unwrap();
        let mut builder = BatchBuilder::new(tables[0].columns.clone(), 512).unwrap();
        let stats = Arc::new(Stats::default());
        // A tiny cap forces a roll after almost every batch.
        let mut sink = ParquetSink::new(
            store,
            prefix,
            3,
            settings(builder.schema(), Some(4096)),
            stats.clone(),
        );

        for round in 0..5 {
            for index in 0..500i64 {
                let row: Vec<(String, Value)> = vec![
                    ("id".into(), Value::I64(round * 500 + index)),
                    ("note".into(), Value::String(format!("value number {}", index))),
                ];
                builder
                    .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, v)| v))
                    .unwrap();
            }
            let batch = builder.finish().unwrap();
            sink.write(batch).await.expect("write");
        }
        sink.finish().await.expect("finish");

        let mut names: Vec<String> = std::fs::read_dir(&dir)
            .unwrap()
            .filter_map(|entry| entry.ok())
            .map(|entry| entry.file_name().to_string_lossy().to_string())
            .filter(|name| name.ends_with(".parquet"))
            .collect();
        names.sort();
        assert!(names.len() > 1, "cap should have produced several files: {:?}", names);
        assert_eq!(
            stats.files_completed.load(Ordering::Relaxed) as usize,
            names.len()
        );
        // The writer id appears in the name so parallel writers never collide.
        assert!(names.iter().all(|name| name.starts_with("part-w03-")), "{:?}", names);
        assert_eq!(stats.rows.load(Ordering::Relaxed), 2500);

        std::fs::remove_dir_all(&dir).ok();
    }

    /// `row_group_rows` is a hard maximum, applied before writing: a batch that
    /// would overflow the current row group is split so the group lands exactly
    /// on the limit. The file size cap is the opposite, applied after a batch is
    /// written, so a file overshoots by up to one row group.
    #[tokio::test]
    async fn row_groups_are_split_to_the_exact_limit() {
        let dir = std::env::temp_dir().join(format!("dpsg-rg-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let (store, prefix) = build_store(&Destination::Local { directory: dir.clone() }).unwrap();

        let tables =
            parse_schema("CREATE TABLE t (id BIGINT NOT NULL) ENGINE=OLAP").unwrap();
        let mut builder = BatchBuilder::new(tables[0].columns.clone(), 1000).unwrap();
        let mut settings = (*settings(builder.schema(), None)).clone_for_test();
        // 1500 does not divide the 1000-row batches, so a split must happen.
        settings.row_group_rows = 1500;
        let stats = Arc::new(Stats::default());
        let mut sink =
            ParquetSink::new(store, prefix, 0, Arc::new(settings), stats.clone());

        // Six batches of 1000 rows: 6000 rows total, so four full row groups.
        for round in 0..6i64 {
            for index in 0..1000i64 {
                let row = vec![("id".to_string(), Value::I64(round * 1000 + index))];
                builder
                    .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, v)| v))
                    .unwrap();
            }
            let batch = builder.finish().unwrap();
            sink.write(batch).await.unwrap();
        }
        sink.finish().await.unwrap();

        let file = std::fs::read_dir(&dir)
            .unwrap()
            .filter_map(|entry| entry.ok())
            .find(|entry| entry.path().extension().is_some_and(|ext| ext == "parquet"))
            .expect("one file");
        let handle = std::fs::File::open(file.path()).unwrap();
        let reader =
            parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder::try_new(handle).unwrap();
        let meta = reader.metadata();
        let counts: Vec<i64> = (0..meta.num_row_groups())
            .map(|index| meta.row_group(index).num_rows())
            .collect();
        assert_eq!(
            counts,
            vec![1500, 1500, 1500, 1500],
            "row groups must be split to exactly the limit, not overshoot"
        );

        std::fs::remove_dir_all(&dir).ok();
    }

    /// Each run gets its own folder, and files fill numbered batch folders
    /// to exactly the configured count across all writers.
    #[tokio::test]
    async fn groups_files_into_run_and_batch_folders() {
        let dir = std::env::temp_dir().join(format!("dpsg-folders-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let (store, prefix) = build_store(&Destination::Local { directory: dir.clone() }).unwrap();

        let tables = parse_schema("CREATE TABLE t (id BIGINT NOT NULL, note VARCHAR(64)) ENGINE=OLAP").unwrap();
        let schema = BatchBuilder::new(tables[0].columns.clone(), 512).unwrap().schema();
        let mut shared = (*settings(schema, Some(4096))).clone_for_test();
        shared.run_id = "run-abc".to_string();
        shared.files_per_folder = 3;
        let shared = Arc::new(shared);
        let stats = Arc::new(Stats::default());

        // Two writers interleaving, each rolling several small files.
        let mut sinks: Vec<ParquetSink> = (0..2)
            .map(|id| ParquetSink::new(store.clone(), prefix.clone(), id, shared.clone(), stats.clone()))
            .collect();
        let mut builder = BatchBuilder::new(tables[0].columns.clone(), 512).unwrap();
        for round in 0..4i64 {
            for sink in sinks.iter_mut() {
                for index in 0..500i64 {
                    let row: Vec<(String, Value)> = vec![
                        ("id".into(), Value::I64(round * 500 + index)),
                        ("note".into(), Value::String(format!("value number {}", index))),
                    ];
                    builder
                        .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, v)| v))
                        .unwrap();
                }
                sink.write(builder.finish().unwrap()).await.unwrap();
            }
        }
        for sink in sinks {
            sink.finish().await.unwrap();
        }

        // Only the run folder sits at the top level.
        let top: Vec<String> = std::fs::read_dir(&dir)
            .unwrap()
            .map(|entry| entry.unwrap().file_name().to_string_lossy().to_string())
            .collect();
        assert_eq!(top, vec!["run-abc".to_string()], "{:?}", top);

        let mut folders: Vec<(String, usize)> = std::fs::read_dir(dir.join("run-abc"))
            .unwrap()
            .map(|entry| {
                let entry = entry.unwrap();
                let files = std::fs::read_dir(entry.path())
                    .unwrap()
                    .filter(|file| {
                        file.as_ref().unwrap().path().extension().is_some_and(|ext| ext == "parquet")
                    })
                    .count();
                (entry.file_name().to_string_lossy().to_string(), files)
            })
            .collect();
        folders.sort();
        let total: usize = folders.iter().map(|(_, count)| count).sum();
        assert_eq!(total as u64, stats.files_completed.load(Ordering::Relaxed));
        assert!(folders.len() >= 2, "expected several batch folders: {:?}", folders);
        assert_eq!(folders[0].0, "batch-00001");
        // Every folder but the last is exactly full.
        for (name, count) in &folders[..folders.len() - 1] {
            assert_eq!(*count, 3, "{} should hold 3 files: {:?}", name, folders);
        }
        assert!(folders.last().unwrap().1 <= 3);

        std::fs::remove_dir_all(&dir).ok();
    }
}
