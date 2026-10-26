//! Rows to files that rotate by size, grouped into numbered folders.
//!
//! Files are `<directory>/<folder_prefix><NNNNN>/<file_name_prefix><NNNNNN>.<ext>`
//! with `files_per_folder` files in each folder, or directly under the
//! directory when that is zero. Every file starts with the prelude (the CSV
//! header, or the length-prefixed schema), so each one stands on its own.

use std::fs::File;
use std::io::{BufWriter, Write};
use std::path::PathBuf;

use anyhow::{Context, Result};
use async_trait::async_trait;

use super::{Encoded, Sink};

#[derive(Debug, Clone)]
pub struct FileLayout {
    pub directory: PathBuf,
    pub file_name_prefix: String,
    pub folder_prefix: String,
    pub files_per_folder: u64,
    /// Rotate once a file reaches this many bytes; `None` means one file.
    pub file_size: Option<u64>,
    pub extension: String,
    /// With several sink threads each writer tags its files `wNN-`, so
    /// they never collide; a lone writer leaves the tag out.
    pub writer: Option<usize>,
}

impl FileLayout {
    /// The path of the `index`-th file, counting from 1.
    pub fn path_for(&self, index: u64) -> PathBuf {
        let tag = self.writer.map(|writer| format!("w{:02}-", writer)).unwrap_or_default();
        let name = format!("{}{}{:06}.{}", self.file_name_prefix, tag, index, self.extension);
        if self.files_per_folder == 0 {
            return self.directory.join(name);
        }
        let folder = (index - 1) / self.files_per_folder + 1;
        self.directory.join(format!("{}{:05}", self.folder_prefix, folder)).join(name)
    }
}

pub struct FileSink {
    layout: FileLayout,
    prelude: Vec<u8>,
    writer: Option<BufWriter<File>>,
    file_index: u64,
    bytes_in_file: u64,
    pub files_written: u64,
}

impl FileSink {
    pub fn new(layout: FileLayout, prelude: Option<Vec<u8>>) -> Self {
        Self {
            layout,
            prelude: prelude.unwrap_or_default(),
            writer: None,
            file_index: 0,
            bytes_in_file: 0,
            files_written: 0,
        }
    }

    fn open_next(&mut self) -> Result<&mut BufWriter<File>> {
        self.file_index += 1;
        let path = self.layout.path_for(self.file_index);
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .with_context(|| format!("failed to create {}", parent.display()))?;
        }
        eprintln!("writing to {}", path.display());
        let file = File::create(&path).with_context(|| format!("failed to create {}", path.display()))?;
        let mut writer = BufWriter::with_capacity(1 << 20, file);
        writer.write_all(&self.prelude)?;
        self.bytes_in_file = self.prelude.len() as u64;
        self.files_written += 1;
        Ok(self.writer.insert(writer))
    }

    fn close_current(&mut self) -> Result<()> {
        if let Some(mut writer) = self.writer.take() {
            writer.flush().context("failed to flush an output file")?;
        }
        Ok(())
    }
}

#[async_trait]
impl Sink for FileSink {
    async fn write(&mut self, message: &Encoded) -> Result<()> {
        if self.writer.is_none() {
            self.open_next()?;
        }
        let writer = self.writer.as_mut().expect("opened above");
        writer.write_all(&message.payload).context("failed to write an output file")?;
        self.bytes_in_file += message.payload.len() as u64;
        if self.layout.file_size.is_some_and(|limit| self.bytes_in_file >= limit) {
            self.close_current()?;
        }
        Ok(())
    }

    async fn flush(&mut self) -> Result<()> {
        if let Some(writer) = self.writer.as_mut() {
            writer.flush().context("failed to flush an output file")?;
        }
        Ok(())
    }

    async fn finish(&mut self) -> Result<()> {
        if self.writer.is_none() && self.files_written == 0 {
            // No rows at all: still leave one well-formed file behind.
            self.open_next()?;
        }
        self.close_current()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn rotates_by_size_into_numbered_folders() {
        let dir = std::env::temp_dir().join(format!("datagen-filesink-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let layout = FileLayout {
            directory: dir.clone(),
            file_name_prefix: "part-".into(),
            folder_prefix: "batch-".into(),
            files_per_folder: 3,
            file_size: Some(50),
            extension: "csv".into(),
            writer: None,
        };
        let mut sink = FileSink::new(layout, Some(b"h\n".to_vec()));
        // 20 rows of 10 bytes: 5 rows fill 50 bytes, so 4 files.
        for index in 0..20 {
            sink.write(&Encoded { key: None, payload: format!("row-{:05}\n", index).into_bytes() }).await.unwrap();
        }
        sink.finish().await.unwrap();
        let mut files: Vec<PathBuf> = walkdir(&dir);
        files.sort();
        let names: Vec<String> = files
            .iter()
            .map(|path| path.strip_prefix(&dir).unwrap().to_string_lossy().replace('\\', "/"))
            .collect();
        assert_eq!(
            names,
            [
                "batch-00001/part-000001.csv",
                "batch-00001/part-000002.csv",
                "batch-00001/part-000003.csv",
                "batch-00002/part-000004.csv",
            ]
        );
        let mut rows = 0;
        for file in &files {
            let text = std::fs::read_to_string(file).unwrap();
            assert!(text.starts_with("h\n"), "every file starts with the prelude");
            rows += text.lines().count() - 1;
        }
        assert_eq!(rows, 20);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn no_folders_when_files_per_folder_is_zero() {
        let layout = FileLayout {
            directory: PathBuf::from("out"),
            file_name_prefix: "p-".into(),
            folder_prefix: "b-".into(),
            files_per_folder: 0,
            file_size: None,
            extension: "json".into(),
            writer: None,
        };
        assert_eq!(layout.path_for(7), PathBuf::from("out/p-000007.json"));
        let tagged = FileLayout { writer: Some(3), ..layout };
        assert_eq!(tagged.path_for(7), PathBuf::from("out/p-w03-000007.json"));
    }

    fn walkdir(dir: &std::path::Path) -> Vec<PathBuf> {
        let mut out = Vec::new();
        for entry in std::fs::read_dir(dir).unwrap() {
            let path = entry.unwrap().path();
            if path.is_dir() {
                out.extend(walkdir(&path));
            } else {
                out.push(path);
            }
        }
        out
    }
}
