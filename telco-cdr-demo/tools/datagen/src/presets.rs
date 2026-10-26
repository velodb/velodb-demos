//! Built-in specs: the starter written by `--init`, and the presets.

pub const SAMPLE_SPEC: &str = r#"version: 1

csv:
  delimiter: ","
  quote: "\""
  escape: "\""
  newline: "\n"
  null: ""

context:
  reset: row

batch:
  rows: 1000

fields:
  - name: id
    order: -100
    gen:
      type: sequence
      start: 1
      step: 1

  - name: email_domain
    hidden: true
    gen:
      type: choice
      values:
        - example.com
        - example.net
        - example.org

  - name: first_name
    gen:
      type: name
      part: first

  - name: last_name
    gen:
      type: name
      part: last

  - name: email
    gen:
      type: template
      value: "{{lower first_name}}.{{lower last_name}}.{{id}}@{{email_domain}}"

  - name: age
    gen:
      type: int_range
      min: 18
      max: 90

  - name: score
    gen:
      type: fluctuating
      data_type: int
      start: 50
      min: 1
      max: 100
      initial_direction: random
      step_min: 1
      step_max: 3
      flip_chance: 0.05

  - name: token
    gen:
      type: random_bytes
      min_bytes: 16
      max_bytes: 16
      encoding: base64url

  - name: created_at
    order: 100
    gen:
      type: datetime_around
      offset_seconds_min: -86400
      offset_seconds_max: 86400
      format: "%Y-%m-%d %H:%M:%S"

  - name: status
    order: 200
    gen:
      type: weighted_choice
      values:
        - value: active
          weight: 80
        - value: inactive
          weight: 15
        - value: blocked
          weight: 5

  - name: label
    order: 300
    gen:
      type: javascript
      function: function_a
      file: sample_script.js
      deps:
        - id
      # Declare only the fields your function actually reads from ctx.
      # Unlike the auto-deps behavior, explicit deps let other fields depend on this one.
"#;

/// The `config.toml` written by `--init` and printed by `--emit-config`.
pub const SAMPLE_CONFIG: &str = r#"# datagen configuration. Relative paths resolve against this file's folder.

[generation]
# The spec: a .toml with [fields.<path>] tables, or a .yaml with a fields list.
spec = "spec.toml"
# The record format: csv | json | avro | protobuf | parquet.
#   csv       a header line, then rows; the spec's field order is the column order
#   json      one object per line; dotted fields nest
#   avro      an .avsc decides the shape and types; the spec fills the values
#   protobuf  a .proto does the same
#   parquet   files and s3 only; types inferred from the generators, or from an .avsc
# In files, avro and protobuf are streams of [u32 length][schema] then
# [u32 length][record] per row. In Kafka they use the Confluent wire format
# (see [kafka]). The console shows text only: csv or json.
schema_type = "csv"
# The .avsc or .proto file. Not needed for csv and json.
# schema = "sample.avsc"
# schema = "sample.proto"
# When the .proto defines several messages, name the one to encode.
proto_message = "Event"
# Stop conditions. Leave both out to run until interrupted.
rows = 100000
# time = "5m"

[speed]
# Rate caps: one or the other.
# row_rate_per_second = 5000
# byte_rate_per_second = "45MiB"
# Rows per generated batch; overrides the spec's batch.rows.
generator_batch_rows = 1000
# Batches that may wait between the generators and the sinks.
queue_depth = 8

[parquet]
# For schema_type = "parquet". Rows per row group: each sink thread buffers
# a whole row group before encoding it.
row_group_rows = 100000
# zstd, snappy, gzip, lz4 or none; the level applies to zstd (1-22) and gzip (0-9).
compression = "zstd"
compression_level = 3

[threading]
# Generator threads. Defaults to the CPU count; a stateful generator
# (sequence, fluctuating, javascript without parallel) forces one.
# generator_threads = 8
# Sink threads: parallel file writers (files are tagged w01-, w02-, ...) or
# Kafka producers. The console always uses one.
sink_threads = 1

[sink]
# console | file | s3 | kafka
dest = "console"

[console]
header = true

[file]
directory = "out"
# Files: <directory>/<folder_prefix>NNNNN/<file_name_prefix>NNNNNN.<extension>
# Start a new file at this size; leave out for a single file per writer.
file_size = "100MiB"
file_name_prefix = "part-"
folder_prefix = "batch-"
# Files per folder; 0 puts every file directly in the directory.
files_per_folder = 100
# extension = "csv"          # defaults to csv, json, avro, proto or parquet
header = true

[s3]
# Objects: <prefix><folder_prefix>NNNNN/<file_name_prefix>NNNNNN.<extension>,
# uploaded as multipart parts while they are written.
bucket = "my-bucket"
prefix = "events/run-01/"
region = "ap-southeast-1"
# For any S3-compatible server set endpoint instead of relying on region.
# MinIO and Ceph address buckets by path; AWS and Alibaba OSS by subdomain.
# endpoint = "https://minio.internal:9000"
# path_style = true
# allow_http = false
# Leave credentials out to use AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY, a
# profile, or an instance role. Keep this file at mode 600 if you put keys in it.
# [s3.credentials]
# access_key_id = "AKIA..."
# secret_access_key = "..."
# session_token = ""
# part_size = "10MiB"        # S3 needs at least 5MiB; memory per sink thread is part_size x max_concurrent_parts
# max_concurrent_parts = 8
# retries = 10
# timeout = "60s"
file_size = "100MiB"
file_name_prefix = "part-"
folder_prefix = "batch-"
files_per_folder = 100
header = true

