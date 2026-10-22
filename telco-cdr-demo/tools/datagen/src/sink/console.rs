//! Rows to stdout, prelude first. Nothing else is written there.

use std::io::{self, BufWriter, Write};

use anyhow::{Context, Result};
use async_trait::async_trait;

use super::{Encoded, Sink};

pub struct ConsoleSink {
    out: BufWriter<io::Stdout>,
    prelude: Option<Vec<u8>>,
}

impl ConsoleSink {
    pub fn new(prelude: Option<Vec<u8>>) -> Self {
        Self { out: BufWriter::with_capacity(1 << 16, io::stdout()), prelude }
    }
}

#[async_trait]
impl Sink for ConsoleSink {
    async fn write(&mut self, message: &Encoded) -> Result<()> {
        if let Some(prelude) = self.prelude.take() {
            self.out.write_all(&prelude).context("failed to write to stdout")?;
        }
        self.out.write_all(&message.payload).context("failed to write to stdout")
    }

    async fn flush(&mut self) -> Result<()> {
        self.out.flush().context("failed to flush stdout")
    }

    async fn finish(&mut self) -> Result<()> {
        if let Some(prelude) = self.prelude.take() {
            self.out.write_all(&prelude).context("failed to write to stdout")?;
        }
        self.out.flush().context("failed to flush stdout")
    }
}
