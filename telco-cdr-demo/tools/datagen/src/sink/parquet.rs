//! Rows to Parquet files, local or in S3, rotated and named like the file
//! sink.
//!
//! Generator threads hand over rows as JSON lines (Arrow mode: timestamps
//! at full precision). The sink buffers a row group's worth, decodes them
//! through Arrow's JSON reader against the Arrow schema, and writes the
//! batch with the Parquet writer. The same code path serves local files
//! (a tokio file) and S3 (a multipart upload), since both are async writers.

use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{anyhow, bail, Context, Result};
use arrow::datatypes::{Schema, SchemaRef};
use arrow::json::ReaderBuilder;
use async_trait::async_trait;
use object_store::buffered::BufWriter;
use object_store::ObjectStore;
use parquet::arrow::AsyncArrowWriter;
use parquet::basic::{Compression, GzipLevel, ZstdLevel};
use parquet::file::properties::WriterProperties;
use tokio::io::AsyncWrite;

use super::file::FileLayout;
use super::{Encoded, Sink};

type Writer = AsyncArrowWriter<Box<dyn AsyncWrite + Unpin + Send>>;

#[derive(Debug, Clone)]
pub struct ParquetOptions {
    pub row_group_rows: usize,
    /// zstd, snappy, gzip, lz4 or none.
    pub compression: String,
    pub compression_level: i32,
}

impl ParquetOptions {
    pub fn writer_properties(&self) -> Result<WriterProperties> {
        let compression = match self.compression.to_ascii_lowercase().as_str() {
            "zstd" => Compression::ZSTD(
                ZstdLevel::try_new(self.compression_level).map_err(|error| anyhow!("zstd level: {}", error))?,
            ),
            "snappy" => Compression::SNAPPY,
            "gzip" => Compression::GZIP(
                GzipLevel::try_new(self.compression_level as u32)
                    .map_err(|error| anyhow!("gzip level: {}", error))?,
            ),
            "lz4" => Compression::LZ4_RAW,
            "none" | "uncompressed" => Compression::UNCOMPRESSED,
            other => bail!("unknown parquet.compression `{}`; use zstd, snappy, gzip, lz4 or none", other),
        };
        Ok(WriterProperties::builder()
            .set_compression(compression)
            .set_max_row_group_row_count(Some(self.row_group_rows.max(1)))
            .build())
    }
}

/// Where the files go.
pub enum ParquetTarget {
    Local(FileLayout),
    Object {
        store: Arc<dyn ObjectStore>,
        prefix: String,
        layout: FileLayout,
        part_size: usize,
        max_concurrent_parts: usize,
    },
}

pub struct ParquetSink {
    schema: SchemaRef,
    properties: WriterProperties,
    target: ParquetTarget,
    row_group_rows: usize,
    file_size: Option<u64>,
    /// JSON lines waiting to become the next row group.
    buffer: Vec<u8>,
    buffered_rows: usize,
    writer: Option<Writer>,
    file_index: u64,
    pub files_written: u64,
}

impl ParquetSink {
    pub fn new(schema: Schema, options: &ParquetOptions, target: ParquetTarget, file_size: Option<u64>) -> Result<Self> {
        Ok(Self {
            schema: Arc::new(schema),
            properties: options.writer_properties()?,
            target,
            row_group_rows: options.row_group_rows.max(1),
            file_size,
            buffer: Vec::new(),
            buffered_rows: 0,
            writer: None,
            file_index: 0,
            files_written: 0,
        })
    }

    fn layout(&self) -> &FileLayout {
        match &self.target {
            ParquetTarget::Local(layout) => layout,
            ParquetTarget::Object { layout, .. } => layout,
        }
    }

    /// Where the `index`-th file lands, for messages and tests.
    pub fn location_for(&self, index: u64) -> String {
        match &self.target {
            ParquetTarget::Local(layout) => layout.path_for(index).display().to_string(),
            ParquetTarget::Object { prefix, layout, .. } => object_key(prefix, layout, index).to_string(),
        }
    }

