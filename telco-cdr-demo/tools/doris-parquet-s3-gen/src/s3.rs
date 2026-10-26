//! Destination configuration: where the Parquet files go.
//!
//! Loaded from the TOML given with `--config`. `[dest] type` names either an
//! S3 bucket, described by `[s3]`, or a local directory, described by
//! `[local]`. Unknown keys are rejected so a typo fails at startup rather
//! than silently using a default, and every S3 value is validated against
//! the limits S3 actually enforces.

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{bail, Context, Result};
use object_store::aws::AmazonS3Builder;
use object_store::{BackoffConfig, Certificate, ClientOptions, ObjectStore, RetryConfig};
use serde::Deserialize;

/// S3 requires every part except the last to be at least this large.
const MIN_PART_SIZE: u64 = 5 << 20;
/// S3 caps a single part at 5 GiB.
const MAX_PART_SIZE: u64 = 5 << 30;
/// S3 allows at most 10,000 parts per multipart upload.
const MAX_PARTS_PER_OBJECT: u64 = 10_000;

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OutputConfig {
    pub dest: DestSection,
    #[serde(default)]
    pub s3: Option<S3Section>,
    #[serde(default)]
    pub local: Option<LocalSection>,
    #[serde(default)]
    pub layout: LayoutSection,
    #[serde(default)]
    pub run: RunSection,
    #[serde(default)]
    pub upload: UploadSection,
    #[serde(default)]
    pub parquet: ParquetSection,
}

/// Which destination the rest of the file describes.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DestSection {
    #[serde(rename = "type")]
    pub kind: DestKind,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum DestKind {
    S3,
    Local,
}

/// A directory on this machine. The same layout, rolling and Parquet
/// settings apply; only the upload section does not.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LocalSection {
    pub directory: PathBuf,
}

/// How files are arranged inside the destination, for either kind.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LayoutSection {
    /// Names this run's folder. Defaults to a UTC timestamp with a short
    /// random suffix, so a second run adds a sibling folder instead of
    /// overwriting the last one. Set it to pin the folder, or to "" to write
    /// straight into the prefix.
    #[serde(default)]
    pub run_id: Option<String>,
    /// Files per `batch-NNNNN/` folder inside the run folder, so each folder
    /// is a ready-made unit for one Doris load job. 0 puts every file
    /// directly in the run folder. `--files-per-folder` overrides it.
    #[serde(default = "default_files_per_folder")]
    pub files_per_folder: u64,
    /// Roll to a new object once a file passes this size. Files can only
    /// roll on a row group boundary, so each lands between this and this
    /// plus one row group.
    #[serde(default = "default_file_size", deserialize_with = "de_byte_size")]
    pub file_size: u64,
}

impl Default for LayoutSection {
    fn default() -> Self {
        Self {
            run_id: None,
            files_per_folder: default_files_per_folder(),
            file_size: default_file_size(),
        }
    }
}

/// How the run executes and when it stops. Threads and buffering size the
/// pipeline; the limits are the stop conditions, and `--rows`,
/// `--target-size` and `--time` override them for one invocation.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RunSection {
    /// Generator threads. 0 means the CPU count. A stateful generator in the
    /// spec forces 1 regardless.
    #[serde(default)]
    pub threads: usize,
    /// Upload workers draining the batch queue; each owns its own files.
    #[serde(default = "default_upload_threads")]
    pub upload_threads: usize,
    /// Batches that may wait between generation and upload.
    #[serde(default = "default_queue_depth")]
    pub queue_depth: usize,
    /// Rows per batch handed to the queue.
    #[serde(default = "default_batch_rows")]
    pub batch_rows: usize,
    /// Stop after this many rows. Exact.
    #[serde(default)]
    pub rows: Option<u64>,
    /// Stop once this much Parquet has been produced. Approximate, within a
    /// few percent. Exclusive with `rows`.
    #[serde(default, deserialize_with = "de_opt_byte_size")]
    pub target_size: Option<u64>,
    /// Stop after this long, whatever else is set.
    #[serde(default, deserialize_with = "de_opt_duration")]
    pub time: Option<Duration>,
}

impl Default for RunSection {
    fn default() -> Self {
        Self {
            threads: 0,
            upload_threads: default_upload_threads(),
            queue_depth: default_queue_depth(),
            batch_rows: default_batch_rows(),
            rows: None,
            target_size: None,
            time: None,
        }
    }
}

pub fn default_file_size() -> u64 {
    2 << 30
}
fn default_upload_threads() -> usize {
    8
}
fn default_queue_depth() -> usize {
    100
}
fn default_batch_rows() -> usize {
    32_768
}

