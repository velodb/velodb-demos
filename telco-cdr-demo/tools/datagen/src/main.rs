//! `datagen`: generate rows from a spec and deliver them as CSV, JSON, Avro
//! or Protobuf to the console, to files, or to Kafka.
//!
//! Rows go to stdout by default, so the output pipes straight into whatever
//! consumes them. Everything diagnostic goes to stderr.

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{bail, Context, Result};
use clap::{ArgGroup, Parser};

use datagen::config::{
    Config, ConsoleConfig, Dest, GenerationConfig, ParquetConfig, SchemaType, SinkConfig,
    SpeedConfig, ThreadingConfig,
};
use datagen::encode::{Framing, RecordEncoder};
use datagen::presets::{
    preset_yaml, SAMPLE_AVSC, SAMPLE_CONFIG, SAMPLE_JS, SAMPLE_PROTO, SAMPLE_SPEC_TOML,
};
use datagen::registry::{self, RegistryClient};
use datagen::schema::{AvroEncoder, ProtoEncoder, RowTree};
use datagen::sink::{
    ConsoleSink, FileLayout, FileSink, KafkaSink, KafkaSinkOptions, ObjectSink, ParquetOptions,
    ParquetSink, ParquetTarget, S3Options, Sink,
};
use datagen::stream::{self, SinkFactory, StreamOptions};
use datagen::text::{OutputColumn, TextEncoder, TextFormat};
use datagen::units::{format_bytes, group_digits, parse_byte_size, parse_duration};
use datagen::CompiledSpec;

#[derive(Parser, Debug)]
#[command(
    version,
    about = "Generate rows from a spec and deliver them as CSV, JSON, Avro or Protobuf to the console, files or Kafka",
    group(ArgGroup::new("rate").args(["rows_per_second", "bytes_per_second"]).multiple(false)),
    group(ArgGroup::new("source").args(["config", "spec", "preset", "init", "emit_config"]).multiple(false)),
)]
struct Args {
    /// The config.toml: spec, format, speed, threads and sink. See --emit-config.
    #[arg(long, value_name = "FILE")]
    config: Option<PathBuf>,

    /// A spec on its own, written to stdout as --format: a quick look.
    #[arg(long, value_name = "FILE")]
    spec: Option<PathBuf>,

    /// A built-in spec instead of --spec: user, order, product or event.
    #[arg(long, value_name = "NAME")]
    preset: Option<String>,

    /// Write config.toml, spec.toml, sample.avsc, sample.proto and
    /// sample_script.js here to start from, then exit.
    #[arg(long)]
    init: bool,

    /// Print the documented config.toml template and exit.
    #[arg(long)]
    emit_config: bool,

    /// With --spec or --preset: csv or json (one object per line).
    #[arg(long, value_name = "FORMAT")]
    format: Option<String>,

    /// Generator threads. Overrides threading.generator_threads.
    #[arg(long, value_name = "N")]
    threads: Option<usize>,

    /// Stop after this many rows. Overrides generation.rows.
    #[arg(long, value_name = "N")]
    rows: Option<u64>,

    /// Stop after this long, e.g. 30s, 5m, 1h30m. Overrides generation.time.
    #[arg(long, value_parser = parse_duration, value_name = "DURATION")]
    time: Option<Duration>,

    /// Cap output at this many rows per second. Overrides [speed].
    #[arg(long, value_name = "N")]
    rows_per_second: Option<u64>,

    /// Cap output at this many bytes per second, e.g. 512K, 1MiB. Overrides [speed].
    #[arg(long, value_parser = parse_byte_size, value_name = "SIZE")]
    bytes_per_second: Option<u64>,

    /// With --spec or --preset: omit the CSV header line.
    #[arg(long)]
    no_header: bool,
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();

    if args.init {
        return init_samples();
    }
    if args.emit_config {
        print!("{}", SAMPLE_CONFIG);
        return Ok(());
    }

    let (spec, mut config) = if let Some(path) = &args.config {
        let config = Config::load(path)?;
        let spec = datagen::compile_spec_file(&config.generation.spec)?;
        (spec, config)
    } else if args.spec.is_some() || args.preset.is_some() {
        let spec = match (&args.spec, &args.preset) {
            (Some(path), _) => datagen::compile_spec_file(path)?,
            (None, Some(name)) => {
                let yaml = preset_yaml(name).with_context(|| {
                    format!("unknown preset `{}`; available: user, order, product, event", name)
                })?;
                datagen::compile_yaml(yaml)?
            }
            (None, None) => unreachable!(),
        };
        let schema_type = match args.format.as_deref().unwrap_or("csv").to_ascii_lowercase().as_str() {
            "csv" => SchemaType::Csv,
            "json" => SchemaType::Json,
            other => bail!("unknown --format `{}`; use csv or json (avro and protobuf need --config)", other),
        };
        (spec, console_config(schema_type, !args.no_header))
    } else {
        use clap::CommandFactory;
        Args::command().print_help()?;
        eprintln!("\nerror: one of --config <FILE>, --spec <FILE>, --preset <NAME>, --init or --emit-config is required");
        std::process::exit(2);
    };

