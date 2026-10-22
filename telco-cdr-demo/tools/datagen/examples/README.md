# Examples

One spec per shape, one schema pair, and a config per feature. Every config
points at files in this folder, so they run from anywhere:

```sh
datagen --config examples/console-csv.toml --rows 10
```

## Sinks and formats

| Config | Format | Sink |
|---|---|---|
| `console-csv.toml` | csv | stdout |
| `console-json.toml` | json | stdout |
| `file-csv.toml` | csv | `out/` files, three writers, batches and queue tuned |
| `file-avro.toml` | avro stream | `out/` files |
| `file-protobuf.toml` | protobuf stream | `out/` files |
| `file-parquet.toml` | parquet, types inferred | `out/` files, two writers, zstd |
| `file-parquet-avsc.toml` | parquet, types from `event.avsc` | `out/` files |
| `s3-csv.toml` | csv | S3 bucket, four writers, folders under a prefix |
| `s3-parquet.toml` | parquet | S3 bucket, four writers, 512 MiB objects |
| `s3-minio-avro.toml` | avro stream | MinIO: path style, plain http, credentials in the file |
| `kafka-json.toml` | json | Kafka, keyed by `id`, rate capped |
| `kafka-avro.toml` | avro, Confluent wire format | Kafka plus Schema Registry, two producers |
| `kafka-protobuf.toml` | protobuf, Confluent wire format | Kafka plus Schema Registry |
| `kafka-sasl-ssl.toml` | json | Kafka over SASL_SSL with librdkafka properties |

## Generation

| Config | Shows |
|---|---|
| `console-all-generators.toml` | `all-generators.toml`: every generator and field attribute, as JSON |
| `console-fluctuating.toml` | `fluctuating.toml`: stateful generators and the one-thread clamp |
| `limits.toml` | `rows`, `time`, byte rate, batch size, queue depth, generator threads |
| `console-yaml.toml` | `basic.yaml`: the older YAML list layout |

## Files

- `spec.toml` generates an `Event` with a nested `customer` record, a
  decimal `amount`, a timestamp, an enum-like `status`, an array of `tags`,
  and a `label` computed by `label.js`.
- `event.avsc` and `event.proto` describe that record for the schema-driven
  formats. `event.proto` defines two messages, so the configs name `Event`.
- `all-generators.toml` is the reference spec: every generator with its
  parameters commented.
- `fluctuating.toml` holds the stateful generators separately, since they
  force one generator thread.
- `basic.yaml` with `basic.js` is the YAML layout.

## Reading a stream file back

```python
import struct
def frames(data):
    i = 0
    while i < len(data):
        n = struct.unpack(">I", data[i:i+4])[0]; i += 4
        yield data[i:i+n]; i += n
schema, *records = frames(open("out/batch-00001/part-000001.avro", "rb").read())
```

For a protobuf record, `protoc --decode=datagen.sample.Event examples/event.proto < record.bin`
prints it.
