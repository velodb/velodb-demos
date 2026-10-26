//! The `config.toml` that drives the tool: what to generate, in which
//! format, at what speed, and where it goes.
//!
//! Unknown keys are rejected, so a typo fails at startup rather than being
//! silently ignored. Relative paths resolve against the config file's
//! directory.

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::time::Duration;

use anyhow::{bail, Context, Result};
use serde::{Deserialize, Deserializer};

use crate::units::{parse_byte_size, parse_duration};

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub generation: GenerationConfig,
    #[serde(default)]
    pub speed: SpeedConfig,
    #[serde(default)]
    pub threading: ThreadingConfig,
    #[serde(default)]
    pub parquet: ParquetConfig,
    pub sink: SinkConfig,
    #[serde(default)]
    pub console: ConsoleConfig,
    #[serde(default)]
    pub file: Option<FileConfig>,
    #[serde(default)]
    pub s3: Option<S3Config>,
    #[serde(default)]
    pub kafka: Option<KafkaConfig>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GenerationConfig {
    /// `.toml` for the path-keyed layout, `.yaml` for the list layout.
    pub spec: PathBuf,
    /// The record format: csv, json, avro, protobuf or parquet.
    #[serde(default = "default_schema_type")]
    pub schema_type: SchemaType,
    /// The `.avsc` or `.proto` file; avro and protobuf need it, csv and json
    /// take their columns from the spec's order. Parquet infers its types
    /// from the generators, or takes them from an `.avsc` when one is set.
    #[serde(default)]
    pub schema: Option<PathBuf>,
    /// The message to encode when the `.proto` defines more than one.
    #[serde(default)]
    pub proto_message: Option<String>,
    /// Stop after this many rows; unlimited when absent.
    #[serde(default)]
    pub rows: Option<u64>,
    /// Stop after this long, e.g. "30s", "5m", "1h30m".
    #[serde(default, deserialize_with = "duration_opt")]
    pub time: Option<Duration>,
}

/// The record format. `avro` and `protobuf` are length-prefixed streams
/// in files and Confluent wire-format messages in Kafka; `csv` is a header
/// plus rows and never goes to Kafka; `parquet` goes to files and S3 only.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SchemaType {
    Csv,
    Json,
    Avro,
    #[serde(alias = "proto")]
    Protobuf,
    Parquet,
}

impl SchemaType {
    pub fn needs_schema(self) -> bool {
        matches!(self, SchemaType::Avro | SchemaType::Protobuf)
    }

    /// Binary formats: never to the console.
    pub fn is_binary(self) -> bool {
        matches!(self, SchemaType::Avro | SchemaType::Protobuf | SchemaType::Parquet)
    }

    pub fn default_extension(self) -> &'static str {
        match self {
            SchemaType::Csv => "csv",
            SchemaType::Json => "json",
            SchemaType::Avro => "avro",
            SchemaType::Protobuf => "proto",
            SchemaType::Parquet => "parquet",
        }
    }
}

/// Parquet writer settings, for `schema_type = "parquet"`.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ParquetConfig {
    /// Rows per row group. Each sink thread buffers a whole row group
    /// before encoding it.
    #[serde(default = "default_row_group_rows")]
    pub row_group_rows: usize,
    /// zstd, snappy, gzip, lz4 or none.
    #[serde(default = "default_compression")]
    pub compression: String,
    /// zstd 1-22, gzip 0-9; ignored by the others.
    #[serde(default = "default_compression_level")]
    pub compression_level: i32,
}

