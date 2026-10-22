//! Rows to objects in S3 (or any S3-compatible store), rotated and named
//! exactly like the file sink: `<prefix><folder_prefix>NNNNN/<file_name_prefix>NNNNNN.<ext>`.
//!
//! Each object is uploaded as multipart parts while it is being written,
//! so a 100 MiB object never sits whole in memory: only `part_size` times
//! the parts in flight.

use std::sync::Arc;
use std::time::Duration;

use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use object_store::aws::AmazonS3Builder;
use object_store::buffered::BufWriter;
use object_store::{BackoffConfig, ClientOptions, ObjectStore, RetryConfig};
use tokio::io::AsyncWriteExt;

use super::file::FileLayout;
use super::{Encoded, Sink};

/// How to reach the bucket.
#[derive(Debug, Clone)]
pub struct S3Options {
    pub bucket: String,
    /// Key prefix; a trailing slash is added when missing.
    pub prefix: String,
    pub region: Option<String>,
    /// For S3-compatible servers. MinIO and Ceph address buckets by path,
    /// AWS and Alibaba OSS by subdomain.
    pub endpoint: Option<String>,
    pub path_style: bool,
    pub allow_http: bool,
    pub access_key_id: Option<String>,
    pub secret_access_key: Option<String>,
    pub session_token: Option<String>,
    /// Multipart part size; S3 wants at least 5 MiB.
    pub part_size: usize,
    pub max_concurrent_parts: usize,
    pub retries: usize,
    pub timeout: Duration,
}

impl S3Options {
    /// The client. Credentials come from the options, else from the usual
    /// AWS environment and profile sources.
    pub fn build_store(&self) -> Result<Arc<dyn ObjectStore>> {
        if self.bucket.trim().is_empty() {
            bail!("s3.bucket is required");
        }
        let mut builder = AmazonS3Builder::from_env().with_bucket_name(&self.bucket);
        if let Some(region) = &self.region {
            builder = builder.with_region(region);
        }
        if let Some(endpoint) = &self.endpoint {
            // object_store never rewrites the host: with virtual-hosted
            // style it uses the endpoint verbatim and appends the key.
            let endpoint = if self.path_style {
                endpoint.clone()
            } else {
                virtual_hosted_endpoint(endpoint, &self.bucket)
            };
            builder = builder.with_endpoint(endpoint);
        }
        let client = ClientOptions::new()
            .with_timeout(self.timeout)
            .with_allow_http(self.allow_http);
        builder = builder
            .with_retry(RetryConfig {
                backoff: BackoffConfig {
                    init_backoff: Duration::from_millis(100),
                    max_backoff: Duration::from_secs(60),
                    base: 2.0,
                },
                max_retries: self.retries,
                retry_timeout: Duration::from_secs(15 * 60),
            })
            .with_client_options(client)
            .with_virtual_hosted_style_request(!self.path_style);
        if let (Some(key), Some(secret)) = (&self.access_key_id, &self.secret_access_key) {
            builder = builder.with_access_key_id(key).with_secret_access_key(secret);
            if let Some(token) = self.session_token.as_deref().filter(|t| !t.is_empty()) {
                builder = builder.with_token(token);
            }
        }
        let store = builder
            .build()
            .context("failed to construct the S3 client; check bucket, region and credentials")?;
        Ok(Arc::new(store))
    }

    pub fn normalised_prefix(&self) -> String {
        normalise_prefix(&self.prefix)
    }
}

fn normalise_prefix(prefix: &str) -> String {
    let trimmed = prefix.trim().trim_start_matches('/');
    if trimmed.is_empty() {
        String::new()
    } else if trimmed.ends_with('/') {
        trimmed.to_string()
    } else {
        format!("{}/", trimmed)
    }
}

fn virtual_hosted_endpoint(endpoint: &str, bucket: &str) -> String {
    let trimmed = endpoint.trim_end_matches('/');
    let Some((scheme, rest)) = trimmed.split_once("://") else {
        return trimmed.to_string();
    };
    if rest.starts_with(&format!("{}.", bucket)) {
        return trimmed.to_string();
    }
    format!("{}://{}.{}", scheme, bucket, rest)
}

/// Rotating objects in any [`ObjectStore`].
pub struct ObjectSink {
    store: Arc<dyn ObjectStore>,
    prefix: String,
    layout: FileLayout,
    prelude: Vec<u8>,
    part_size: usize,
    max_concurrent_parts: usize,
    writer: Option<BufWriter>,
    object_index: u64,
    bytes_in_object: u64,
    pub objects_written: u64,
}

impl ObjectSink {
    pub fn new(
        store: Arc<dyn ObjectStore>,
        prefix: &str,
        layout: FileLayout,
        prelude: Option<Vec<u8>>,
        part_size: usize,
        max_concurrent_parts: usize,
    ) -> Self {
        Self {
            store,
            prefix: normalise_prefix(prefix),
            layout,
            prelude: prelude.unwrap_or_default(),
            part_size: part_size.max(5 * 1024 * 1024),
            max_concurrent_parts: max_concurrent_parts.max(1),
            writer: None,
            object_index: 0,
            bytes_in_object: 0,
            objects_written: 0,
        }
    }

