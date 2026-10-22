//! Where encoded records go: stdout, rotating files or S3 objects (as text,
//! binary streams or Parquet), or Kafka.

pub mod console;
pub mod file;
pub mod kafka;
pub mod object;
pub mod parquet;

use anyhow::Result;
use async_trait::async_trait;

pub use console::ConsoleSink;
pub use file::{FileLayout, FileSink};
pub use kafka::{KafkaSink, KafkaSinkOptions};
pub use object::{ObjectSink, S3Options};
pub use parquet::{ParquetOptions, ParquetSink, ParquetTarget};

/// One framed record and, for keyed sinks, its key.
#[derive(Debug, Clone, Default)]
pub struct Encoded {
    pub key: Option<Vec<u8>>,
    pub payload: Vec<u8>,
}

#[async_trait]
pub trait Sink: Send {
    /// Deliver one record. A sink that writes a prelude per file or stream
    /// takes care of it here.
    async fn write(&mut self, message: &Encoded) -> Result<()>;

    /// A batch boundary: a good moment to push buffered bytes out.
    async fn flush(&mut self) -> Result<()>;

    /// No more records: close files, wait for acknowledgements.
    async fn finish(&mut self) -> Result<()>;
}
