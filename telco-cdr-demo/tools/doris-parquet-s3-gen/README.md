# doris-parquet-s3-gen

Generate synthetic rows for a Doris table and stream them to S3, or a local
directory, as typed Parquet. Nothing is staged on disk and no whole file is
held in memory; each object is streamed out as a multipart upload and appears
atomically when it completes.

Row generation comes from the [`datagen`](../datagen) framework. This tool
adds the Doris schema, the type mapping, the Parquet writer and the upload
pipeline.

## Running it

```sh
cargo build --release          # needs ../datagen checked out alongside

# 1. an editable field spec, one entry per column, from your DDL
./target/release/doris-parquet-s3-gen --schema schema.sql --emit-spec spec.yaml

# 2. a commented config: where the files go, and how the run executes
./target/release/doris-parquet-s3-gen --emit-config dest.toml

# 3. generate
./target/release/doris-parquet-s3-gen --schema schema.sql --spec spec.yaml --config dest.toml
```

The command line names the inputs and the config; everything about how the
run executes is in the config.

| Flag | Meaning |
|---|---|
| `--schema FILE` | the Doris `CREATE TABLE`; required, it supplies column types and order |
| `--table NAME` | which table, when the DDL file holds several |
| `--spec FILE` | the field spec; without it one is derived from the schema |
| `--config FILE` | destination and run settings, see below |
| `--out-dir DIR` | shorthand for a local destination with every setting at default |
| `--rows N`, `--target-size SIZE`, `--time DURATION` | override the config's stop conditions for this run |
| `--emit-spec FILE`, `--emit-config FILE` | write the starter files and exit; `-` for stdout |
| `--no-progress` | no live status display |

`./run.sh` wraps step 3 with checks: the files exist, credentials are
present, and a pinned run id does not collide with objects already in the
bucket. `./run.sh -n` prints the command it would run. Both refuse to
overwrite an existing spec or config.

```sh
./run.sh                    # run dest.local.toml as configured
./run.sh -t 1GiB            # a smaller run, same config
./run.sh --local /tmp/pq    # into a directory instead
```

## Configuring it

One TOML file. `[dest] type` picks the destination, `[layout]` and `[run]`
apply to either kind, `[upload]` only to S3.

```toml
[dest]
type = "s3"                  # or "local"

[s3]
bucket = "my-bucket"
prefix = "doris/my_table/"
region = "ap-southeast-1"
# endpoint = "https://minio.internal:9000"   # any S3-compatible server
# path_style = true                          # MinIO, Ceph; not AWS or OSS
# [s3.credentials]                           # omit to use AWS_ACCESS_KEY_ID etc.
# [s3.tls]
# ca_certificate = "/etc/ssl/private-ca.pem" # extra roots for a private CA
# verify_certificates = true                 # false accepts any certificate

# [local]
# directory = "/data/doris-out"

[layout]
file_size = "2GiB"           # roll to a new object past this
files_per_folder = 100       # batch-NNNNN/ folders of this many files
# run_id = "run01"           # pin the run folder; default is a timestamp

[run]
threads = 0                  # generator threads; 0 = CPU count
upload_threads = 8           # upload workers, each owning its own files
queue_depth = 100            # batches buffered between the two pools
batch_rows = 32768
target_size = "10GiB"        # or rows = N; exclusive
# time = "2h"                # a cap on top of either

[upload]                     # S3 only: part size, concurrency, retries
[parquet]                    # codec, level, row group size
```

`--emit-config` writes this with every key explained. Unknown keys are
rejected, a missing or mismatched destination section is reported by name,
and a CA bundle is read and parsed at startup so a wrong path fails before
the first upload.

Two ready-made examples: `examples/dest-s3.toml` pins a run id and uses
20-file batch folders for a bucket, and `examples/dest-local.toml` produces
the same layout under `./test-output` for rehearsing a load from disk. Copy
one to `dest.local.toml`, which is gitignored, and edit.

This tool writes Parquet only. For CSV or JSON on stdout, to pipe into Kafka
or a stream load, use the `datagen` console tool it builds on.

## How it works