    /// The key of the `index`-th object: the layout's relative path under
    /// the prefix, with forward slashes.
    pub fn key_for(&self, index: u64) -> object_store::path::Path {
        let relative = self.layout.path_for(index);
        let relative = relative
            .strip_prefix(&self.layout.directory)
            .unwrap_or(&relative)
            .components()
            .map(|component| component.as_os_str().to_string_lossy().into_owned())
            .collect::<Vec<_>>()
            .join("/");
        object_store::path::Path::from(format!("{}{}", self.prefix, relative))
    }

    async fn open_next(&mut self) -> Result<()> {
        self.object_index += 1;
        let key = self.key_for(self.object_index);
        eprintln!("uploading to {}", key);
        let mut writer = BufWriter::with_capacity(self.store.clone(), key.clone(), self.part_size)
            .with_max_concurrency(self.max_concurrent_parts);
        writer
            .write_all(&self.prelude)
            .await
            .with_context(|| format!("failed to start uploading {}", key))?;
        self.bytes_in_object = self.prelude.len() as u64;
        self.objects_written += 1;
        self.writer = Some(writer);
        Ok(())
    }

    async fn close_current(&mut self) -> Result<()> {
        if let Some(mut writer) = self.writer.take() {
            // A failed shutdown cannot be aborted through the writer (it
            // is already in its flush state); the store's retries have
            // been spent by then, so report it.
            writer.shutdown().await.context("failed to complete an upload")?;
        }
        Ok(())
    }
}

#[async_trait]
impl Sink for ObjectSink {
    async fn write(&mut self, message: &Encoded) -> Result<()> {
        if self.writer.is_none() {
            self.open_next().await?;
        }
        let writer = self.writer.as_mut().expect("opened above");
        writer
            .write_all(&message.payload)
            .await
            .context("failed to upload a part")?;
        self.bytes_in_object += message.payload.len() as u64;
        if self.layout.file_size.is_some_and(|limit| self.bytes_in_object >= limit) {
            self.close_current().await?;
        }
        Ok(())
    }

    async fn flush(&mut self) -> Result<()> {
        // Parts go out as they fill; there is nothing to push early.
        Ok(())
    }

    async fn finish(&mut self) -> Result<()> {
        if self.writer.is_none() && self.objects_written == 0 {
            self.open_next().await?;
        }
        self.close_current().await
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use futures::TryStreamExt;
    use std::path::PathBuf;

    fn layout(file_size: Option<u64>, files_per_folder: u64, writer: Option<usize>) -> FileLayout {
        FileLayout {
            directory: PathBuf::from("."),
            file_name_prefix: "part-".into(),
            folder_prefix: "batch-".into(),
            files_per_folder,
            file_size,
            extension: "csv".into(),
            writer,
        }
    }

    #[test]
    fn keys_follow_the_file_layout_under_the_prefix() {
        let store: Arc<dyn ObjectStore> = Arc::new(object_store::memory::InMemory::new());
        let sink = ObjectSink::new(store, "/data/events", layout(None, 100, Some(2)), None, 0, 0);
        assert_eq!(sink.key_for(1).as_ref(), "data/events/batch-00001/part-w02-000001.csv");
        assert_eq!(sink.key_for(101).as_ref(), "data/events/batch-00002/part-w02-000101.csv");
        let flat = ObjectSink::new(Arc::new(object_store::memory::InMemory::new()), "", layout(None, 0, None), None, 0, 0);
        assert_eq!(flat.key_for(7).as_ref(), "part-000007.csv");
    }

    #[tokio::test]
    async fn rotates_objects_by_size_with_a_prelude_each() {
        let store: Arc<dyn ObjectStore> = Arc::new(object_store::memory::InMemory::new());
        let mut sink = ObjectSink::new(store.clone(), "run1", layout(Some(50), 2, None), Some(b"h\n".to_vec()), 0, 0);
        for index in 0..20 {
            sink.write(&Encoded { key: None, payload: format!("row-{:05}\n", index).into_bytes() }).await.unwrap();
        }
        sink.finish().await.unwrap();
        let mut objects: Vec<String> = store
            .list(None)
            .map_ok(|meta| meta.location.to_string())
            .try_collect()
            .await
            .unwrap();
        objects.sort();
        assert_eq!(
            objects,
            [
                "run1/batch-00001/part-000001.csv",
                "run1/batch-00001/part-000002.csv",
                "run1/batch-00002/part-000003.csv",
                "run1/batch-00002/part-000004.csv",
            ]
        );
        let mut rows = 0;
        for key in &objects {
            let bytes = store
                .get_opts(&object_store::path::Path::from(key.as_str()), object_store::GetOptions::default())
                .await
                .unwrap()
                .bytes()
                .await
                .unwrap();
            let text = String::from_utf8(bytes.to_vec()).unwrap();
            assert!(text.starts_with("h\n"), "{}", key);
            rows += text.lines().count() - 1;
        }
        assert_eq!(rows, 20);
    }

    #[test]
    fn endpoints_and_prefixes_are_normalised() {
        assert_eq!(virtual_hosted_endpoint("https://s3.example.com/", "b"), "https://b.s3.example.com");
        assert_eq!(virtual_hosted_endpoint("https://b.s3.example.com", "b"), "https://b.s3.example.com");
        assert_eq!(normalise_prefix("/a/b"), "a/b/");
        assert_eq!(normalise_prefix("  "), "");
    }
}