    // Command-line limits override the file.
    if args.rows.is_some() {
        config.generation.rows = args.rows;
    }
    if args.time.is_some() {
        config.generation.time = args.time;
    }
    if args.threads.is_some() {
        config.threading.generator_threads = args.threads;
    }
    if args.rows_per_second.is_some() {
        config.speed.row_rate_per_second = args.rows_per_second;
        config.speed.byte_rate_per_second = None;
    }
    if args.bytes_per_second.is_some() {
        config.speed.byte_rate_per_second = args.bytes_per_second;
        config.speed.row_rate_per_second = None;
    }

    let requested = config.threading.generator_threads.unwrap_or_else(default_threads).max(1);
    let threads = stream::effective_threads(&spec, requested);
    if threads < requested {
        eprintln!(
            "stateful generators present; running {} generator thread instead of {}",
            threads, requested
        );
    }
    let mut sink_threads = config.threading.sink_threads.max(1);
    if config.sink.dest == Dest::Console && sink_threads > 1 {
        eprintln!("the console is one stream; running 1 sink thread instead of {}", sink_threads);
        sink_threads = 1;
    }

    let (encoder, framing, sinks, key_field) = build_output(&spec, &config, sink_threads).await?;
    let options = StreamOptions {
        threads,
        sink_threads,
        queue_depth: config.speed.queue_depth,
        batch_rows: config.speed.generator_batch_rows,
        rows: config.generation.rows,
        time_limit: config.generation.time,
        rows_per_second: config.speed.row_rate_per_second,
        bytes_per_second: config.speed.byte_rate_per_second,
        key_field,
    };
    let summary = stream::run(spec, encoder, framing, sinks, options).await?;
    let seconds = summary.elapsed.as_secs_f64().max(1e-9);
    eprintln!(
        "done: {} rows, {} in {:.1}s ({} rows/s, {}/s)",
        group_digits(summary.rows),
        format_bytes(summary.bytes),
        seconds,
        group_digits((summary.rows as f64 / seconds) as u64),
        format_bytes((summary.bytes as f64 / seconds) as u64),
    );
    Ok(())
}

/// A console config for the `--spec` / `--preset` shortcut.
fn console_config(schema_type: SchemaType, header: bool) -> Config {
    Config {
        generation: GenerationConfig {
            spec: PathBuf::new(),
            schema_type,
            schema: None,
            proto_message: None,
            rows: None,
            time: None,
        },
        speed: SpeedConfig::default(),
        threading: ThreadingConfig::default(),
        parquet: ParquetConfig::default(),
        sink: SinkConfig { dest: Dest::Console },
        console: ConsoleConfig { header },
        file: None,
        s3: None,
        kafka: None,
    }
}

