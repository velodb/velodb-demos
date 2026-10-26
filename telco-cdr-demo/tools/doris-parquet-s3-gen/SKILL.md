> Note: this document describes the original CSV generator this tool grew
> out of. Parquet and S3 output, Doris schema parsing and the pre-flight
> commands are documented in README.md.

# Skill: Generate Data with datagen

This skill guides you through generating CSV data with `datagen`. Read the user's intent, then build the right command from the options below.

## Step 1 — Choose a schema source

### Option A: Built-in preset

No spec file needed. Choose a preset that matches the data category:

| Preset | Columns |
|---|---|
| `user` | user_id, first_name, last_name, email, age, status, created_at |
| `order` | order_id, customer_id, item, quantity, unit_price, status, ordered_at |
| `product` | product_id, name, category, price, stock_qty, active, updated_at |
| `event` | event_id, event_type, user_id, session_id, occurred_at, payload |

```sh
datagen --preset user --rows 10000
```

### Option B: Custom YAML spec

Use `--init` to generate a starter spec, then edit it:

```sh
datagen --init          # writes sample.yaml + sample_script.js
datagen --spec sample.yaml --rows 100
```

`--init` refuses to overwrite existing files. Delete both `sample.yaml` and `sample_script.js` to regenerate.

### Option C: SQL CREATE TABLE (planned)

Future: `--from-sql schema.sql` will parse a `CREATE TABLE` statement and infer generator types from column names and SQL types, mapping them to the appropriate datagen generators.

## Step 2 — Set output limits

Use any combination of row count and time:

```sh
--rows 1000000          # stop after 1 million rows
--time 30s              # stop after 30 seconds
--time 5m               # stop after 5 minutes
--time 1h30m            # stop after 1 hour 30 minutes
```

Both can be set simultaneously — the first limit reached wins.

## Step 3 — Control output rate

At most one rate limit at a time:

```sh
--rows-per-second 5000
--bytes-per-second 1MB      # also accepts: 500000, 512K, 1KiB, 2MiB, 1G
```

## Step 4 — Choose output destination

### stdout (default)

```sh
datagen --preset user --rows 100000
datagen --preset user --rows 100000 | gzip > users.csv.gz
```

### Single file

```sh
datagen --preset order --rows 500000 --output orders.csv
```

### Split into multiple files

Each file gets a CSV header. Files are named `<stem>.<000001>.<ext>`:

```sh
datagen --preset event --rows 10000000 --output events.csv --split-bytes 100MB
# produces: events.000001.csv, events.000002.csv, ...
```

`--split-bytes` accepts the same size formats as `--bytes-per-second`.

## Step 5 — Other options

```sh
--threads 4             # producer threads (default: 2; forced to 1 for stateful generators)
--no-header             # suppress the CSV header row
```

## Full examples

Stream 10M user rows to stdout as fast as possible, 4 threads:
```sh
datagen --preset user --rows 10000000 --threads 4
```

Generate 1 week of event data into 500 MB files:
```sh
datagen --preset event --time 1h --output events.csv --split-bytes 500MB
```

Rate-limited order feed at 1000 rows/sec for 5 minutes:
```sh
datagen --preset order --rows-per-second 1000 --time 5m
```

Custom spec, no header, pipe into `wc -l`:
```sh
datagen --spec my_spec.yaml --rows 1000000 --no-header | wc -l
```

## Spec quick reference

```yaml
version: 1
csv:
  delimiter: ","
  quote: "\""
  escape: "\""
  newline: "\n"
  null: ""
context:
  reset: row          # row | batch | never
batch:
  rows: 1000
fields:
  - name: id
    order: -100       # lower order = earlier column and earlier generation (when no deps)
    gen:
      type: sequence
      start: 1
      step: 1
  - name: uid
    gen:
      type: sequence_string          # unique fixed-width string, no entropy draw
      template: "inv0000000000000{}"   # default "{}"
      width: 36                      # counter is zero-padded to fill the rest
  - name: label
    hidden: true      # generated but not written to CSV
    gen:
      type: template
      value: "{{lower first_name}}_{{id}}"   # deps auto-detected from {{...}}
  - name: score
    gen:
      type: fluctuating
      data_type: int
      start: 50
      min: 1
      max: 100
      initial_direction: random
      step_min: 1
      step_max: 5
      flip_chance: 0.05
```

See README.md for the full generator reference.

## Adding a JavaScript generator

Any field can use JavaScript for logic that other generators cannot express. The JS runtime is stateful — globals persist across rows.

```yaml
- name: label
  order: 10
  gen:
    type: javascript
    function: my_func
    file: logic.js
    deps:
      - id        # fields the function reads from ctx
```

```js
// logic.js
var _n = 0;
function my_func(ctx) {
    _n += 1;
    return ctx.id + "-" + _n;
}
```

Declare every field your function reads in `deps`. Unlike `template`, JavaScript deps are not auto-detected.
