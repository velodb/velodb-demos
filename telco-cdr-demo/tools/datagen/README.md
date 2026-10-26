# datagen

A record generation framework, and a console tool built on it.

Describe rows in a spec: one generator per field, fields that can read each
other, nested arrays, maps and structs, and JavaScript where nothing else
will do. The library turns that into typed rows. The `datagen` binary
delivers them as CSV, JSON, Avro, Protobuf or Parquet to the console, to
rotating files, to S3, or to Kafka, all driven by one `config.toml`.

```sh
cargo build --release

# starter files in the current directory: config.toml, spec.toml,
# sample.avsc, sample.proto, sample_script.js
./target/release/datagen --init

# ten rows as configured (CSV to stdout by default)
./target/release/datagen --config config.toml --rows 10

# a quick look at a spec without a config file
./target/release/datagen --spec spec.toml --format json --rows 5
```

Only rows go to stdout. Everything else goes to stderr.

## The config file

`datagen --emit-config` prints the documented template. The sections:

```toml
[generation]
spec = "spec.toml"          # .toml (path-keyed) or .yaml (list layout)
schema_type = "csv"         # csv | json | avro | protobuf | parquet
# schema = "event.avsc"     # the .avsc or .proto; avro and protobuf need it, parquet may use an .avsc
# proto_message = "Event"   # when the .proto defines several messages
rows = 100000               # stop conditions; leave out to run until stopped
# time = "5m"

[speed]
# row_rate_per_second = 5000        one cap or the other
# byte_rate_per_second = "45MiB"
generator_batch_rows = 1000         # rows per batch, overrides the spec's batch.rows
queue_depth = 8                     # batches waiting between generators and sinks

[parquet]                   # for schema_type = "parquet"
row_group_rows = 100000
compression = "zstd"        # zstd | snappy | gzip | lz4 | none
compression_level = 3

[threading]
# generator_threads = 8     # CPU count by default; stateful generators force 1
sink_threads = 1            # parallel file writers or Kafka producers

[sink]
dest = "console"            # console | file | s3 | kafka

[console]
header = true

[file]
directory = "out"
file_size = "100MiB"        # start a new file at this size
file_name_prefix = "part-"  # <directory>/<folder_prefix>NNNNN/<file_name_prefix>NNNNNN.<ext>
folder_prefix = "batch-"
files_per_folder = 100      # 0 puts every file directly in the directory
# extension = "csv"         # defaults to csv, json, avro or proto
header = true

[s3]                        # objects laid out like [file], under a key prefix
bucket = "my-bucket"
prefix = "events/run-01/"
region = "ap-southeast-1"
# endpoint = "https://minio.internal:9000"   # S3-compatible servers
# path_style = true                          # MinIO, Ceph
# allow_http = false
# [s3.credentials]          # else AWS_ACCESS_KEY_ID and friends, a profile, or a role
# access_key_id = ""
# secret_access_key = ""
file_size = "100MiB"        # plus file_name_prefix, folder_prefix, files_per_folder, header
part_size = "10MiB"         # multipart parts, uploaded as the object is written

[kafka]
brokers = "localhost:9092"
topic = "events"
key_field = "id"            # message key from a field, as text; optional
in_flight = 10000           # unacknowledged messages per sink thread
[kafka.schema_registry]     # needed for avro and protobuf
url = "http://localhost:8081"
# subject = "events-value"  # default: <topic>-value
# username = ""
# password = ""
[kafka.properties]          # anything librdkafka accepts, verbatim
"compression.type" = "lz4"
```

Unknown keys are rejected. Relative paths resolve against the config file's
directory; a spec's JavaScript `file` is looked up from the working
directory first, then beside the spec. `--rows`, `--time`, `--threads`, `--rows-per-second` and
`--bytes-per-second` on the command line override the file.

With several sink threads each file or S3 writer tags its files `w01-`,
`w02-`, so `part-w02-000017.csv`. The console always uses one sink thread.

## Formats

| `schema_type` | Console | File and S3 | Kafka message |
|---|---|---|---|
| `csv` | header line, then rows; the spec's field order is the column order | same | not allowed |
| `json` | one object per line; dotted fields nest | same | the bare object |
| `avro` | not allowed (binary) | `[u32 len][schema JSON]` then `[u32 len][Avro datum]` per row | `0x00` + schema id (u32 big-endian) + datum |
| `protobuf` | not allowed (binary) | `[u32 len][.proto text]` then `[u32 len][message]` per row | `0x00` + schema id + message-index path + message |
| `parquet` | not allowed (binary) | Parquet files of `row_group_rows` row groups | not allowed |

Lengths are 4-byte big-endian. Every file starts with its own prelude (the
CSV header or the length-prefixed schema), so each file stands on its own.
Reading a stream back is a loop:

```python
import struct
def frames(data):
    i = 0
    while i < len(data):
        n = struct.unpack(">I", data[i:i+4])[0]; i += 4
        yield data[i:i+n]; i += n
schema, *records = frames(open("part-000001.avro", "rb").read())
```

For Kafka, avro and protobuf schemas are registered with the Schema
Registry under `<topic>-value` (or `subject`) and messages use the Confluent
wire format, so any registry-aware consumer reads them. The message-index
path for Protobuf is the standard zigzag-varint list, collapsed to a single
`0x00` for the first message in the file.