/// The encoder, the framing, a factory for one sink per sink thread, and
/// the key field the config asks for.
async fn build_output(
    spec: &CompiledSpec,
    config: &Config,
    sink_threads: usize,
) -> Result<(Arc<RecordEncoder>, Framing, SinkFactory, Option<String>)> {
    // Path conflicts are reported here, before any schema is opened.
    RowTree::from_spec(spec)?;
    let schema_type = config.generation.schema_type;
    match config.sink.dest {
        Dest::Console => {
            let encoder = Arc::new(encoder_for(spec, config, config.console.header)?);
            let framing = stream_framing(schema_type);
            let prelude = encoder.prelude(&framing)?;
            let sinks: SinkFactory = Box::new(move |_| Ok(Box::new(ConsoleSink::new(prelude.clone())) as Box<dyn Sink>));
            Ok((encoder, framing, sinks, None))
        }
        Dest::File => {
            let file = config.file.as_ref().context("sink.dest = \"file\" needs a [file] section")?;
            let encoder = Arc::new(encoder_for(spec, config, file.header)?);
            let framing = stream_framing(schema_type);
            let prelude = encoder.prelude(&framing)?;
            let layout = FileLayout {
                directory: file.directory.clone(),
                file_name_prefix: file.file_name_prefix.clone(),
                folder_prefix: file.folder_prefix.clone(),
                files_per_folder: file.files_per_folder,
                file_size: file.file_size,
                extension: file
                    .extension
                    .clone()
                    .unwrap_or_else(|| schema_type.default_extension().to_string()),
                writer: None,
            };
            if schema_type == SchemaType::Parquet {
                let arrow_schema = Arc::new(parquet_schema(spec, config)?);
                let options = parquet_options(config);
                let sinks: SinkFactory = Box::new(move |index| {
                    let layout = FileLayout {
                        writer: (sink_threads > 1).then_some(index + 1),
                        ..layout.clone()
                    };
                    let file_size = layout.file_size;
                    Ok(Box::new(ParquetSink::new(
                        (*arrow_schema).clone(),
                        &options,
                        ParquetTarget::Local(layout),
                        file_size,
                    )?) as Box<dyn Sink>)
                });
                return Ok((encoder, framing, sinks, None));
            }
            let sinks: SinkFactory = Box::new(move |index| {
                let layout = FileLayout {
                    writer: (sink_threads > 1).then_some(index + 1),
                    ..layout.clone()
                };
                Ok(Box::new(FileSink::new(layout, prelude.clone())) as Box<dyn Sink>)
            });
            Ok((encoder, framing, sinks, None))
        }
        Dest::S3 => {
            let s3 = config.s3.as_ref().context("sink.dest = \"s3\" needs an [s3] section")?;
            let encoder = Arc::new(encoder_for(spec, config, s3.header)?);
            let framing = stream_framing(schema_type);
            let prelude = encoder.prelude(&framing)?;
            let options = S3Options {
                bucket: s3.bucket.clone(),
                prefix: s3.prefix.clone(),
                region: s3.region.clone(),
                endpoint: s3.endpoint.clone(),
                path_style: s3.path_style,
                allow_http: s3.allow_http,
                access_key_id: s3.credentials.as_ref().map(|c| c.access_key_id.clone()),
                secret_access_key: s3.credentials.as_ref().map(|c| c.secret_access_key.clone()),
                session_token: s3.credentials.as_ref().and_then(|c| c.session_token.clone()),
                part_size: s3.part_size as usize,
                max_concurrent_parts: s3.max_concurrent_parts,
                retries: s3.retries,
                timeout: s3.timeout,
            };
            let store = options.build_store()?;
            let layout = FileLayout {
                directory: PathBuf::from("."),
                file_name_prefix: s3.file_name_prefix.clone(),
                folder_prefix: s3.folder_prefix.clone(),
                files_per_folder: s3.files_per_folder,
                file_size: s3.file_size,
                extension: s3
                    .extension
                    .clone()
                    .unwrap_or_else(|| schema_type.default_extension().to_string()),
                writer: None,
            };
            if schema_type == SchemaType::Parquet {
                let arrow_schema = Arc::new(parquet_schema(spec, config)?);
                let parquet = parquet_options(config);
                let sinks: SinkFactory = Box::new(move |index| {
                    let layout = FileLayout {
                        writer: (sink_threads > 1).then_some(index + 1),
                        ..layout.clone()
                    };
                    let file_size = layout.file_size;
                    Ok(Box::new(ParquetSink::new(
                        (*arrow_schema).clone(),
                        &parquet,
                        ParquetTarget::Object {
                            store: store.clone(),
                            prefix: options.normalised_prefix(),
                            layout,
                            part_size: options.part_size,
                            max_concurrent_parts: options.max_concurrent_parts,
                        },
                        file_size,
                    )?) as Box<dyn Sink>)
                });
                return Ok((encoder, framing, sinks, None));
            }
            let sinks: SinkFactory = Box::new(move |index| {
                let layout = FileLayout {
                    writer: (sink_threads > 1).then_some(index + 1),
                    ..layout.clone()
                };
                Ok(Box::new(ObjectSink::new(
                    store.clone(),
                    &options.prefix,
                    layout,
                    prelude.clone(),
                    options.part_size,
                    options.max_concurrent_parts,
                )) as Box<dyn Sink>)
            });
            Ok((encoder, framing, sinks, None))
        }
        Dest::Kafka => {
            let kafka = config.kafka.as_ref().context("sink.dest = \"kafka\" needs a [kafka] section")?;
            let encoder = encoder_for(spec, config, false)?;
            let registry_type = match schema_type {
                SchemaType::Csv | SchemaType::Parquet => {
                    bail!("kafka carries json, avro or protobuf records, not csv or parquet")
                }
                SchemaType::Json => None,
                SchemaType::Avro => Some(registry::SchemaType::Avro),
                SchemaType::Protobuf => Some(registry::SchemaType::Protobuf),
            };
            let schema_id = match registry_type {
                None => 0,
                Some(registry_type) => {
                    let registry = kafka
                        .schema_registry
                        .as_ref()
                        .context("kafka with avro or protobuf needs [kafka.schema_registry]")?;
                    let subject = registry
                        .subject
                        .clone()
                        .unwrap_or_else(|| format!("{}-value", kafka.topic));
                    let client = RegistryClient::new(&registry.url, registry.username.clone(), registry.password.clone())?;
                    let schema = encoder.schema_text().unwrap_or_default();
                    let id = client.register(&subject, schema, registry_type).await?;
                    eprintln!("schema registered under `{}` with id {}", subject, id);
                    id
                }
            };
            let options = KafkaSinkOptions {
                brokers: kafka.brokers.clone(),
                topic: kafka.topic.clone(),
                in_flight: kafka.in_flight,
                properties: kafka.properties.clone(),
            };
            let sinks: SinkFactory = Box::new(move |_| {
                Ok(Box::new(KafkaSink::new(KafkaSinkOptions {
                    brokers: options.brokers.clone(),
                    topic: options.topic.clone(),
                    in_flight: options.in_flight,
                    properties: options.properties.clone(),
                })?) as Box<dyn Sink>)
            });
            Ok((Arc::new(encoder), Framing::Confluent { schema_id }, sinks, kafka.key_field.clone()))
        }
    }
}