    async fn open_next(&mut self) -> Result<()> {
        self.file_index += 1;
        let index = self.file_index;
        let sink: Box<dyn AsyncWrite + Unpin + Send> = match &self.target {
            ParquetTarget::Local(layout) => {
                let path: PathBuf = layout.path_for(index);
                if let Some(parent) = path.parent() {
                    std::fs::create_dir_all(parent)
                        .with_context(|| format!("failed to create {}", parent.display()))?;
                }
                eprintln!("writing to {}", path.display());
                let file = tokio::fs::File::create(&path)
                    .await
                    .with_context(|| format!("failed to create {}", path.display()))?;
                Box::new(file)
            }
            ParquetTarget::Object { store, prefix, layout, part_size, max_concurrent_parts } => {
                let key = object_key(prefix, layout, index);
                eprintln!("uploading to {}", key);
                Box::new(
                    BufWriter::with_capacity(store.clone(), key, *part_size).with_max_concurrency(*max_concurrent_parts),
                )
            }
        };
        let writer = AsyncArrowWriter::try_new(sink, self.schema.clone(), Some(self.properties.clone()))
            .context("failed to start a Parquet file")?;
        self.writer = Some(writer);
        self.files_written += 1;
        Ok(())
    }

    async fn close_current(&mut self) -> Result<()> {
        if let Some(writer) = self.writer.take() {
            writer.close().await.context("failed to finish a Parquet file")?;
        }
        Ok(())
    }

    /// Decode the buffered rows and write them as one row group.
    async fn flush_group(&mut self) -> Result<()> {
        if self.buffered_rows == 0 {
            return Ok(());
        }
        let mut decoder = ReaderBuilder::new(self.schema.clone())
            .with_batch_size(self.buffered_rows)
            .with_coerce_primitive(true)
            .build_decoder()
            .context("failed to build the Arrow JSON decoder")?;
        decoder
            .decode(&self.buffer)
            .context("a generated row does not fit the Parquet schema")?;
        if decoder.has_partial_record() {
            bail!("a generated row was cut short before the Parquet writer");
        }
        let batch = decoder
            .flush()
            .context("a generated row does not fit the Parquet schema")?
            .ok_or_else(|| anyhow!("no rows decoded from {} buffered", self.buffered_rows))?;
        self.buffer.clear();
        self.buffered_rows = 0;

        if self.writer.is_none() {
            self.open_next().await?;
        }
        let writer = self.writer.as_mut().expect("opened above");
        writer.write(&batch).await.context("failed to write a Parquet row group")?;
        writer.flush().await.context("failed to flush a Parquet row group")?;
        if self.file_size.is_some_and(|limit| writer.bytes_written() as u64 >= limit) {
            self.close_current().await?;
        }
        Ok(())
    }
}

fn object_key(prefix: &str, layout: &FileLayout, index: u64) -> object_store::path::Path {
    let relative = layout.path_for(index);
    let relative = relative
        .strip_prefix(&layout.directory)
        .unwrap_or(&relative)
        .components()
        .map(|component| component.as_os_str().to_string_lossy().into_owned())
        .collect::<Vec<_>>()
        .join("/");
    object_store::path::Path::from(format!("{}{}", prefix, relative))
}

#[async_trait]
impl Sink for ParquetSink {
    async fn write(&mut self, message: &Encoded) -> Result<()> {
        self.buffer.extend_from_slice(&message.payload);
        if !message.payload.ends_with(b"\n") {
            self.buffer.push(b'\n');
        }
        self.buffered_rows += 1;
        if self.buffered_rows >= self.row_group_rows {
            self.flush_group().await?;
        }
        Ok(())
    }

    async fn flush(&mut self) -> Result<()> {
        // Row groups stay full; a batch boundary is not a reason to cut one.
        Ok(())
    }