[kafka]
brokers = "localhost:9092"
topic = "events"
# Message key from a field, as text. Leave out for unkeyed messages.
key_field = "id"
# Messages awaiting acknowledgement at once, per sink thread.
in_flight = 10000

# Needed for avro and protobuf: the schema is registered under the subject
# and every message starts with 0x00 and the schema id as 4 big-endian
# bytes (protobuf adds the message-index path, a single 0x00 for the first
# message in the file).
[kafka.schema_registry]
url = "http://localhost:8081"
# subject = "events-value"   # default: <topic>-value
# username = ""
# password = ""

[kafka.properties]
# Anything librdkafka accepts, verbatim.
"compression.type" = "lz4"
# "security.protocol" = "SASL_SSL"
# "sasl.mechanism" = "PLAIN"
# "sasl.username" = ""
# "sasl.password" = ""
"#;

/// The `spec.toml` written by `--init`: one row of the sample Event, with a
/// nested `customer` record.
pub const SAMPLE_SPEC_TOML: &str = r#"# One generator per field. A dotted name such as "customer.email" is a field
# inside a record for Avro, Protobuf and JSON, and a dotted column for CSV.
# Table order is column order.
version = 1

[csv]
delimiter = ","
quote = "\""
escape = "\""
newline = "\n"
null = ""

[batch]
rows = 1000

[fields.id]
type = "sequence_string"
template = "evt-{}"
width = 16

[fields.email_domain]
hidden = true
type = "choice"
values = ["example.com", "example.net", "example.org"]

[fields."customer.first_name"]
type = "name"
part = "first"

[fields."customer.last_name"]
type = "name"
part = "last"

[fields."customer.email"]
type = "template"
value = "{{lower customer.first_name}}.{{lower customer.last_name}}@{{email_domain}}"

[fields.age]
type = "int_range"
min = 18
max = 90

[fields.amount]
type = "decimal_range"
min = "1.00"
max = "999.99"
scale = 2

[fields.created_at]
type = "datetime_around"
offset_seconds_min = -86400
offset_seconds_max = 0
format = "%Y-%m-%d %H:%M:%S"

[fields.status]
type = "weighted_choice"
values = [
  { value = "active", weight = 80 },
  { value = "inactive", weight = 15 },
  { value = "blocked", weight = 5 },
]

[fields.tags]
type = "array"
min_len = 0
max_len = 3
element = { type = "choice", values = ["new", "vip", "trial", "beta"] }

[fields.label]
type = "javascript"
function = "label"
file = "sample_script.js"
deps = ["id", "age"]
# The function keeps no state, so every thread can run it.
parallel = true
"#;

/// The Avro schema for the sample spec.
pub const SAMPLE_AVSC: &str = r#"{
  "type": "record",
  "name": "Event",
  "namespace": "datagen.sample",
  "fields": [
    {"name": "id", "type": "string"},
    {"name": "customer", "type": {
      "type": "record", "name": "Customer", "fields": [
        {"name": "first_name", "type": "string"},
        {"name": "last_name", "type": "string"},
        {"name": "email", "type": "string"}
      ]
    }},
    {"name": "age", "type": "int"},
    {"name": "amount", "type": {"type": "bytes", "logicalType": "decimal", "precision": 10, "scale": 2}},
    {"name": "created_at", "type": {"type": "long", "logicalType": "timestamp-micros"}},
    {"name": "status", "type": {"type": "enum", "name": "Status", "symbols": ["active", "inactive", "blocked"]}},
    {"name": "tags", "type": {"type": "array", "items": "string"}},
    {"name": "label", "type": ["null", "string"], "default": null}
  ]
}
"#;

/// The Protobuf schema for the sample spec.
pub const SAMPLE_PROTO: &str = r#"syntax = "proto3";

package datagen.sample;

message Event {
  string id = 1;
  Customer customer = 2;
  int32 age = 3;
  // A decimal as text keeps every digit.
  string amount = 4;
  // Microseconds since the Unix epoch.
  int64 created_at = 5;
  Status status = 6;
  repeated string tags = 7;
  string label = 8;
}

message Customer {
  string first_name = 1;
  string last_name = 2;
  string email = 3;
}

// Symbols match the spec's values case-insensitively.
enum Status {
  ACTIVE = 0;
  INACTIVE = 1;
  BLOCKED = 2;
}
"#;

/// The JavaScript file written alongside the sample spec.
pub const SAMPLE_JS: &str = r#"// label(ctx) runs once per row. ctx holds the fields listed in the spec's
// `deps`. It keeps no state, so the spec sets `parallel = true` and every
// generator thread runs its own copy.
function label(ctx) {
    var band = ctx.age >= 65 ? "senior" : ctx.age >= 30 ? "adult" : "young";
    return band + ":" + ctx.id;
}
"#;