impl Default for ParquetConfig {
    fn default() -> Self {
        Self {
            row_group_rows: default_row_group_rows(),
            compression: default_compression(),
            compression_level: default_compression_level(),
        }
    }
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SpeedConfig {
    #[serde(default, deserialize_with = "byte_size_opt")]
    pub byte_rate_per_second: Option<u64>,
    #[serde(default)]
    pub row_rate_per_second: Option<u64>,
    /// Rows per generated batch. Overrides the spec's `batch.rows`.
    #[serde(default)]
    pub generator_batch_rows: Option<usize>,
    /// Batches that may wait between the generators and the sinks.
    #[serde(default = "default_queue_depth")]
    pub queue_depth: usize,
}

impl Default for SpeedConfig {
    fn default() -> Self {
        Self {
            byte_rate_per_second: None,
            row_rate_per_second: None,
            generator_batch_rows: None,
            queue_depth: default_queue_depth(),
        }
    }
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ThreadingConfig {
    /// Generator threads; the CPU count when absent. Stateful generators
    /// force one regardless.
    #[serde(default)]
    pub generator_threads: Option<usize>,
    /// Sink threads: parallel file writers or Kafka producers. The console
    /// always uses one.
    #[serde(default = "default_sink_threads")]
    pub sink_threads: usize,
}

impl Default for ThreadingConfig {
    fn default() -> Self {
        Self { generator_threads: None, sink_threads: default_sink_threads() }
    }
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SinkConfig {
    pub dest: Dest,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Dest {
    Console,
    File,
    S3,
    Kafka,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConsoleConfig {
    /// Write the CSV header line.
    #[serde(default = "default_true")]
    pub header: bool,
}

impl Default for ConsoleConfig {
    fn default() -> Self {
        Self { header: true }
    }
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileConfig {
    #[serde(default = "default_directory")]
    pub directory: PathBuf,
    /// Start a new file once the current one reaches this size.
    #[serde(default, deserialize_with = "byte_size_opt")]
    pub file_size: Option<u64>,
    #[serde(default = "default_file_name_prefix")]
    pub file_name_prefix: String,
    #[serde(default = "default_folder_prefix")]
    pub folder_prefix: String,
    /// Files per numbered folder; 0 puts every file in the directory.
    #[serde(default)]
    pub files_per_folder: u64,
    /// File extension; by default csv, json, avro or proto after the format.
    #[serde(default)]
    pub extension: Option<String>,
    #[serde(default = "default_true")]
    pub header: bool,
}

/// Objects in S3 or an S3-compatible store, laid out like the file sink:
/// `<prefix><folder_prefix>NNNNN/<file_name_prefix>NNNNNN.<ext>`.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct S3Config {
    pub bucket: String,
    /// Key prefix for every object, e.g. "events/run-01/".
    #[serde(default)]
    pub prefix: String,
    #[serde(default)]
    pub region: Option<String>,
    /// For S3-compatible servers; leave out for AWS.
    #[serde(default)]
    pub endpoint: Option<String>,
    /// Address buckets by path (MinIO, Ceph) rather than subdomain (AWS, OSS).
    #[serde(default)]
    pub path_style: bool,
    /// Plain-HTTP endpoints are refused unless this is set.
    #[serde(default)]
    pub allow_http: bool,
    /// Leave out to use AWS_ACCESS_KEY_ID and friends, a profile, or an
    /// instance role.
    #[serde(default)]
    pub credentials: Option<S3Credentials>,
    /// Multipart part size; S3 needs at least 5 MiB. Peak memory per sink
    /// thread is part_size x max_concurrent_parts.
    #[serde(default = "default_part_size", deserialize_with = "byte_size")]
    pub part_size: u64,
    #[serde(default = "default_max_concurrent_parts")]
    pub max_concurrent_parts: usize,
    /// Attempts after the first for one request, and the deadline for each.
    #[serde(default = "default_retries")]
    pub retries: usize,
    #[serde(default = "default_timeout", deserialize_with = "duration")]
    pub timeout: Duration,
    /// Start a new object once the current one reaches this size.
    #[serde(default, deserialize_with = "byte_size_opt")]
    pub file_size: Option<u64>,
    #[serde(default = "default_file_name_prefix")]
    pub file_name_prefix: String,
    #[serde(default = "default_folder_prefix")]
    pub folder_prefix: String,
    /// Objects per numbered folder; 0 puts every object under the prefix.
    #[serde(default)]
    pub files_per_folder: u64,
    #[serde(default)]
    pub extension: Option<String>,
    #[serde(default = "default_true")]
    pub header: bool,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct S3Credentials {
    pub access_key_id: String,
    pub secret_access_key: String,
    #[serde(default)]
    pub session_token: Option<String>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct KafkaConfig {
    pub brokers: String,
    pub topic: String,
    /// The field whose text becomes the message key.
    #[serde(default)]
    pub key_field: Option<String>,
    /// Messages awaiting acknowledgement at once, per sink thread.
    #[serde(default = "default_in_flight")]
    pub in_flight: usize,
    #[serde(default)]
    pub schema_registry: Option<RegistryConfig>,
    /// Extra librdkafka properties, verbatim: "compression.type",
    /// "security.protocol", "sasl.mechanism", ...
    #[serde(default)]
    pub properties: BTreeMap<String, String>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RegistryConfig {
    pub url: String,
    /// Defaults to `<topic>-value`.
    #[serde(default)]
    pub subject: Option<String>,
    #[serde(default)]
    pub username: Option<String>,
    #[serde(default)]
    pub password: Option<String>,
}

impl Config {
    pub fn parse(text: &str) -> Result<Self> {
        let config: Config = toml::from_str(text).map_err(|error| anyhow::anyhow!("invalid config: {}", error))?;
        config.validate()?;
        Ok(config)
    }

    /// Read, parse, validate, and resolve relative paths against the file's
    /// directory.
    pub fn load(path: &Path) -> Result<Self> {
        let text = std::fs::read_to_string(path)
            .with_context(|| format!("failed to read config {}", path.display()))?;
        let mut config = Self::parse(&text).with_context(|| format!("config {}", path.display()))?;
        let base = path.parent().filter(|p| !p.as_os_str().is_empty()).map(Path::to_path_buf);
        if let Some(base) = base {
            config.generation.spec = resolve(&base, &config.generation.spec);
            config.generation.schema = config.generation.schema.as_ref().map(|p| resolve(&base, p));
            if let Some(file) = config.file.as_mut() {
                file.directory = resolve(&base, &file.directory);
            }
        }
        Ok(config)
    }

    pub fn validate(&self) -> Result<()> {
        let generation = &self.generation;
        if generation.schema_type == SchemaType::Parquet {
            if let Some(schema) = &generation.schema {
                if schema.extension().is_some_and(|e| e.eq_ignore_ascii_case("proto")) {
                    bail!("parquet takes its types from the generators or an .avsc, not a .proto");
                }
            }
            if self.parquet.row_group_rows == 0 {
                bail!("parquet.row_group_rows must be at least 1");
            }
        }
        if generation.schema_type.needs_schema() && generation.schema.is_none() {
            bail!(
                "generation.schema_type = \"{}\" needs generation.schema, the {} file",
                match generation.schema_type {
                    SchemaType::Avro => "avro",
                    _ => "protobuf",
                },
                match generation.schema_type {
                    SchemaType::Avro => ".avsc",
                    _ => ".proto",
                }
            );
        }
        let speed = &self.speed;
        if speed.row_rate_per_second.is_some() && speed.byte_rate_per_second.is_some() {
            bail!("set speed.row_rate_per_second or speed.byte_rate_per_second, not both");
        }
        if speed.row_rate_per_second == Some(0) || speed.byte_rate_per_second == Some(0) {
            bail!("a rate cap must be greater than zero");
        }
        if speed.generator_batch_rows == Some(0) {
            bail!("speed.generator_batch_rows must be at least 1");
        }
        if speed.queue_depth == 0 {
            bail!("speed.queue_depth must be at least 1");
        }
        let threading = &self.threading;
        if threading.generator_threads == Some(0) {
            bail!("threading.generator_threads must be at least 1");
        }
        if threading.sink_threads == 0 {
            bail!("threading.sink_threads must be at least 1");
        }
        match self.sink.dest {
            Dest::Console => {
                if generation.schema_type.is_binary() {
                    bail!(
                        "the console shows text; avro, protobuf and parquet are binary, send them to a file or s3 sink"
                    );
                }
            }
            Dest::File => {
                let file = self.file.as_ref().ok_or_else(|| anyhow::anyhow!("sink.dest = \"file\" needs a [file] section"))?;
                if file.file_size == Some(0) {
                    bail!("file.file_size must be greater than zero");
                }
                if file.file_name_prefix.contains('/') || file.folder_prefix.contains('/') {
                    bail!("file.file_name_prefix and file.folder_prefix cannot contain '/'");
                }
            }
            Dest::S3 => {
                let s3 = self.s3.as_ref().ok_or_else(|| anyhow::anyhow!("sink.dest = \"s3\" needs an [s3] section"))?;
                if s3.bucket.trim().is_empty() {
                    bail!("s3.bucket is required");
                }
                if s3.file_size == Some(0) {
                    bail!("s3.file_size must be greater than zero");
                }
                if s3.part_size < 5 * 1024 * 1024 {
                    bail!("s3.part_size must be at least 5MiB; S3 rejects smaller parts");
                }
                if s3.max_concurrent_parts == 0 {
                    bail!("s3.max_concurrent_parts must be at least 1");
                }
                if s3.file_name_prefix.contains('/') || s3.folder_prefix.contains('/') {
                    bail!("s3.file_name_prefix and s3.folder_prefix cannot contain '/'");
                }
                if let Some(endpoint) = &s3.endpoint {
                    if !endpoint.contains("://") {
                        bail!("s3.endpoint needs a scheme, e.g. https://minio.internal:9000");
                    }
                    if endpoint.starts_with("http://") && !s3.allow_http {
                        bail!("s3.endpoint is plain http; set s3.allow_http = true to accept that");
                    }
                }
            }
            Dest::Kafka => {
                let kafka = self.kafka.as_ref().ok_or_else(|| anyhow::anyhow!("sink.dest = \"kafka\" needs a [kafka] section"))?;
                if matches!(generation.schema_type, SchemaType::Csv | SchemaType::Parquet) {
                    bail!("kafka carries json, avro or protobuf records, not csv or parquet; set generation.schema_type");
                }
                if generation.schema_type.needs_schema() && kafka.schema_registry.is_none() {
                    bail!("kafka with avro or protobuf needs a [kafka.schema_registry] section");
                }
                if kafka.brokers.trim().is_empty() || kafka.topic.trim().is_empty() {
                    bail!("kafka.brokers and kafka.topic are required");
                }
                if kafka.in_flight == 0 {
                    bail!("kafka.in_flight must be at least 1");
                }
            }
        }
        Ok(())
    }
}

fn resolve(base: &Path, path: &Path) -> PathBuf {
    if path.is_absolute() {
        path.to_path_buf()
    } else {
        base.join(path)
    }
}

/// A byte size as "100MiB" or a bare number of bytes.
fn byte_size_opt<'de, D: Deserializer<'de>>(deserializer: D) -> std::result::Result<Option<u64>, D::Error> {
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum Raw {
        Number(u64),
        Text(String),
    }
    match Option::<Raw>::deserialize(deserializer)? {
        None => Ok(None),
        Some(Raw::Number(bytes)) => Ok(Some(bytes)),
        Some(Raw::Text(text)) => parse_byte_size(&text).map(Some).map_err(serde::de::Error::custom),
    }
}

fn byte_size<'de, D: Deserializer<'de>>(deserializer: D) -> std::result::Result<u64, D::Error> {
    byte_size_opt(deserializer)?.ok_or_else(|| serde::de::Error::custom("a byte size is required"))
}

fn duration<'de, D: Deserializer<'de>>(deserializer: D) -> std::result::Result<Duration, D::Error> {
    duration_opt(deserializer)?.ok_or_else(|| serde::de::Error::custom("a duration is required"))
}

/// A duration as "5m", "1h30m", or a bare number of seconds.
fn duration_opt<'de, D: Deserializer<'de>>(deserializer: D) -> std::result::Result<Option<Duration>, D::Error> {
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum Raw {
        Number(u64),
        Text(String),
    }
    match Option::<Raw>::deserialize(deserializer)? {
        None => Ok(None),
        Some(Raw::Number(seconds)) => Ok(Some(Duration::from_secs(seconds))),
        Some(Raw::Text(text)) => parse_duration(&text).map(Some).map_err(serde::de::Error::custom),
    }
}

fn default_true() -> bool {
    true
}

fn default_schema_type() -> SchemaType {
    SchemaType::Csv
}

fn default_queue_depth() -> usize {
    8
}

fn default_sink_threads() -> usize {
    1
}

fn default_directory() -> PathBuf {
    PathBuf::from(".")
}

fn default_file_name_prefix() -> String {
    "part-".to_string()
}

fn default_folder_prefix() -> String {
    "batch-".to_string()
}

fn default_in_flight() -> usize {
    10_000
}

fn default_row_group_rows() -> usize {
    100_000
}

fn default_compression() -> String {
    "zstd".to_string()
}

fn default_compression_level() -> i32 {
    3
}

fn default_part_size() -> u64 {
    10 << 20
}

fn default_max_concurrent_parts() -> usize {
    8
}

fn default_retries() -> usize {
    10
}

fn default_timeout() -> Duration {
    Duration::from_secs(60)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_the_full_layout() {
        let config = Config::parse(
            r#"
[generation]
spec = "spec.toml"
schema_type = "avro"
schema = "event.avsc"
rows = 10
time = "5m"

[speed]
byte_rate_per_second = "45MiB"
generator_batch_rows = 3000
queue_depth = 100

[threading]
generator_threads = 12
sink_threads = 3

[sink]
dest = "file"

[file]
directory = "out"
file_size = "100MiB"
files_per_folder = 10

[kafka]
brokers = "b:9092"
topic = "t"
[kafka.schema_registry]
url = "http://r:8081"
[kafka.properties]
"compression.type" = "lz4"
"#,
        )
        .unwrap();
        assert_eq!(config.generation.schema_type, SchemaType::Avro);
        assert_eq!(config.generation.time, Some(Duration::from_secs(300)));
        assert_eq!(config.speed.byte_rate_per_second, Some(45 << 20));
        assert_eq!(config.speed.generator_batch_rows, Some(3000));
        assert_eq!(config.speed.queue_depth, 100);
        assert_eq!(config.threading.generator_threads, Some(12));
        assert_eq!(config.threading.sink_threads, 3);
        let file = config.file.unwrap();
        assert_eq!(file.file_size, Some(100 << 20));
        let kafka = config.kafka.unwrap();
        assert_eq!(kafka.properties["compression.type"], "lz4");
        assert_eq!(kafka.in_flight, 10_000);
    }

    #[test]
    fn rejects_unknown_keys_and_missing_schemas() {
        let error = Config::parse("[generation]\nspec = \"s.toml\"\nbogus = 1\n[sink]\ndest = \"console\"\n")
            .expect_err("unknown key");
        assert!(error.to_string().contains("bogus"), "{}", error);

        let error = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"protobuf\"\n[sink]\ndest = \"file\"\n[file]\n")
            .expect_err("no proto schema");
        assert!(error.to_string().contains("generation.schema"), "{}", error);

        let error = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"avro\"\nschema = \"a.avsc\"\n[sink]\ndest = \"console\"\n")
            .expect_err("binary to the console");
        assert!(error.to_string().contains("console shows text"), "{}", error);

        let error = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"avro\"\nschema = \"a.avsc\"\n[sink]\ndest = \"kafka\"\n[kafka]\nbrokers = \"b\"\ntopic = \"t\"\n")
            .expect_err("no registry");
        assert!(error.to_string().contains("schema_registry"), "{}", error);

        let error = Config::parse("[generation]\nspec = \"s.toml\"\n[sink]\ndest = \"kafka\"\n[kafka]\nbrokers = \"b\"\ntopic = \"t\"\n")
            .expect_err("csv to kafka");
        assert!(error.to_string().contains("not csv"), "{}", error);
    }

    #[test]
    fn parses_and_checks_the_s3_section() {
        let config = Config::parse(
            "[generation]\nspec = \"s.toml\"\n[sink]\ndest = \"s3\"\n[s3]\nbucket = \"b\"\nprefix = \"events/\"\nendpoint = \"http://minio:9000\"\nallow_http = true\npath_style = true\npart_size = \"8MiB\"\nfile_size = \"100MiB\"\nfiles_per_folder = 50\n[s3.credentials]\naccess_key_id = \"k\"\nsecret_access_key = \"s\"\n",
        )
        .unwrap();
        let s3 = config.s3.unwrap();
        assert_eq!(s3.part_size, 8 << 20);
        assert_eq!(s3.timeout, Duration::from_secs(60));
        assert_eq!(s3.credentials.unwrap().access_key_id, "k");

        let error = Config::parse("[generation]\nspec = \"s.toml\"\n[sink]\ndest = \"s3\"\n[s3]\nbucket = \"b\"\nendpoint = \"http://minio:9000\"\n")
            .expect_err("plain http without allow_http");
        assert!(error.to_string().contains("allow_http"), "{}", error);
        let error = Config::parse("[generation]\nspec = \"s.toml\"\n[sink]\ndest = \"s3\"\n[s3]\nbucket = \"b\"\npart_size = \"1MiB\"\n")
            .expect_err("tiny parts");
        assert!(error.to_string().contains("5MiB"), "{}", error);
    }

    #[test]
    fn parquet_goes_to_files_and_s3_only() {
        let config = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"parquet\"\n[parquet]\ncompression = \"snappy\"\n[sink]\ndest = \"file\"\n[file]\n").unwrap();
        assert_eq!(config.parquet.compression, "snappy");
        assert_eq!(config.parquet.row_group_rows, 100_000);
        let error = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"parquet\"\n[sink]\ndest = \"console\"\n").expect_err("console");
        assert!(error.to_string().contains("binary"), "{}", error);
        let error = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"parquet\"\n[sink]\ndest = \"kafka\"\n[kafka]\nbrokers = \"b\"\ntopic = \"t\"\n").expect_err("kafka");
        assert!(error.to_string().contains("parquet"), "{}", error);
        let error = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"parquet\"\nschema = \"e.proto\"\n[sink]\ndest = \"file\"\n[file]\n").expect_err("proto types");
        assert!(error.to_string().contains(".avsc"), "{}", error);
    }

    #[test]
    fn proto_is_an_alias_for_protobuf() {
        let config = Config::parse("[generation]\nspec = \"s.toml\"\nschema_type = \"proto\"\nschema = \"e.proto\"\n[sink]\ndest = \"file\"\n[file]\n").unwrap();
        assert_eq!(config.generation.schema_type, SchemaType::Protobuf);
        assert_eq!(config.speed.queue_depth, 8, "a missing [speed] keeps the defaults");
    }
}