/// TLS for the S3 endpoint.
#[derive(Debug, Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TlsSection {
    /// A PEM file of extra root certificates to trust, for an endpoint signed
    /// by a private CA. The system roots stay trusted too.
    #[serde(default)]
    pub ca_certificate: Option<PathBuf>,
    /// Off, and any certificate is accepted, so a self-signed endpoint works.
    /// That also means an impostor is accepted; leave it on where you can.
    #[serde(default = "default_true")]
    pub verify_certificates: bool,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct S3Section {
    pub bucket: String,
    /// Key prefix for generated objects. A trailing slash is added if missing.
    #[serde(default)]
    pub prefix: String,
    #[serde(default)]
    pub region: Option<String>,
    /// Set for MinIO, Ceph, or any S3-compatible endpoint.
    #[serde(default)]
    pub endpoint: Option<String>,
    #[serde(default)]
    pub tls: TlsSection,
    /// Path-style addressing (`endpoint/bucket/key`). MinIO and Ceph want
    /// this. Leave it off for AWS and for Alibaba OSS, which both address
    /// buckets as a subdomain (`bucket.endpoint/key`).
    #[serde(default)]
    pub path_style: bool,
    /// Permit a plain-HTTP endpoint. Off by default.
    #[serde(default)]
    pub allow_http: bool,
    #[serde(default)]
    pub credentials: Option<Credentials>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Credentials {
    pub access_key_id: String,
    pub secret_access_key: String,
    #[serde(default)]
    pub session_token: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct UploadSection {
    /// Multipart part size. Buffered in memory before each part is sent.
    #[serde(default = "default_part_size", deserialize_with = "de_byte_size")]
    pub part_size: u64,
    /// Parts uploaded concurrently per file.
    #[serde(default = "default_concurrency")]
    pub max_concurrent_parts: usize,
    /// Attempts after the first for one HTTP request.
    #[serde(default = "default_retries")]
    pub retries: u32,
    /// Deadline for a single HTTP request.
    #[serde(default = "default_timeout", deserialize_with = "de_duration")]
    pub timeout: Duration,
    /// Total wall clock a single request may spend being retried. This is the
    /// one that decides whether a run survives an outage: retries stop at
    /// whichever of this and `retries` comes first, so a generous count with a
    /// short window still gives up in a couple of minutes.
    #[serde(default = "default_retry_timeout", deserialize_with = "de_duration")]
    pub retry_timeout: Duration,
    /// Ceiling on the exponential backoff between attempts.
    #[serde(default = "default_max_backoff", deserialize_with = "de_duration")]
    pub max_backoff: Duration,
    /// Files a writer may lose to upload failures before the run gives up.
    /// Past every HTTP-level retry, a writer abandons the file it was building
    /// and starts a new one rather than taking the whole run down; the rows in
    /// that file are lost. Zero restores the old behaviour of failing the run.
    #[serde(default = "default_file_retries")]
    pub file_retries: u32,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ParquetSection {
    /// zstd, snappy, gzip, lz4, or none.
    #[serde(default = "default_compression")]
    pub compression: String,
    /// Codec effort. zstd accepts 1-22, gzip 0-9; ignored by the others.
    /// Higher costs CPU and produces fewer bytes to upload, which is usually
    /// the right trade when the network is the bottleneck.
    #[serde(default = "default_compression_level")]
    pub compression_level: Option<i32>,
    /// Rows per row group. A file can only roll on a row group boundary.
    #[serde(default = "default_row_group_rows")]
    pub row_group_rows: usize,
    /// Dictionary encoding for repeated string values.
    #[serde(default = "default_true")]
    pub dictionary: bool,
}

fn default_part_size() -> u64 {
    10 << 20
}
fn default_concurrency() -> usize {
    8
}
fn default_retries() -> u32 {
    10
}
fn default_timeout() -> Duration {
    Duration::from_secs(60)
}
/// object_store defaults this to three minutes, which is short for a run
/// measured in days: a storage service having a bad ten minutes should not
/// cost the whole job.
fn default_retry_timeout() -> Duration {
    Duration::from_secs(15 * 60)
}
fn default_max_backoff() -> Duration {
    Duration::from_secs(60)
}
fn default_file_retries() -> u32 {
    8
}
pub fn default_files_per_folder() -> u64 {
    100
}
fn default_compression() -> String {
    "zstd".to_string()
}
fn default_compression_level() -> Option<i32> {
    Some(3)
}
fn default_row_group_rows() -> usize {
    200_000
}
fn default_true() -> bool {
    true
}

impl Default for UploadSection {
    fn default() -> Self {
        Self {
            part_size: default_part_size(),
            max_concurrent_parts: default_concurrency(),
            retries: default_retries(),
            timeout: default_timeout(),
            retry_timeout: default_retry_timeout(),
            max_backoff: default_max_backoff(),
            file_retries: default_file_retries(),
        }
    }
}

impl Default for ParquetSection {
    fn default() -> Self {
        Self {
            compression: default_compression(),
            compression_level: default_compression_level(),
            row_group_rows: default_row_group_rows(),
            dictionary: default_true(),
        }
    }
}

fn de_byte_size<'de, D>(deserializer: D) -> std::result::Result<u64, D::Error>
where
    D: serde::Deserializer<'de>,
{
    use serde::de::Error as _;
    let raw = SizeOrInt::deserialize(deserializer)?;
    match raw {
        SizeOrInt::Int(value) => Ok(value),
        SizeOrInt::Text(text) => datagen::units::parse_byte_size(&text).map_err(D::Error::custom),
    }
}

fn de_duration<'de, D>(deserializer: D) -> std::result::Result<Duration, D::Error>
where
    D: serde::Deserializer<'de>,
{
    use serde::de::Error as _;
    let raw = SizeOrInt::deserialize(deserializer)?;
    match raw {
        SizeOrInt::Int(value) => Ok(Duration::from_secs(value)),
        SizeOrInt::Text(text) => datagen::units::parse_duration(&text).map_err(D::Error::custom),
    }
}

fn de_opt_byte_size<'de, D>(deserializer: D) -> std::result::Result<Option<u64>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    use serde::de::Error as _;
    Ok(match Option::<SizeOrInt>::deserialize(deserializer)? {
        None => None,
        Some(SizeOrInt::Int(value)) => Some(value),
        Some(SizeOrInt::Text(text)) => {
            Some(datagen::units::parse_byte_size(&text).map_err(D::Error::custom)?)
        }
    })
}

fn de_opt_duration<'de, D>(deserializer: D) -> std::result::Result<Option<Duration>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    use serde::de::Error as _;
    Ok(match Option::<SizeOrInt>::deserialize(deserializer)? {
        None => None,
        Some(SizeOrInt::Int(value)) => Some(Duration::from_secs(value)),
        Some(SizeOrInt::Text(text)) => {
            Some(datagen::units::parse_duration(&text).map_err(D::Error::custom)?)
        }
    })
}

#[derive(Deserialize)]
#[serde(untagged)]
enum SizeOrInt {
    Int(u64),
    Text(String),
}

pub fn load(path: &Path) -> Result<OutputConfig> {
    let text = std::fs::read_to_string(path)
        .with_context(|| format!("failed to read config {}", path.display()))?;
    let config: OutputConfig = toml::from_str(&text)
        .with_context(|| format!("failed to parse config {}", path.display()))?;
    config
        .validate()
        .with_context(|| format!("invalid config {}", path.display()))?;
    Ok(config)
}

impl OutputConfig {
    /// A config for a local directory with every other setting at default,
    /// which is what `--out-dir` means.
    pub fn local(directory: PathBuf) -> Self {
        Self {
            dest: DestSection { kind: DestKind::Local },
            s3: None,
            local: Some(LocalSection { directory }),
            layout: LayoutSection::default(),
            run: RunSection::default(),
            upload: UploadSection::default(),
            parquet: ParquetSection::default(),
        }
    }

    pub fn is_s3(&self) -> bool {
        self.dest.kind == DestKind::S3
    }

    /// The S3 section, for callers that only make sense against a bucket.
    pub fn s3(&self) -> Result<&S3Section> {
        self.s3
            .as_ref()
            .ok_or_else(|| anyhow::anyhow!("this config's destination is a local directory, not S3"))
    }

    pub fn local_directory(&self) -> Result<&Path> {
        self.local
            .as_ref()
            .map(|local| local.directory.as_path())
            .ok_or_else(|| anyhow::anyhow!("this config's destination is S3, not a local directory"))
    }

    pub fn validate(&self) -> Result<()> {
        match self.dest.kind {
            DestKind::S3 => {
                if self.s3.is_none() {
                    bail!("dest.type is \"s3\" but there is no [s3] section");
                }
                if self.local.is_some() {
                    bail!("dest.type is \"s3\"; remove the [local] section or change the type");
                }
                self.validate_s3()?;
            }
            DestKind::Local => {
                let Some(local) = &self.local else {
                    bail!("dest.type is \"local\" but there is no [local] section");
                };
                if self.s3.is_some() {
                    bail!("dest.type is \"local\"; remove the [s3] section or change the type");
                }
                if local.directory.as_os_str().is_empty() {
                    bail!("local.directory must not be empty");
                }
            }
        }
        self.validate_common()
    }

    fn validate_s3(&self) -> Result<()> {
        let s3 = self.s3()?;
        if s3.bucket.trim().is_empty() {
            bail!("s3.bucket must not be empty");
        }
        if s3.endpoint.is_none() && s3.region.is_none() {
            bail!("set s3.region, or s3.endpoint for an S3-compatible server");
        }
        if let Some(endpoint) = &s3.endpoint {
            if endpoint.starts_with("http://") && !s3.allow_http {
                bail!(
                    "s3.endpoint `{}` is plain HTTP; set s3.allow_http = true to permit it",
                    endpoint
                );
            }
            if !endpoint.starts_with("http://") && !endpoint.starts_with("https://") {
                bail!("s3.endpoint `{}` must start with http:// or https://", endpoint);
            }
        }
        if let Some(path) = &s3.tls.ca_certificate {
            // Read and parse it now, so a wrong path fails here and not on
            // the first upload.
            load_ca_bundle(path)?;
        }
        Ok(())
    }

    fn validate_common(&self) -> Result<()> {
        if self.run.rows.is_some() && self.run.target_size.is_some() {
            bail!("run.rows and run.target_size are exclusive; set one stop condition");
        }
        if self.run.upload_threads == 0 {
            bail!("run.upload_threads must be at least 1");
        }
        if self.run.queue_depth == 0 {
            bail!("run.queue_depth must be at least 1");
        }
        if self.run.batch_rows == 0 {
            bail!("run.batch_rows must be at least 1");
        }
        if self.layout.file_size == 0 {
            bail!("layout.file_size must be greater than 0");
        }
        if self.is_s3() {
            self.check_file_cap(self.layout.file_size)?;
        }
        if self.upload.part_size < MIN_PART_SIZE {
            bail!(
                "upload.part_size {} is below the S3 minimum of 5MiB",
                self.upload.part_size
            );
        }
        if self.upload.part_size > MAX_PART_SIZE {
            bail!(
                "upload.part_size {} exceeds the S3 maximum of 5GiB",
                self.upload.part_size
            );
        }
        if self.upload.max_concurrent_parts == 0 {
            bail!("upload.max_concurrent_parts must be at least 1");
        }
        if self.parquet.row_group_rows == 0 {
            bail!("parquet.row_group_rows must be at least 1");
        }
        const CODECS: [&str; 6] = ["zstd", "snappy", "gzip", "lz4", "lz4_raw", "none"];
        let codec = self.parquet.compression.to_ascii_lowercase();
        if !CODECS.contains(&codec.as_str()) {
            bail!(
                "parquet.compression `{}` is not one of: {}",
                self.parquet.compression,
                CODECS.join(", ")
            );
        }
        if let Some(level) = self.parquet.compression_level {
            let range = match codec.as_str() {
                "zstd" => Some((1, 22)),
                "gzip" => Some((0, 9)),
                _ => None,
            };
            match range {
                Some((low, high)) if level < low || level > high => bail!(
                    "parquet.compression_level {} is outside the {} range of {}..={}",
                    level,
                    codec,
                    low,
                    high
                ),
                _ => {}
            }
        }
        Ok(())
    }

    /// Largest file this config can produce, given the S3 part-count ceiling.
    pub fn max_file_bytes(&self) -> u64 {
        self.upload.part_size.saturating_mul(MAX_PARTS_PER_OBJECT)
    }

    /// Reject a per-file cap that would need more than 10,000 parts.
    pub fn check_file_cap(&self, cap: u64) -> Result<()> {
        // A local directory has no part limit.
        if !self.is_s3() {
            return Ok(());
        }
        let limit = self.max_file_bytes();
        if cap > limit {
            bail!(
                "a file cap of {} bytes needs more than {} parts at upload.part_size {}; \
                 raise upload.part_size or lower the cap",
                cap,
                MAX_PARTS_PER_OBJECT,
                self.upload.part_size
            );
        }
        Ok(())
    }

    /// Peak upload buffering per writer thread.
    pub fn buffer_bytes_per_thread(&self) -> u64 {
        self.upload
            .part_size
            .saturating_mul(self.upload.max_concurrent_parts as u64)
    }

    /// Object key prefix, normalised to end with a slash when non-empty.
    pub fn normalised_prefix(&self) -> String {
        let Some(s3) = &self.s3 else {
            return String::new();
        };
        let trimmed = s3.prefix.trim().trim_start_matches('/');
        if trimmed.is_empty() {
            String::new()
        } else if trimmed.ends_with('/') {
            trimmed.to_string()
        } else {
            format!("{}/", trimmed)
        }
    }
}


impl OutputConfig {
    /// Build the object store this config describes, and the key prefix every
    /// object goes under. Lives here rather than in `sink` so a tool that only
    /// needs to talk to the bucket does not pull in arrow and parquet.
    pub fn build_object_store(&self) -> Result<(Arc<dyn ObjectStore>, String)> {
        let s3 = self.s3()?;
        // from_env picks up AWS_ACCESS_KEY_ID and friends; explicit
        // credentials in the config file override it.
        let mut builder = AmazonS3Builder::from_env().with_bucket_name(&s3.bucket);
        if let Some(region) = &s3.region {
            builder = builder.with_region(region);
        }
        if let Some(endpoint) = &s3.endpoint {
            // object_store never rewrites the host: with virtual-hosted style
            // it uses the endpoint verbatim and appends the key, so an endpoint
            // without the bucket addresses the wrong object.
            let endpoint = if s3.path_style {
                endpoint.clone()
            } else {
                virtual_hosted_endpoint(endpoint, &s3.bucket)
            };
            builder = builder.with_endpoint(endpoint);
        }
        if s3.allow_http {
            builder = builder.with_allow_http(true);
        }
        let mut client = ClientOptions::new().with_timeout(self.upload.timeout);
        if let Some(path) = &s3.tls.ca_certificate {
            for certificate in load_ca_bundle(path)? {
                client = client.with_root_certificate(certificate);
            }
        }
        if !s3.tls.verify_certificates {
            client = client.with_allow_invalid_certificates(true);
        }
        builder = builder
            .with_retry(RetryConfig {
                backoff: BackoffConfig {
                    init_backoff: Duration::from_millis(100),
                    max_backoff: self.upload.max_backoff,
                    base: 2.0,
                },
                max_retries: self.upload.retries as usize,
                retry_timeout: self.upload.retry_timeout,
            })
            .with_client_options(client);
        // Always set this explicitly. object_store defaults to path style, so
        // only ever passing `false` left virtual-hosted style unreachable,
        // which S3-compatible services such as Alibaba OSS require.
        builder = builder.with_virtual_hosted_style_request(!s3.path_style);
        if let Some(credentials) = &s3.credentials {
            builder = builder
                .with_access_key_id(&credentials.access_key_id)
                .with_secret_access_key(&credentials.secret_access_key);
            if let Some(token) = &credentials.session_token {
                if !token.is_empty() {
                    builder = builder.with_token(token);
                }
            }
        }
        let store = builder
            .build()
            .context("failed to construct the S3 client; check bucket, region and credentials")?;
        Ok((Arc::new(store), self.normalised_prefix()))
    }
}

/// Read every certificate in a PEM bundle.
fn load_ca_bundle(path: &Path) -> Result<Vec<Certificate>> {
    let pem = std::fs::read(path)
        .with_context(|| format!("failed to read s3.tls.ca_certificate {}", path.display()))?;
    let certificates = Certificate::from_pem_bundle(&pem)
        .with_context(|| format!("{} is not a PEM certificate bundle", path.display()))?;
    if certificates.is_empty() {
        bail!("{} holds no certificates", path.display());
    }
    Ok(certificates)
}

/// Put the bucket in front of the endpoint host, the way virtual-hosted
/// addressing wants it: `https://oss-cn-beijing.aliyuncs.com` with bucket
/// `zyk-bj` becomes `https://zyk-bj.oss-cn-beijing.aliyuncs.com`. An endpoint
/// that already names the bucket is left alone, so both spellings work.
fn virtual_hosted_endpoint(endpoint: &str, bucket: &str) -> String {
    let trimmed = endpoint.trim_end_matches('/');
    let (scheme, rest) = match trimmed.split_once("://") {
        Some(split) => split,
        // Validation in s3.rs rejects a scheme-less endpoint, so this only
        // guards against a caller that skipped it.
        None => return trimmed.to_string(),
    };
    if rest.starts_with(&format!("{}.", bucket)) {
        return trimmed.to_string();
    }
    format!("{}://{}.{}", scheme, bucket, rest)
}
/// A commented starter config, written by `--emit-config`.
pub const SAMPLE_TOML: &str = r#"# Where the Parquet files go. Written by --emit-config; pass it back with
# --config. Unknown keys are rejected, so a typo fails at startup rather than
# being silently ignored.

[dest]
# "s3" uses the [s3] section below; "local" uses [local].
type = "s3"

[s3]
bucket = "my-bucket"

# Key prefix for generated objects. Files land at
# <prefix><run_id>/batch-NNNNN/part-wNN-NNNNNN.parquet.
prefix = "doris/my_table/"

# Region for real AWS S3.
region = "ap-southeast-1"

# For any S3-compatible server, set endpoint instead of relying on region.
# MinIO and Ceph address buckets by path; AWS and Alibaba OSS use a
# subdomain, so leave path_style off for those.
# endpoint = "https://minio.internal:9000"
# path_style = true

# Plain-HTTP endpoints are refused unless this is set.
# allow_http = false

# Credentials are optional. Omit this whole section to use the standard
# AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY environment variables, an
# instance profile, or any other default AWS credential source.
# Keep this file at mode 600 if you do put keys in it.
# [s3.credentials]
# access_key_id = "AKIA..."
# secret_access_key = "..."
# session_token = ""

# TLS for the endpoint. The system roots are always trusted; add a PEM
# bundle for an endpoint signed by a private CA. Turning verification off
# accepts any certificate, impostors included, so it is for test servers.
# [s3.tls]
# ca_certificate = "/etc/ssl/private-ca.pem"
# verify_certificates = true

# A local directory instead. Everything below [layout] still applies;
# [upload] does not.
# [local]
# directory = "/data/doris-out"

[layout]
# The run id names a folder of its own. It defaults to a UTC timestamp with a
# random suffix, so restarting adds a sibling folder rather than overwriting
# the last run. Pin it to make the folder predictable, or set it to "" to
# write straight into the prefix.
# run_id = "run01"

# Roll to a new object once a file passes this size. Files roll on a row
# group boundary, so each lands between this and this plus one row group.
file_size = "2GiB"

# Files per batch folder. Each batch-NNNNN/ folder is a ready-made unit for
# one Doris load job, holding about files_per_folder x file_size of data.
# 0 puts every file directly in the run folder.
files_per_folder = 100

[run]
# Generator threads; 0 means the CPU count. A stateful generator in the spec
# (sequence, fluctuating, javascript without parallel) forces one thread.
threads = 0

# Upload workers, each owning its own files, and the queue between the two
# pools: queue_depth batches of batch_rows rows. A full queue is normal on a
# slow link; it is what lets generation wait for the network.
upload_threads = 8
queue_depth = 100
batch_rows = 32768

# When to stop. rows is exact; target_size is within a few percent. Set one
# or the other, and optionally a time limit on top. Any of the three can be
# overridden for a single run with --rows, --target-size or --time.
# rows = 100000000
target_size = "10GiB"
# time = "2h"

[upload]
# Multipart part size. Each part is buffered in memory before it is sent.
# S3 requires at least 5MiB and allows at most 10000 parts per object,
# so this also caps the largest file: 10MiB x 10000 is about 97GiB.
part_size = "10MiB"

# Parts in flight per file. Peak memory per writer thread is
# part_size x max_concurrent_parts, so 10MiB x 8 = 80MiB here.
max_concurrent_parts = 8

# Attempts after the first for one HTTP request, and the deadline for each.
retries = 10
timeout = "60s"

# Total wall clock one request may spend being retried, and the ceiling on the
# backoff between attempts. These are what carry a multi-day run through an
# outage; object_store's own default window is three minutes.
retry_timeout = "15m"
max_backoff = "60s"

# Past those retries, a writer abandons the file it was building and starts a
# new one instead of failing the run. The rows in the abandoned file are lost
# and the count is reported at the end. Set to 0 to fail the run instead.
file_retries = 8

[parquet]
# zstd, snappy, gzip, lz4, lz4_raw, or none. Doris reads all of these.
compression = "zstd"

# Codec effort: zstd 1-22, gzip 0-9. When the network is the bottleneck,
# spending CPU here is free speed, because it shrinks what has to be uploaded.
# Level 1 is the parquet crate's default and compresses poorly; 3 is zstd's
# own default. Try 6 or 9 on a slow link.
compression_level = 3

# Rows per row group. Each upload worker buffers a whole row group in memory
# before encoding it, so this multiplied by the worker count drives peak
# memory. It also sets how finely file sizes and size targets can be tracked.
row_group_rows = 200000

dictionary = true
"#;

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(text: &str) -> Result<OutputConfig> {
        let config: OutputConfig = toml::from_str(text)?;
        config.validate()?;
        Ok(config)
    }

    #[test]
    fn sample_config_is_valid() {
        let config = parse(SAMPLE_TOML).expect("shipped sample must parse and validate");
        assert_eq!(config.s3().unwrap().bucket, "my-bucket");
        assert_eq!(config.upload.part_size, 10 << 20);
        assert_eq!(config.upload.max_concurrent_parts, 8);
        assert_eq!(config.parquet.compression, "zstd");
        assert_eq!(config.parquet.compression_level, Some(3));
        assert_eq!(config.upload.timeout, Duration::from_secs(60));
        assert_eq!(config.buffer_bytes_per_thread(), 80 << 20);
        // 10MiB x 10000 parts, which is about 97GiB rather than a round 100GiB.
        assert_eq!(config.max_file_bytes(), 10_485_760 * 10_000);
    }

    #[test]
    fn minimal_config_fills_in_defaults() {
        let config = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket = \"b\"\nregion = \"us-east-1\"\n").expect("parse");
        assert_eq!(config.upload.part_size, 10 << 20);
        assert_eq!(config.parquet.row_group_rows, 200_000);
        assert!(config.parquet.dictionary);
        assert_eq!(config.normalised_prefix(), "");
    }

    #[test]
    fn rejects_unknown_keys() {
        let err = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket = \"b\"\nregion = \"r\"\nbuckett = \"typo\"\n")
            .expect_err("typo must fail");
        assert!(err.to_string().contains("buckett"), "unhelpful error: {}", err);
    }

    #[test]
    fn enforces_s3_part_size_limits() {
        let too_small = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[upload]\npart_size=\"1MiB\"\n")
            .expect_err("below 5MiB must fail");
        assert!(too_small.to_string().contains("5MiB"), "{}", too_small);

        let too_big = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[upload]\npart_size=\"6GiB\"\n")
            .expect_err("above 5GiB must fail");
        assert!(too_big.to_string().contains("5GiB"), "{}", too_big);

        assert!(parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[upload]\npart_size=\"5MiB\"\n").is_ok());
    }

    #[test]
    fn file_cap_must_fit_in_ten_thousand_parts() {
        let config = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n").expect("parse");
        // 2GiB at 10MiB parts is about 205 parts.
        assert!(config.check_file_cap(2 << 30).is_ok());
        let err = config.check_file_cap(200 << 30).expect_err("200GiB needs too many parts");
        assert!(err.to_string().contains("10000"), "{}", err);
    }

    /// These settings were parsed and then never applied, so the run silently
    /// used object_store's three-minute retry window. Pin the values and the
    /// defaults now that they reach the client.
    #[test]
    fn upload_retry_settings_have_long_run_defaults() {
        let config = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket = \"b\"\nregion = \"r\"\n").expect("parse");
        assert_eq!(config.upload.retries, 10);
        assert_eq!(config.upload.timeout, Duration::from_secs(60));
        assert_eq!(config.upload.retry_timeout, Duration::from_secs(15 * 60));
        assert_eq!(config.upload.max_backoff, Duration::from_secs(60));
        assert_eq!(config.upload.file_retries, 8);
    }

    #[test]
    fn upload_retry_settings_can_be_overridden() {
        let config = parse(
            "[dest]\ntype=\"s3\"\n[s3]\nbucket = \"b\"\nregion = \"r\"\n[upload]\nretries = 25\n\
             retry_timeout = \"1h\"\nmax_backoff = \"2m\"\nfile_retries = 0\n",
        )
        .expect("parse");
        assert_eq!(config.upload.retries, 25);
        assert_eq!(config.upload.retry_timeout, Duration::from_secs(3600));
        assert_eq!(config.upload.max_backoff, Duration::from_secs(120));
        // Zero is meaningful: fail the run on the first unrecoverable upload.
        assert_eq!(config.upload.file_retries, 0);
    }

    #[test]
    fn puts_the_bucket_in_the_endpoint_host() {
        assert_eq!(
            virtual_hosted_endpoint("https://oss-cn-beijing-internal.aliyuncs.com", "zyk-bj"),
            "https://zyk-bj.oss-cn-beijing-internal.aliyuncs.com"
        );
        // A trailing slash must not end up inside the host.
        assert_eq!(
            virtual_hosted_endpoint("https://oss-cn-beijing.aliyuncs.com/", "zyk-bj"),
            "https://zyk-bj.oss-cn-beijing.aliyuncs.com"
        );
        // Already spelled out, so leave it alone rather than double it up.
        assert_eq!(
            virtual_hosted_endpoint("https://zyk-bj.oss-cn-beijing.aliyuncs.com", "zyk-bj"),
            "https://zyk-bj.oss-cn-beijing.aliyuncs.com"
        );
    }

    #[test]
    fn guards_plain_http_endpoints() {
        let refused = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nendpoint=\"http://minio:9000\"\n")
            .expect_err("plain http must be opt-in");
        assert!(refused.to_string().contains("allow_http"), "{}", refused);

        assert!(parse(
            "[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nendpoint=\"http://minio:9000\"\nallow_http=true\n"
        )
        .is_ok());

        let bad_scheme = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nendpoint=\"minio:9000\"\n")
            .expect_err("scheme is required");
        assert!(bad_scheme.to_string().contains("http"), "{}", bad_scheme);
    }

    #[test]
    fn requires_a_region_or_an_endpoint() {
        let err = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\n").expect_err("no region and no endpoint");
        assert!(err.to_string().contains("region"), "{}", err);
    }

    #[test]
    fn rejects_unknown_compression() {
        let err = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[parquet]\ncompression=\"brotli\"\n")
            .expect_err("brotli is not offered");
        assert!(err.to_string().contains("zstd"), "{}", err);
    }

    #[test]
    fn normalises_the_key_prefix() {
        let with_slash = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\nprefix=\"a/b/\"\n").unwrap();
        assert_eq!(with_slash.normalised_prefix(), "a/b/");

        let without = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\nprefix=\"a/b\"\n").unwrap();
        assert_eq!(without.normalised_prefix(), "a/b/");

        let leading = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\nprefix=\"/a/b\"\n").unwrap();
        assert_eq!(leading.normalised_prefix(), "a/b/", "leading slash is dropped");
    }

    #[test]
    fn accepts_credentials_when_given() {
        let config = parse(
            "[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[s3.credentials]\naccess_key_id=\"A\"\nsecret_access_key=\"S\"\n",
        )
        .expect("parse");
        let credentials = config.s3().unwrap().credentials.clone().expect("credentials present");
        assert_eq!(credentials.access_key_id, "A");
        assert_eq!(credentials.session_token, None);
    }

    #[test]
    fn validates_compression_level_against_the_codec() {
        let ok = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[parquet]\ncompression=\"zstd\"\ncompression_level=19\n");
        assert!(ok.is_ok(), "zstd accepts 19");

        let too_high = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[parquet]\ncompression=\"zstd\"\ncompression_level=23\n")
            .expect_err("zstd stops at 22");
        assert!(too_high.to_string().contains("1..=22"), "{}", too_high);

        let gzip_range = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[parquet]\ncompression=\"gzip\"\ncompression_level=12\n")
            .expect_err("gzip stops at 9");
        assert!(gzip_range.to_string().contains("0..=9"), "{}", gzip_range);

        // Codecs without a level simply ignore it.
        assert!(parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[parquet]\ncompression=\"snappy\"\ncompression_level=5\n").is_ok());
    }

    #[test]
    fn the_destination_type_must_match_its_section() {
        let local = parse("[dest]\ntype=\"local\"\n[local]\ndirectory=\"/tmp/out\"\n").unwrap();
        assert!(!local.is_s3());
        assert_eq!(local.local_directory().unwrap(), Path::new("/tmp/out"));
        assert!(local.s3().is_err());
        assert_eq!(local.layout.files_per_folder, 100);
        assert!(local.check_file_cap(u64::MAX).is_ok(), "no part limit on a directory");

        let mismatch = parse("[dest]\ntype=\"s3\"\n[local]\ndirectory=\"/tmp\"\n").expect_err("s3 without [s3]");
        assert!(mismatch.to_string().contains("[s3]"), "{}", mismatch);
        let both = parse("[dest]\ntype=\"local\"\n[local]\ndirectory=\"/tmp\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n")
            .expect_err("two destinations");
        assert!(both.to_string().contains("remove"), "{}", both);
        let no_type = "[s3]\nbucket=\"b\"\nregion=\"r\"\n";
        assert!(parse(no_type).is_err(), "dest.type is required");
        assert!(parse("[dest]\ntype=\"ftp\"\n").is_err());

        let shorthand = OutputConfig::local(PathBuf::from("./out"));
        shorthand.validate().unwrap();
        assert_eq!(shorthand.normalised_prefix(), "");
    }

    #[test]
    fn layout_and_tls_are_read_and_checked() {
        let config = parse(
            "[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[s3.tls]\nverify_certificates=false\n[layout]\nrun_id=\"r1\"\nfiles_per_folder=7\n",
        )
        .unwrap();
        assert_eq!(config.layout.run_id.as_deref(), Some("r1"));
        assert_eq!(config.layout.files_per_folder, 7);
        assert!(!config.s3().unwrap().tls.verify_certificates);

        let missing = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[s3.tls]\nca_certificate=\"/no/such/ca.pem\"\n")
            .expect_err("a missing CA file fails at load");
        assert!(missing.to_string().contains("ca_certificate"), "{}", missing);

        let dir = std::env::temp_dir().join(format!("dpsg-ca-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let bad = dir.join("ca.pem");
        std::fs::write(&bad, "not a certificate").unwrap();
        let garbage = parse(&format!(
            "[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[s3.tls]\nca_certificate=\"{}\"\n",
            bad.display()
        ))
        .expect_err("garbage is not a bundle");
        assert!(garbage.to_string().contains("certificate"), "{}", garbage);
        std::fs::remove_dir_all(&dir).ok();

        // The old location for these keys is refused, not silently ignored.
        let old = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\nfiles_per_folder=5\n")
            .expect_err("moved to [layout]");
        assert!(old.to_string().contains("files_per_folder"), "{}", old);
    }

    /// The shipped examples must load, so they cannot drift from the schema.
    #[test]
    fn every_example_config_loads_and_validates() {
        let mut seen = 0;
        for entry in std::fs::read_dir("examples").expect("examples/ next to Cargo.toml") {
            let path = entry.unwrap().path();
            if path.extension().is_some_and(|ext| ext == "toml") {
                let config = load(&path).unwrap_or_else(|error| panic!("{}: {:#}", path.display(), error));
                // Both examples pin a run id and use batch folders.
                assert!(config.layout.run_id.is_some(), "{} should pin run_id", path.display());
                assert!(config.layout.files_per_folder > 0, "{} should use batch folders", path.display());
                seen += 1;
            }
        }
        assert!(seen >= 2, "expected the s3 and local example configs, found {}", seen);
    }

    #[test]
    fn run_section_defaults_and_limits() {
        let config = parse("[dest]\ntype=\"local\"\n[local]\ndirectory=\"/tmp/o\"\n").unwrap();
        assert_eq!(config.run.threads, 0, "0 means the CPU count");
        assert_eq!(config.run.upload_threads, 8);
        assert_eq!(config.run.queue_depth, 100);
        assert_eq!(config.run.batch_rows, 32_768);
        assert_eq!(config.layout.file_size, 2 << 30);
        assert!(config.run.rows.is_none() && config.run.target_size.is_none() && config.run.time.is_none());

        let set = parse(
            "[dest]\ntype=\"local\"\n[local]\ndirectory=\"/tmp/o\"\n[layout]\nfile_size=\"512MiB\"\n[run]\nthreads=4\ntarget_size=\"10GiB\"\ntime=\"90m\"\n",
        )
        .unwrap();
        assert_eq!(set.layout.file_size, 512 << 20);
        assert_eq!(set.run.threads, 4);
        assert_eq!(set.run.target_size, Some(10 << 30));
        assert_eq!(set.run.time, Some(Duration::from_secs(90 * 60)));

        let both = parse("[dest]\ntype=\"local\"\n[local]\ndirectory=\"/tmp/o\"\n[run]\nrows=5\ntarget_size=\"1GiB\"\n")
            .expect_err("two stop conditions");
        assert!(both.to_string().contains("exclusive"), "{}", both);
        assert!(parse("[dest]\ntype=\"local\"\n[local]\ndirectory=\"/tmp/o\"\n[run]\nupload_threads=0\n").is_err());
        // The S3 part ceiling now applies to the configured file size.
        let too_big = parse("[dest]\ntype=\"s3\"\n[s3]\nbucket=\"b\"\nregion=\"r\"\n[layout]\nfile_size=\"200GiB\"\n")
            .expect_err("more parts than S3 allows");
        assert!(too_big.to_string().contains("10000"), "{}", too_big);
    }
}