## The spec

A TOML spec keys each field by its fully qualified name. Table order is
column order.

```toml
version = 1

[csv]                            # optional: delimiter, quote, escape, newline, null
delimiter = ","

[fields.id]
type = "sequence_string"
template = "evt-{}"
width = 16

[fields."customer.first_name"]   # a field inside the customer record
type = "name"
part = "first"

[fields."customer.email"]
type = "template"
value = "{{lower customer.first_name}}@{{email_domain}}"

[fields.email_domain]
hidden = true                    # generated, never written
type = "choice"
values = ["example.com", "example.net"]

[fields.tags]                    # arrays, maps and structs take any generator
type = "array"
min_len = 0
max_len = 3
element = { type = "choice", values = ["new", "vip"] }
```

A dotted name such as `customer.email` is a field inside a record for
Avro, Protobuf and JSON, and a dotted column for CSV. A name and one of its
prefixes cannot both be fields. Each table holds the generator's `type` and
parameters plus the optional field attributes `hidden` and `order`.

The YAML list layout from earlier versions still works (`examples/basic.yaml`);
the parser is chosen by file extension.

**Generators:** `constant`, `sequence`, `sequence_string`, `uuid`, `int_range`
(to 128 bits), `float_range`, `decimal_range`, `fluctuating` (a random walk
with optional `noise`), `datetime_around`, `datetime_range`, `choice`,
`weighted_choice`, `name`, `email`, `address`, `lorem`, `random_bytes`,
`template`, `javascript`, `ipv4`, `ipv6`, and the nesting generators `array`,
`map` and `struct`, which take any generator as their elements.

**Fields read each other.** A `template` references other fields, dotted
names included, and picks up its dependencies automatically; `javascript`
declares them in `deps`. Fields generate in dependency order, whatever order
they are written in.

**Threading.** Every generator thread is independent: its own generators,
random state and row context. A stateful generator, `sequence`,
`fluctuating`, or `javascript` without `parallel = true`, puts generation on
one thread, because its values must come out in order. `sequence_string` is
the counter that stays parallel: threads claim blocks of it. Prefer it for
ids.

## Schemas

For `avro` and `protobuf` the schema file decides the shape and the types;
the spec fills in the values. At startup every spec field is matched to a
schema field by path, and each generator's extreme values are pushed through
the conversion, so a `name` generator aimed at a `long`, or a `null_rate` on
a non-nullable field, fails before the run rather than on row one.

- Avro: a spec field must exist in the schema; a schema field without a spec
  entry must be nullable or have a default. Decimals rescale to the schema's
  scale (never losing digits), timestamps become `timestamp-micros` /
  `-millis` / `-nanos` or a formatted string, strings match enum symbols,
  arrays, maps and structs map to array, map and record.
- Protobuf: the `.proto` is compiled in-process, no `protoc` needed. Spec
  fields must exist in the message; fields without a spec entry take their
  proto3 defaults. Enum values match by name, case-insensitively; a
  `google.protobuf.Timestamp` field takes a timestamp generator.

- Parquet: column types are inferred from the generators (strings, Int64,
  Float64, Boolean, `Decimal128(38, scale)` for decimals and 128-bit
  integers, microsecond timestamps, lists, maps and structs; every column
  nullable), or taken from an `.avsc` when `schema` names one. Each sink
  thread buffers a row group, decodes it through Arrow, and writes it; files
  rotate at `file_size` on row-group boundaries.

`examples/` holds a spec with the matching `event.avsc` and `event.proto`,
and a config for every sink and format; see `examples/README.md`.

## The framework

```rust
use datagen::{compile_toml, text::{OutputColumn, TextEncoder, TextFormat}};
use rand::SeedableRng;

let spec = compile_toml(toml)?;
let columns = OutputColumn::from_spec(&spec);
let encoder = TextEncoder::new(TextFormat::Json, &columns, spec.csv.clone());

let mut rng = rand::rngs::StdRng::from_entropy();
let mut fields = (*spec.fields).clone();
let mut row = datagen::RowContext::new();
for &index in spec.generation_order.iter() {
    let field = &mut fields[index];
    row.insert(field.name.clone(), field.generator.generate(&row, &mut rng)?);
}
let mut line = Vec::new();
encoder.encode_row(&row, &mut line)?;
```

`datagen::schema::{AvroEncoder, ProtoEncoder}` encode a row against a
schema, `datagen::encode::RecordEncoder` wraps all three with the framing,
`datagen::sink` holds the console, file, S3 and Kafka sinks, and
`datagen::stream::run` is the whole pipeline as a function: generator
threads, the bounded queue, rate limits and sink threads, without the CLI.

**Values keep their types.** Timestamps and decimals are carried as numbers,
not text, and rendered exactly; integers beyond 64 bits are supported for
LARGEINT-style columns; arrays, maps and structs render as JSON in the text
formats. `Value::to_json_string` renders any value, preserving field order.

## Consumers

[`doris-parquet-s3-gen`](../doris-parquet-s3-gen) builds on this library to
write typed Parquet for Doris tables and stream it to S3. Its schema parsing,
type mapping and upload pipeline live there; the rows come from here.

The previous single-file version of this tool is kept under `old-v0.1/`.