The Doris DDL supplies column types, order and nullability. The spec supplies
the generator for each column, and the [`datagen`](../datagen) framework
generates the rows: this tool adds the Doris schema, the type mapping, the
Parquet writer and the upload pipeline. See [Types](#types) for how every Doris type is
written to Parquet.

Generation and upload run in separate thread pools with a bounded queue
between them, so a row-group encode or a slow upload never stalls generation:

```
generators (CPU count)  ->  queue (30 batches)  ->  upload workers (8)
```

Each upload worker owns its own objects, encodes a row group at a time, and
streams the bytes out as S3 multipart parts. Closing a file completes the
upload, so an object appears atomically and Doris never sees a partial file.

## Examples

| File | What it shows |
|---|---|
| `examples/all_types.sql` | one column of every Doris type |
| `examples/all_types.spec` | every generator, with comments |
| `examples/all_types.js` | JavaScript generators for that spec |
| `examples/dest-s3.toml` | an S3 destination with a pinned run id and batch folders |
| `examples/dest-local.toml` | the same layout into a local directory, for rehearsing a load |

```sh
doris-parquet-s3-gen --schema examples/all_types.sql \
    --spec examples/all_types.spec --config examples/dest-local.toml
```

Run these from the repository root: a spec's `file:` paths resolve against the
working directory, not the spec's own directory. A test compiles every example,
checks it fits its schema, writes a Parquet file and asserts the corner cases
it is built to reach, so the examples cannot drift from the code.

## Compression

Parquet compresses internally, per column chunk, before anything is uploaded.
The bytes on the wire are already compressed, so transport compression on top
would gain nothing.

`compression_level` sets codec effort: zstd takes 1-22, gzip 0-9. The parquet
crate defaults zstd to 1, its weakest setting; this tool defaults to 3, which
is zstd's own default. When the network is the bottleneck, spending CPU here is
usually free speed because it shrinks what has to be uploaded.

The exception is high-entropy data. On a table dominated by UUIDs and random
numbers, levels 1 through 12 all produced 358-364 MiB from the same 4M rows,
a spread under 2%. Parquet dictionary-encodes low-cardinality columns before
zstd sees them, and random values have nothing left to squeeze. Raising the
level only pays when the data actually repeats. Measure before tuning.

### Unique ids: `sequence_string` vs `uuid`

`uuid` emits a real v4 UUID. The sixteen random bytes come from the
generator's own ChaCha12 stream, seeded from the OS once per thread, not from
`Uuid::new_v4`: that draws from the entropy pool on every call, which is a
`getrandom` syscall on any kernel without the vDSO entry point added in 6.11.
Three UUID columns meant three syscalls a row. Taking the bytes from the
already-seeded stream is worth about 31% on the whole pipeline, 922k to 1.20M
rows/s at four generator threads, and produces a v4 UUID by the same
construction.

The interesting difference is size, not speed. The same 4M rows are 358 MiB of
Parquet with `uuid` and 139 MiB with `sequence_string`, because dense ordered
strings compress and random ones do not. Which way that cuts depends entirely
on what bounds the run:

| | bytes/row | rows for 40 TB | hours at 4 threads |
|---|---|---|---|
| `uuid` | 93.8 | 4.3e11 | 98 |
| `sequence_string` | 36.4 | 1.1e12 | 312 |

Bounded by bytes, `uuid` wins three times over: it needs 2.6x fewer rows to
reach the same volume, and it is now the faster generator per row as well.
Bounded by rows, `sequence_string` wins instead, with 2.6x less to compress and
upload for the same row count.

`sequence_string` renders a counter into a fixed-width string instead:

```yaml
- name: invoiceid
  gen:
    type: sequence_string
    template: "inv0000000000000{}"   # default "{}"
    width: 36                                # default 36, a UUID's width
    start: 1                                 # default 1
    step: 1                                  # default 1
```

The counter is zero-padded to fill whatever the literal text leaves over, so
the output is always `width` characters and column widths and file sizes stay
where they were. All generator threads share one cursor and claim blocks of it,
so values are unique across threads; the atomic is touched once per block, not
once per row.

Reach for `uuid` when the run is bounded by a data volume, or when the test
depends on values being random, or when you are measuring compression,
compaction, or write amplification and need the pessimistic case. `sample.spec`
uses it for exactly the first reason. Reach for `sequence_string` when the run
is bounded by rows, a column only needs to be unique and the right size, and
you would rather not push the extra bytes.

### Where generation time goes

Randomness is not the expensive part. Measured by replacing one generator at a
time with a constant, 4M rows of `sample.spec` at four generator threads, best
of three, against a base of 8022 ms:

| generator | fields | cost | note |
|---|---|---|---|
| `datetime_around` | 3 | 2384 ms | 30%, and see the caveat below |
| `sequence_string` | 3 | 1131 ms | 14%, the zero-padding |
| `decimal_range` | 1 | 38 ms | 0.5% |
| `int_range` | 2 | none measurable | under the noise floor |
| `weighted_choice` | 3 | none measurable | under the noise floor |

The last two came out marginally *faster* with the generator than with a
constant, which is how a measurement says "below the noise", here about 1.5%.
A `weighted_choice` row is a float draw, a scan of a handful of weights, and a
`String` clone; a `constant` still pays the clone, so there is nothing to see.
A random draw is around 32 ns: `StdRng` is ChaCha12 in userspace, not a
syscall. Entropy has never been the thing to optimise here.

That ablation writes CSV, which forces the text rendering the Parquet path
skips, so it overstates `datetime_around` for real runs. It is the right shape
for comparing generators against each other, not for costing the pipeline.

So timestamps and decimals do not become text on the way to Parquet. A
generator emits `Value::Timestamp` (epoch microseconds) or `Value::Decimal`
(unscaled units and a scale), and the Parquet writer takes the number. The
strings are built only where something actually needs text: CSV output,
`template` fields, and the JavaScript bridge. Formatting a timestamp so the
writer could parse it straight back was costing more than the whole rest of the
row: on the full Parquet pipeline the change took 4M rows from about 435k to
about 1.09M rows/s.

Two traps in the other direction. `sequence` is classified `StatefulOrdered`,
which clamps generation to one thread, so swapping an `int_range` for it to
"save the random draw" costs about 4x. And `sequence_string` pays for every
digit it zero-pads: letting a literal prefix carry 16 of a 36-character value,
so only 20 digits are padded, is worth about 5% on the whole pipeline over
padding all 36. Twenty digits is also past the u64 counter range, so it cannot
overflow, while a UUID-shaped template leaves only twelve digits and 40 TB of
this data is about 1.1e12 rows. A run that would pass that ceiling is refused
before it starts rather than quietly emitting 37-character values partway
through.

`datetime_around` also stopped reading the clock per row. It refreshes
`Utc::now()` every few thousand rows and draws its offset in microseconds
rather than seconds, which keeps the sub-second digits as varied as they were
when every row called the clock. That matters more than it looks: a datetime
column that quietly became compressible would change what a load test measures.

## Object layout

```
<prefix><run_id>/batch-<NNNNN>/part-w<writer>-<index>.parquet
datagen/invoiceevent/20260911T051827Z-3f4abd/batch-00001/part-w03-000002.parquet
```

**Every run gets its own folder.** The run id defaults to a UTC timestamp
plus a short random suffix, so a restart or a second machine writing to the
same prefix adds a sibling folder instead of overwriting the last run. One run
can then be loaded, inspected or deleted by prefix alone. Pinning `run_id` in
the config makes the folder predictable, and is the only way two runs can
collide; `run_id = ""` writes straight into the prefix.

**Inside a run, files fill numbered batch folders.** Each `batch-NNNNN/`
holds exactly `files_per_folder` files, the last one possibly fewer. Folders
are assigned from one counter shared by every writer, so they fill in order
regardless of which writer produced a file. A folder holds roughly
`files_per_folder x file_size` of data, 200GiB at the defaults, which makes
it a natural unit for one Doris load job: point the job at a single folder,
and retry just that folder if it fails.

```sql
SELECT * FROM S3(
    "uri"    = "s3://my-bucket/datagen/invoiceevent/20260911T051827Z-3f4abd/batch-00001/*",
    "format" = "parquet"
    -- plus your endpoint, region and credentials
);
```

```toml
[layout]
files_per_folder = 100   # default; 0 puts files directly in the run folder
# run_id = "run01"       # pin the folder, or "" for no run folder
```

Both are `[layout]` keys in the config. The startup banner
prints the run folder, since that is what a load job or a cleanup needs.

S3 itself does not need the folders: it has no per-prefix object limit, and
this tool issues a few requests a second against a per-prefix ceiling in the
thousands. The folders are for the people and jobs that consume the output.

The writer id keeps concurrent writers apart and the index orders one writer's
files. Both pad rather than truncate, so more writers or more files than the
padding expects still produce distinct names.

## Surviving a long run

A run measured in days will meet a storage service having a bad minute. Three
layers stand between that and losing the job.

`object_store` retries a failed request on its own. That is configured in
`[upload]`, and it was worth checking: `retries` and `timeout` were being
parsed and then never handed to the client, so every run used the library
default of ten attempts inside a three-minute window. Three minutes is thin
cover for a job that runs for days.

```toml
[upload]
retries = 10           # attempts after the first, per request
timeout = "60s"        # deadline for one request
retry_timeout = "15m"  # total wall clock one request may spend retrying
max_backoff = "60s"    # ceiling on the exponential backoff
file_retries = 8       # files a writer may lose before the run gives up
```

`retry_timeout` is the one that decides whether a run survives an outage.
Retries stop at whichever of it and `retries` comes first, so a generous count
with a short window still gives up in a couple of minutes.

Past those, a writer abandons the file it was building and starts a new one
rather than taking the whole run down. The multipart upload is aborted, so the
parts it had already sent do not linger as billable storage; if that abort
cannot get through either, the parts are left for the bucket's own lifecycle
rule, which a long-running job should have anyway. Rows in an abandoned file
are gone -- nothing is retained to re-upload from -- so they are counted and
the final line says so rather than letting a degraded run look clean:

```
done: 41231 rows in 4 files, 2.1GiB in 611.2s  [1 file(s) abandoned after repeated upload failures, 8192 rows lost]
```

Whether the run makes those rows up depends on what bounds it. A
`--target-size` run does automatically, because abandoned bytes never reach the
counter the target is measured against. A `--rows` run spends its quota when a
row is generated rather than when it lands, so the lost rows are credited back
to it explicitly; that works as long as the generators are still running, which
they normally are, since uploads lag generation.

Set `file_retries = 0` to fail the run on the first upload that cannot be
recovered, which is the right setting when a partial dataset is worse than no
dataset.

## Sizing and limits

`--rows` is exact. `--target-size` is approximate, because a row's compressed
size is unknown until its row group is encoded. Generators project the observed
bytes-per-row across every row produced, and then whatever is already queued
still has to be written, so a run overshoots by up to
`queue_depth x batch_rows x bytes-per-row`. At the defaults that is around
300MiB: a rounding error against a multi-terabyte target, most of a small one.
Lower `run.queue_depth` when a small target needs to be tight.

`layout.file_size` rolls to a new object once a file passes the cap. Files can only
roll on a row group boundary, so the result is between the cap and the cap plus
one row group.

Peak memory is roughly `row_group_rows x columns x 24 bytes` per upload worker,
plus `part_size x max_concurrent_parts` for uploads in flight. The startup
banner prints an estimate.

S3 allows 10,000 parts per object, so `part_size` caps the largest file:
10MiB parts allow about 97GiB. A `file_size` that would need more parts is
refused at startup.

## Reading the status display

```
 rows 12,847,392 / 50,000,000   26%  ████████░░░░░░░░░░░░░░░  ETA 1m28s
 gen  421,003 rows/s upload-bound  threads 14  queue 30/30  buffered 192 MiB
 up   6 done, 8 writers                            sent 15 GiB  rate 15 MiB/s
 file part-w00-000007.parquet 1.2 GiB/2.0 GiB
```

`upload-bound` means generators are blocked on a full queue because uploads
cannot keep up. That is backpressure, not a stall. `draining` means generation
finished and the workers are completing their files. There is no byte rate on
the generation line on purpose: bytes do not exist until a row group is
encoded, so the only honest byte rate is the upload one.

`sent` and `rate` count compressed Parquet bytes, the same bytes that travel
over the network, so the rate is directly comparable to your link speed.
`buffered` is different: it is the uncompressed row group held in memory, so
it describes memory pressure rather than network traffic.

If uploads are the bottleneck, more generator threads will not help. Raise
`upload.max_concurrent_parts` and `run.upload_threads`, or accept the link speed. The
queue provides backpressure automatically: when it fills, generators block, so
generation self-throttles to whatever the network sustains. No rate limiting
is needed or wanted.

## Types

Every Doris column type is supported except `AGG_STATE`. The mapping follows
what Doris itself writes when it exports Parquet, on the principle that Doris
certainly reads its own output back.

| Doris | Parquet | Notes |
|---|---|---|
| `BOOLEAN` | BOOLEAN | |
| `TINYINT` / `SMALLINT` / `INT` | INT32, annotated 8 / 16 / 32-bit | range-checked per width |
| `BIGINT` | INT64 | |
| `LARGEINT` | UTF8 decimal digits | the only form holding its full ±(2^127 - 1) range |
| `FLOAT` / `DOUBLE` | FLOAT / DOUBLE | |
| `DECIMAL(p,s)`, p ≤ 38 | DECIMAL | INT32 to 9 digits, INT64 to 18, then fixed bytes |
| `DECIMAL(p,s)`, p 39..76 | DECIMAL, 32 fixed bytes | needs `enable_decimal256` on the cluster |
| `DATE` | INT32 DATE | 0000-01-01 to 9999-12-31 |
| `DATETIME(p)` | INT64 TIMESTAMP(MICROS), no zone | truncated to p digits |
| `CHAR(n)` / `VARCHAR(n)` / `STRING` | UTF8 | n is checked in UTF-8 bytes |
| `JSON` / `VARIANT` | UTF8 | always valid JSON |
| `IPV4` / `IPV6` | UTF8 | canonical text form |
| `ARRAY<T>` | LIST | any element type, nested to any depth |
| `MAP<K,V>` | MAP | scalar keys, unique and never null |
| `STRUCT<...>` | group | field comments allowed in the DDL |
| `BITMAP` / `HLL` / `QUANTILE_STATE` | INT64 / UTF8 / DOUBLE source values | see below |

Legacy spellings `DECIMALV2`, `DATEV1` and `DATETIMEV1` are accepted, and so
are element types written with `NOT NULL`, such as `ARRAY<INT NOT NULL>`.

**Values land exactly as Doris will store them.** A `DATETIME(3)` value is
written with three fractional digits, never six that Doris would then round.
A DECIMAL value with more fractional digits than its column holds is refused
rather than silently truncated. CHAR and VARCHAR lengths count UTF-8 bytes,
as Doris does, so a Chinese character costs three.

**Timestamps carry no time zone.** Doris DATETIME is a wall-clock value, so the
file stores the value as written and Doris loads it unchanged, whatever the
session time zone.

**Two deliberate departures from Doris's own export.** Doris writes every
DECIMAL as fixed-length bytes, while the writer here uses INT32 and INT64 for
narrow ones. Both are valid Parquet, and the second is what Spark writes, so
Doris reads it routinely. And sketch types have no Parquet form Doris loads
directly, so their columns carry the values a load turns into sketches. The
startup banner prints the expressions:

```
sketch columns carry source values; load them with:
  `uv` <- to_bitmap(`uv`)
  `visitors` <- hll_hash(`visitors`)
  `latency` <- to_quantile_state(`latency`, 2048)
```

so a load looks like:

```sql
INSERT INTO t
SELECT id, to_bitmap(uv), hll_hash(visitors), to_quantile_state(latency, 2048)
FROM S3("uri" = "s3://bucket/prefix/<run_id>/batch-00001/*", "format" = "parquet" ...);
```

`AGG_STATE` is refused, because its load expression depends on the aggregate.

`examples/all_types.sql` has one column of each type. It runs as is:

```sh
doris-parquet-s3-gen --schema examples/all_types.sql --out-dir /tmp/all --rows 100000
```

### Checked before the first row

Before generating anything, every generator's extreme values go through the
real Parquet conversion for its column: range bounds, the longest text it can
produce, every choice, and null if it has a null rate. A spec that could
produce something its column cannot hold fails immediately, listing each
problem:

```
Error: the spec can produce values these columns cannot hold:
  `xc_cdc_operation` VARCHAR(6): `thunder thunder thunder` is 23 bytes but VARCHAR(6) holds 6 bytes (Doris counts UTF-8 bytes, not characters)
  `amount` DECIMAL(19,4): `99.999999` has 6 decimal places but the column holds 4
```

This matters most for the rare value: a `fluctuating` generator that drifts
past SMALLINT an hour in, or a twelve-word lorem that only occasionally
overflows its VARCHAR. Templates and JavaScript produce text nothing can
predict, so only their null rate is checked up front; their values are still
checked on every row, as all values are.

## Field spec

The spec is YAML. Each field names a generator; fields can reference each other
and are ordered by a dependency graph. Generators include `uuid`,
`sequence_string`, `sequence`, `int_range`, `float_range`, `decimal_range`,
`datetime_around`, `datetime_range`, `choice`, `weighted_choice`, `template`,
`lorem`, `name`, `email`, `address`, `random_bytes`, `fluctuating`,
`javascript`, `array`, `map`, `struct`, `ipv4` and `ipv6`.

`array`, `map` and `struct` nest any generator, including each other. They run
against the same row, so an element can be a template over other fields.

```yaml
- name: tags                      # ARRAY<VARCHAR(16)>
  gen:
    type: array
    min_len: 0                    # defaults 0 and 5
    max_len: 3
    element:
      type: choice
      values: [new, sale, featured]

- name: prices                    # MAP<VARCHAR(16), DECIMAL(10,2)>
  gen:
    type: map
    max_len: 4                    # keys are redrawn until unique
    key: {type: lorem, words_min: 1, words_max: 1}
    value: {type: decimal_range, min: "1.00", max: "999.99", scale: 2}

- name: shipping                  # STRUCT<city:VARCHAR(80), zip:INT>
  gen:                            # or a JSON / VARIANT column, as an object
    type: struct
    fields:
      - name: city
        gen: {type: address, part: city}
      - name: zip
        gen: {type: int_range, min: 10000, max: 99999}

- name: client_ip                 # IPV4; ipv6 works the same way
  gen: {type: ipv4, cidr: "10.0.0.0/8"}
```

Nested columns also accept JSON text, so a `template` or `javascript` field can
feed one: `"[1, 2, 3]"` fills an `ARRAY<INT>`. A YAML list or mapping is a valid
`constant`, for a fixed array or struct.

`int_range` spans LARGEINT's full range. Bounds past the 64-bit range must be
quoted, because the spec parser has no 128-bit numbers:

```yaml
gen: {type: int_range, min: 0, max: "18446744073709551615"}
```

```yaml
version: 1
fields:
  - name: invoiceid
    gen:
      type: sequence_string
      template: "inv0000000000000{}"
  - name: xc_cdc_operation
    gen:
      type: weighted_choice
      values:
        - value: INSERT
          weight: 80
        - value: UPDATE
          weight: 20
  - name: amount
    gen:
      type: decimal_range
      min: "1.00"
      max: "99.99"
      scale: 2
```

Any generator takes `null_rate` to emit nulls at a given rate. `sequence`,
`fluctuating` and `javascript` are stateful and force a single generator
thread. See `sample.spec` for a worked example and the generator reference
below it.

### Fluctuating series

A value that walks rather than jumps, for series that look measured instead of
random. `direction` decides which way it heads, `flip_chance` is the per-row
chance of turning around, and the walk is clamped to `min` and `max`.

```yaml
gen:
  type: fluctuating
  data_type: double     # int | float | double | decimal
  start: 50.0
  min: 0.0
  max: 100.0
  initial_direction: up # up | down | random
  step_min: 0.01
  step_max: 1.5
  noise: 2.0            # jitter, default 0
  flip_chance: 0.1
```

`noise` jitters each step by a value drawn from `-noise..=noise`. The step
follows the direction; the noise does not. Below `step_min` it only varies the
pace, but once it outweighs the step it flips the sign, so a run headed up
still dips:

```
noise 0    101  102.8  104  105.4  106.8  108.7  109.7  111  112.2  113.7
noise 3    100.9 100.4  98.6 102.5  105.8  109.1  113.4  116.3 118.5 119.0
```

Both series still trend upward and neither leaves `min..max`.

### JavaScript

Use it for logic no other generator expresses. Everything else is far cheaper.

```yaml
- name: tier
  gen:
    type: javascript
    file: examples/all_types.js   # relative to the working directory
    function: tier_label
    deps: [amount]                # what the function reads
    parallel: true                # optional; see below
```

**`deps` decides what the function sees,** as well as ordering the field graph.
Declare everything the function reads. With no `deps` at all it receives the
whole row, which is slower: building the argument object is most of a call's
cost, and narrowing it on a 34-field row was worth about 25%.

**Values that JavaScript cannot hold exactly arrive as text.** Its numbers are
doubles, so integers past 2^53, LARGEINT, DECIMAL and timestamps come through
as strings. Nested values arrive as JSON text, so `JSON.parse(ctx.tags)` reads
an ARRAY, MAP or STRUCT field.

**The script is read, parsed and evaluated once per generator thread,** not per
row, and the function handle and field names are interned once. Only the call
itself repeats.

**Threading.** A stateful generator puts the whole run on one generator thread,
because its values must be produced in order. That covers `sequence`,
`fluctuating`, and JavaScript, whose globals persist across rows. One such
field is enough to clamp everything.

JavaScript is the exception you can opt out of: each thread builds its own
runtime with its own globals, so `parallel: true` is safe unless a global
produces values that must be unique or ordered across the whole run, such as a
row counter. On this machine, with 14 threads:

| | rows/s |
|---|---|
| JavaScript, single thread | 34,500 |
| JavaScript, `parallel: true` | 260,800 |
| No JavaScript at all | far higher; `template` costs almost nothing |

## Credentials

Credentials come from the standard AWS sources: `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY`, `AWS_PROFILE`, or an instance profile. Put them in
`[s3.credentials]` only if you must, and keep that file at mode 600.