pub fn preset_yaml(name: &str) -> Option<&'static str> {
    match name {
        "user" => Some(PRESET_USER),
        "order" => Some(PRESET_ORDER),
        "product" => Some(PRESET_PRODUCT),
        "event" => Some(PRESET_EVENT),
        _ => None,
    }
}

pub const PRESET_USER: &str = r#"version: 1
csv:
  delimiter: ","
  quote: "\""
  escape: "\""
  newline: "\n"
  null: ""
context:
  reset: row
batch:
  rows: 1000
fields:
  - name: user_id
    order: -100
    gen:
      type: sequence
      start: 1
      step: 1
  - name: _domain
    hidden: true
    gen:
      type: choice
      values: [example.com, example.net, example.org, mail.com]
  - name: first_name
    gen:
      type: name
      part: first
  - name: last_name
    gen:
      type: name
      part: last
  - name: email
    gen:
      type: template
      value: "{{lower first_name}}.{{lower last_name}}.{{user_id}}@{{_domain}}"
  - name: age
    gen:
      type: int_range
      min: 18
      max: 90
  - name: status
    gen:
      type: weighted_choice
      values:
        - value: active
          weight: 80
        - value: inactive
          weight: 15
        - value: blocked
          weight: 5
  - name: created_at
    order: 100
    gen:
      type: datetime_around
      offset_seconds_min: -31536000
      offset_seconds_max: 0
      format: "%Y-%m-%d %H:%M:%S"
"#;

pub const PRESET_ORDER: &str = r#"version: 1
csv:
  delimiter: ","
  quote: "\""
  escape: "\""
  newline: "\n"
  null: ""
context:
  reset: row
batch:
  rows: 1000
fields:
  - name: order_id
    gen:
      type: uuid
  - name: customer_id
    gen:
      type: int_range
      min: 1
      max: 100000
  - name: item
    gen:
      type: choice
      values:
        - Laptop
        - Headphones
        - Keyboard
        - Monitor
        - Mouse
        - Desk Chair
        - Webcam
        - USB Hub
  - name: quantity
    gen:
      type: int_range
      min: 1
      max: 20
  - name: unit_price
    gen:
      type: decimal_range
      min: "9.99"
      max: "999.99"
      scale: 2
  - name: status
    gen:
      type: weighted_choice
      values:
        - value: pending
          weight: 10
        - value: processing
          weight: 20
        - value: shipped
          weight: 30
        - value: delivered
          weight: 35
        - value: cancelled
          weight: 5
  - name: ordered_at
    order: 100
    gen:
      type: datetime_around
      offset_seconds_min: -7776000
      offset_seconds_max: 0
      format: "%Y-%m-%d %H:%M:%S"
"#;

pub const PRESET_PRODUCT: &str = r#"version: 1
csv:
  delimiter: ","
  quote: "\""
  escape: "\""
  newline: "\n"
  null: ""
context:
  reset: row
batch:
  rows: 1000
fields:
  - name: product_id
    gen:
      type: uuid
  - name: name
    gen:
      type: choice
      values:
        - Wireless Keyboard
        - Ergonomic Mouse
        - 4K Monitor
        - Noise-Cancelling Headphones
        - Standing Desk
        - Laptop Stand
        - USB-C Hub
        - Mechanical Keyboard
        - Gaming Chair
        - Webcam HD
  - name: category
    gen:
      type: choice
      values: [Electronics, Furniture, Accessories, Peripherals]
  - name: price
    gen:
      type: decimal_range
      min: "4.99"
      max: "1299.99"
      scale: 2
  - name: stock_qty
    gen:
      type: int_range
      min: 0
      max: 500
  - name: active
    gen:
      type: weighted_choice
      values:
        - value: "true"
          weight: 90
        - value: "false"
          weight: 10
  - name: updated_at
    order: 100
    gen:
      type: datetime_around
      offset_seconds_min: -2592000
      offset_seconds_max: 0
      format: "%Y-%m-%dT%H:%M:%SZ"
"#;

pub const PRESET_EVENT: &str = r#"version: 1
csv:
  delimiter: ","
  quote: "\""
  escape: "\""
  newline: "\n"
  null: ""
context:
  reset: row
batch:
  rows: 1000
fields:
  - name: event_id
    gen:
      type: uuid
  - name: event_type
    gen:
      type: weighted_choice
      values:
        - value: page_view
          weight: 40
        - value: click
          weight: 30
        - value: search
          weight: 15
        - value: add_to_cart
          weight: 8
        - value: purchase
          weight: 5
        - value: logout
          weight: 2
  - name: user_id
    gen:
      type: int_range
      min: 1
      max: 1000000
  - name: session_id
    gen:
      type: uuid
  - name: occurred_at
    gen:
      type: datetime_around
      offset_seconds_min: -86400
      offset_seconds_max: 0
      format: "%Y-%m-%dT%H:%M:%SZ"
  - name: payload
    gen:
      type: lorem
      words_min: 2
      words_max: 8
"#;