fn stream_framing(schema_type: SchemaType) -> Framing {
    match schema_type {
        SchemaType::Csv | SchemaType::Json | SchemaType::Parquet => Framing::Lines,
        SchemaType::Avro | SchemaType::Protobuf => Framing::LengthPrefixed,
    }
}

/// Parquet's Arrow schema: from the `.avsc` when one is configured, else
/// inferred from the generators.
fn parquet_schema(spec: &CompiledSpec, config: &Config) -> Result<arrow::datatypes::Schema> {
    match &config.generation.schema {
        Some(path) => datagen::schema::arrow::schema_from_avro(&read_schema(path)?)
            .with_context(|| format!("Avro schema {}", path.display())),
        None => datagen::schema::arrow::infer_schema(spec),
    }
}

fn parquet_options(config: &Config) -> ParquetOptions {
    ParquetOptions {
        row_group_rows: config.parquet.row_group_rows,
        compression: config.parquet.compression.clone(),
        compression_level: config.parquet.compression_level,
    }
}

fn encoder_for(spec: &CompiledSpec, config: &Config, header: bool) -> Result<RecordEncoder> {
    let generation = &config.generation;
    match generation.schema_type {
        SchemaType::Csv => Ok(text_encoder(spec, TextFormat::Csv, header)),
        SchemaType::Json => Ok(text_encoder(spec, TextFormat::Json, header)),
        SchemaType::Parquet => {
            // Rows travel to the Parquet sink as JSON lines in Arrow form.
            let columns = OutputColumn::from_spec(spec);
            let mut encoder = TextEncoder::new(TextFormat::Json, &columns, spec.csv.clone());
            encoder.set_arrow_mode(true);
            Ok(RecordEncoder::Text(encoder))
        }
        SchemaType::Avro => {
            let path = generation.schema.as_ref().context("avro needs generation.schema")?;
            let text = read_schema(path)?;
            let encoder = AvroEncoder::new(&text, spec)
                .with_context(|| format!("Avro schema {}", path.display()))?;
            Ok(RecordEncoder::Avro(encoder))
        }
        SchemaType::Protobuf => {
            let path = generation.schema.as_ref().context("protobuf needs generation.schema")?;
            let encoder = ProtoEncoder::new(path, generation.proto_message.as_deref(), spec)
                .with_context(|| format!("Protobuf schema {}", path.display()))?;
            Ok(RecordEncoder::Proto(encoder))
        }
    }
}

fn text_encoder(spec: &CompiledSpec, format: TextFormat, header: bool) -> RecordEncoder {
    let columns = OutputColumn::from_spec(spec);
    let mut encoder = TextEncoder::new(format, &columns, spec.csv.clone());
    encoder.set_header(header);
    RecordEncoder::Text(encoder)
}

fn read_schema(path: &Path) -> Result<String> {
    std::fs::read_to_string(path).with_context(|| format!("failed to read schema {}", path.display()))
}

fn default_threads() -> usize {
    std::thread::available_parallelism().map(|n| n.get()).unwrap_or(4)
}

fn init_samples() -> Result<()> {
    let files = [
        ("config.toml", SAMPLE_CONFIG),
        ("spec.toml", SAMPLE_SPEC_TOML),
        ("sample.avsc", SAMPLE_AVSC),
        ("sample.proto", SAMPLE_PROTO),
        ("sample_script.js", SAMPLE_JS),
    ];
    if let Some((name, _)) = files.iter().find(|(name, _)| Path::new(name).exists()) {
        bail!("{} already exists; remove the sample files to regenerate them", name);
    }
    for (name, content) in files {
        std::fs::write(name, content).with_context(|| format!("failed to write {}", name))?;
    }
    eprintln!(
        "wrote config.toml, spec.toml, sample.avsc, sample.proto and sample_script.js\n\
         next: datagen --config config.toml --rows 10"
    );
    Ok(())
}