    async fn finish(&mut self) -> Result<()> {
        self.flush_group().await?;
        if self.writer.is_none() && self.files_written == 0 {
            // No rows at all: still leave a well-formed, empty file behind.
            self.open_next().await?;
        }
        let _ = self.layout();
        self.close_current().await
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use arrow::array::{Array, AsArray};
    use arrow::datatypes::{DataType, Field, TimeUnit};
    use parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder;

    fn schema() -> Schema {
        Schema::new(vec![
            Field::new("id", DataType::Int64, true),
            Field::new(
                "customer",
                DataType::Struct(vec![Field::new("name", DataType::Utf8, true)].into()),
                true,
            ),
            Field::new("amount", DataType::Decimal128(38, 2), true),
            Field::new("at", DataType::Timestamp(TimeUnit::Microsecond, None), true),
            Field::new("tags", DataType::List(Arc::new(Field::new("item", DataType::Utf8, true))), true),
        ])
    }

    fn row(index: i64) -> Encoded {
        Encoded {
            key: None,
            payload: format!(
                "{{\"id\":{},\"customer\":{{\"name\":\"n{}\"}},\"amount\":12.3{},\"at\":\"2024-01-01T00:00:00.000{:03}\",\"tags\":[\"a\",\"b\"]}}\n",
                index, index, index % 10, index
            )
            .into_bytes(),
        }
    }

    fn walk(dir: &std::path::Path) -> Vec<PathBuf> {
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
    async fn writes_readable_parquet_and_rotates() {
        let dir = std::env::temp_dir().join(format!("datagen-parquet-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let layout = FileLayout {
            directory: dir.clone(),
            file_name_prefix: "part-".into(),
            folder_prefix: "batch-".into(),
            files_per_folder: 2,
            file_size: None,
            extension: "parquet".into(),
            writer: None,
        };
        let options = ParquetOptions { row_group_rows: 10, compression: "zstd".into(), compression_level: 3 };
        // 10-row groups of tiny rows are a few hundred bytes; rotate at 1 byte
        // so every row group becomes its own file.
        let mut sink = ParquetSink::new(schema(), &options, ParquetTarget::Local(layout), Some(1)).unwrap();
        for index in 0..35 {
            sink.write(&row(index)).await.unwrap();
        }
        sink.finish().await.unwrap();

        let files = walk(&dir);
        let names: Vec<String> = files
            .iter()
            .map(|p| p.strip_prefix(&dir).unwrap().to_string_lossy().replace('\\', "/"))
            .collect();
        assert_eq!(names[0], "batch-00001/part-000001.parquet");
        assert_eq!(files.len(), 4, "35 rows in groups of 10 is 4 files: {:?}", names);

        let mut total = 0;
        for file in &files {
            let reader = ParquetRecordBatchReaderBuilder::try_new(std::fs::File::open(file).unwrap())
                .unwrap()
                .build()
                .unwrap();
            for batch in reader {
                let batch = batch.unwrap();
                total += batch.num_rows();
                assert_eq!(batch.schema().field(2).data_type(), &DataType::Decimal128(38, 2));
                let customer = batch.column(1).as_struct();
                assert!(customer.column(0).as_string::<i32>().value(0).starts_with('n'));
                let at = batch.column(3).as_primitive::<arrow::datatypes::TimestampMicrosecondType>();
                assert!(at.value(0) >= 1_704_067_200_000_000);
                assert_eq!(batch.column(4).as_list::<i32>().value(0).len(), 2);
            }
        }
        assert_eq!(total, 35);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[tokio::test]
    async fn uploads_parquet_objects() {
        let store: Arc<dyn ObjectStore> = Arc::new(object_store::memory::InMemory::new());
        let layout = FileLayout {
            directory: PathBuf::from("."),
            file_name_prefix: "part-".into(),
            folder_prefix: "batch-".into(),
            files_per_folder: 0,
            file_size: None,
            extension: "parquet".into(),
            writer: Some(1),
        };
        let options = ParquetOptions { row_group_rows: 100, compression: "snappy".into(), compression_level: 0 };
        let target = ParquetTarget::Object {
            store: store.clone(),
            prefix: "run/".into(),
            layout,
            part_size: 5 << 20,
            max_concurrent_parts: 2,
        };
        let mut sink = ParquetSink::new(schema(), &options, target, None).unwrap();
        assert_eq!(sink.location_for(1), "run/part-w01-000001.parquet");
        for index in 0..7 {
            sink.write(&row(index)).await.unwrap();
        }
        sink.finish().await.unwrap();
        let bytes = store
            .get_opts(&object_store::path::Path::from("run/part-w01-000001.parquet"), object_store::GetOptions::default())
            .await
            .unwrap()
            .bytes()
            .await
            .unwrap();
        let reader = ParquetRecordBatchReaderBuilder::try_new(bytes).unwrap().build().unwrap();
        let rows: usize = reader.map(|b| b.unwrap().num_rows()).sum();
        assert_eq!(rows, 7);
    }

    #[test]
    fn rejects_an_unknown_compression() {
        let options = ParquetOptions { row_group_rows: 1, compression: "brotli9".into(), compression_level: 0 };
        assert!(options.writer_properties().is_err());
    }
}
