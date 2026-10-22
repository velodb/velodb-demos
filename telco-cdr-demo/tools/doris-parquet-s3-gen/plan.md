> Note: this document describes the original CSV generator this tool grew
> out of. Parquet and S3 output, Doris schema parsing and the pre-flight
> commands are documented in README.md.

I want to rewrite this into Rust code for better generation performance.

Goal:

1. Write a multi-threaded data generator that generates CSV according to a spec.
2. Propose a self-contained spec format.
3. Data generation must be flexible and extensible.
4. Each row must have a generation context so cross-field reference is possible.
5. Each field must be highly customizable. Generator ideas include:
   - lorem text
   - address
   - name
   - email
   - string template
   - number range
   - cross-referenced string template
   - date/time around now, with +/- seconds
   - date/time around a fixed past/future time
   - random date/time in range
   - random choice
   - random choice with weight
   - fluctuating range for int, double, float, decimal, etc.
6. Use `x` producer threads, a high-speed batched MPSC channel, and a single consumer that writes to stdout.
7. The schema must be self-contained: it defines fields and each field's generation spec.
8. Generation context can be reset.

Output format:

1. The first stdout line is the CSV header row.
2. Header columns are field names from the schema, comma-separated.
3. Data rows follow one after another.
4. The stdout writer is the only component that formats and emits final CSV bytes.
5. The first CSV header line can be suppressed with:
   - example: `--no-header`

Runtime controls:

1. The binary should support initializing a starter spec:
   - example: `--init`
2. `--init` should generate `sample.yaml` in the current directory.
3. `--init` should only create `sample.yaml` if it does not already exist.
4. The generated `sample.yaml` must follow the latest design spec.
5. The binary should support configuring the number of producer threads:
   - example: `--threads x`
6. The `--threads` value controls the requested `x` producer threads.
7. The binary should support output rate limiting at stdout writer time.
8. Supported rate limit modes:
   - rows per second
   - bytes per second
9. Use `precise_rate_limiter` from crates.io:
   - https://crates.io/crates/precise_rate_limiter/versions
10. Default behavior is ASAP output with no rate limit.
11. The binary should support limiting total output by time:
   - example: `--time 5m12s`
12. The binary should support limiting total output by row count:
   - example: `--rows 111102`
13. Default behavior is no time limit and no row limit.

Testing requirements:

1. Always add test cases for each generation type.
2. When a new generator type is added, its expected validation and generation behavior should be covered by tests.
3. Tests should include DAG behavior for hidden fields, later references, missing references, and cycles.

Generation spec format proposal:

Use YAML as the first supported spec format. YAML is easy to review and can be parsed into strongly typed Rust structs with `serde`. JSON can be supported later using the same struct model.

Top-level shape:

```yaml
version: 1

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
```

Spec rules:

1. `version` is required so the format can evolve safely.
2. `fields` is required and ordered.
3. By default, CSV output column order is the field definition order.
4. Output order can optionally be influenced with a field-level `order` value.
5. `order` is a fuzzy ordering key:
   - type: signed integer
   - Rust type: `i64`
   - valid range: `i64::MIN..=i64::MAX`
   - default value: `0`
   - fields with smaller `order` values are rendered earlier
   - fields with larger `order` values are rendered later
   - if two fields have the same `order`, the earlier definition goes first
   - fields without `order` behave as if `order: 0`
   - negative values are convenient for fields that should appear before normal/default columns
   - positive values are convenient for fields that should appear after normal/default columns
6. Each field has:
   - `name`: the field name and context key.
   - `gen`: the generator configuration.
7. Each field can optionally have:
   - `hidden`: when `true`, the field is generated and can be referenced, but is not written to CSV output.
   - `order`: fuzzy output ordering value for non-hidden fields.
8. Field names must be unique.
9. Hidden field names still must be unique because they participate in the row context.
10. A field can reference any other field in the same row, including fields defined later in the schema.
11. Field references are not constrained by definition order or output order.
12. The engine should build an in-memory DAG of field dependencies during spec validation.
13. Field generation order should follow the DAG, not the field definition order.
14. Independent fields can be generated in any valid topological order within a row.
15. If the DAG cannot be satisfied, validation must fail immediately.
16. Unsatisfied DAG cases include:
   - cyclic dependencies, such as `field_a -> field_b -> field_a`
   - references to fields that do not exist
   - invalid generator dependencies
17. DAG validation failure is fatal.
18. The binary must print a clear error to stderr and exit non-zero.
19. The binary must not write any stdout output before spec and DAG validation succeeds.
20. The generator should validate the full spec before writing any output.
21. Generator-specific options live under `gen`, keeping the schema self-contained and extensible.

Context model:

1. Each row has a row context.
2. After a field is generated, its value is inserted into the row context using the field name.
3. Fields can reference other fields in the same row regardless of definition order.
4. Hidden fields are inserted into the row context the same as visible fields.
5. Hidden fields are not written to stdout and do not appear in the CSV header.
6. After the row context has all required generated values, the stdout consumer renders visible fields from the context.
7. Rendering uses output order, not generation order.
8. `context.reset` controls when generation context is reset.
9. Proposed reset values:
   - `row`: reset row-scoped values for every row. This is the default.
   - `batch`: keep context for a generated batch, then reset.
   - `never`: keep context for the lifetime of the producer.
10. Random number generators should be per producer thread.

Generator implementation model:

1. The parsed spec should be compiled into Rust generator objects before generation starts.
2. Generator configs should map to a strongly typed Rust enum, for example:

```rust
enum GeneratorSpec {
    Constant(ConstantSpec),
    Sequence(SequenceSpec),
    Name(NameSpec),
    Email(EmailSpec),
    Lorem(LoremSpec),
    Address(AddressSpec),
    Template(TemplateSpec),
    IntRange(IntRangeSpec),
    FloatRange(FloatRangeSpec),
    DecimalRange(DecimalRangeSpec),
    Fluctuating(FluctuatingSpec),
    DateTimeAround(DateTimeAroundSpec),
    DateTimeRange(DateTimeRangeSpec),
    Choice(ChoiceSpec),
    WeightedChoice(WeightedChoiceSpec),
    Uuid(UuidSpec),
    RandomBytes(RandomBytesSpec),
}
```

3. The compiled runtime form should also be enum-backed, for example:

```rust
enum Generator {
    Constant(ConstantGenerator),
    Sequence(SequenceGenerator),
    Name(NameGenerator),
    Email(EmailGenerator),
    Lorem(LoremGenerator),
    Address(AddressGenerator),
    Template(TemplateGenerator),
    IntRange(IntRangeGenerator),
    FloatRange(FloatRangeGenerator),
    DecimalRange(DecimalRangeGenerator),
    Fluctuating(FluctuatingGenerator),
    DateTimeAround(DateTimeAroundGenerator),
    DateTimeRange(DateTimeRangeGenerator),
    Choice(ChoiceGenerator),
    WeightedChoice(WeightedChoiceGenerator),
    Uuid(UuidGenerator),
    RandomBytes(RandomBytesGenerator),
}
```

4. Each generator variant should follow the same generation contract.
5. Conceptual trait shape:

```rust
trait FieldGenerator {
    fn kind(&self) -> GeneratorKind;
    fn dependencies(&self) -> &[FieldName];
    fn generate(&mut self, ctx: &mut RowContext) -> Result<Value>;
}
```

6. `generate` reads any required values from `RowContext`.
7. `generate` returns or inserts the generated value for its field.
8. The field runner is responsible for putting the generated value into the context under the field name, unless the concrete generator needs direct insertion for performance.
9. Stateful generators keep their mutable state inside the generator object.
10. Template generators should compile their Handlebars template once during spec compilation, not parse it for every row.
11. The planner should build the field DAG from `dependencies()`.
12. If the planner cannot produce a valid topological order, startup must fail before output begins.
13. The planner should invoke generators according to a valid topological order.
14. After all required generators for a row have run, stdout rendering reads visible field values from `RowContext`.
15. Output rendering does not invoke generators.

Stateful generators and threading:

Stateful generators make naive multi-threaded row generation incorrect. For example, if `fluctuating` state is owned separately by each producer thread, one logical stock-price-like series becomes multiple independent series interleaved in stdout.

Design rule:

1. Correct generator semantics are more important than using all requested threads.
2. Every generator should declare whether it is:
   - `stateless`: row can be generated independently.
   - `stateful_ordered`: values depend on the previous generated value in output order.
   - `stateful_keyed`: values depend on previous generated values for the same key.
3. `sequence` and `fluctuating` are `stateful_ordered` by default.
4. The spec planner should inspect the field DAG and generator kinds before execution.
5. If any visible or referenced field depends on `stateful_ordered` generators, the engine should preserve a single logical output order for those generators.
6. The simplest correct implementation is:
   - use one producer for specs containing `stateful_ordered` generators
   - ignore or cap `--threads` to `1` for generation
   - optionally warn on stderr that ordered state forced single-thread generation
7. A more advanced implementation can still parallelize safe work:
   - evaluate `stateful_ordered` fields in one ordered stream
   - use worker threads only for independent stateless fields
   - merge completed row contexts by row number before stdout rendering
8. `stateful_keyed` can be parallelized only if rows for the same key are routed to the same state owner, or if the generator has a well-defined merge/partition strategy.
9. The first implementation should prefer the simple correct behavior.
10. Later optimization can add a planner that parallelizes only fields proven safe.

Thread flag semantics with stateful specs:

1. `--threads x` is a requested parallelism level, not a promise that all generation will use `x` workers.
2. The execution planner may reduce effective producer threads to preserve generator semantics.
3. The binary should expose the effective thread count in diagnostics when it differs from the requested count.

Template syntax proposal:

1. Use Handlebars-style templates.
2. Proposed Rust crate:
   - `handlebars`
   - https://crates.io/crates/handlebars
3. Use `{{field_name}}` to reference a generated field from the row context.
4. Templates can reference hidden and visible fields.
5. Templates can reference fields defined earlier or later in the schema.
6. Register simple helper functions for common transforms:
   - `{{lower field_name}}`
   - `{{upper field_name}}`
   - `{{title field_name}}`
   - `{{slug field_name}}`
7. Helper functions are not custom syntax; they are Handlebars helpers registered by the generator.
8. Missing references should be spec validation errors, not silently empty strings.
9. Cyclic references should be fatal validation errors.
10. Template output is a string field value.

Generator types proposal:

Primitive/value generators:

```yaml
gen:
  type: constant
  value: hello
```

```yaml
gen:
  type: sequence
  start: 1
  step: 1
```

```yaml
gen:
  type: uuid
```

```yaml
gen:
  type: random_bytes
  min_bytes: 16
  max_bytes: 32
  encoding: base64url
```

Allowed `random_bytes.encoding` values:

1. `hex`
2. `base64`
3. `base64url`

`random_bytes` behavior:

1. Choose a random byte length in `min_bytes..=max_bytes`.
2. Generate that many random bytes.
3. Encode bytes using the configured `encoding`.
4. Output is a string field value.

String/person generators:

```yaml
gen:
  type: name
  part: full
```

Allowed `name.part` values:

1. `first`
2. `last`
3. `full`

```yaml
gen:
  type: email
  style: random
```

```yaml
gen:
  type: lorem
  words_min: 3
  words_max: 12
```

```yaml
gen:
  type: address
  part: full
```

Allowed `address.part` values:

1. `street`
2. `city`
3. `state`
4. `country`
5. `postal_code`
6. `full`

Template generator:

```yaml
gen:
  type: template
  value: "user-{{id}}-{{lower last_name}}"
```

Numeric generators:

```yaml
gen:
  type: int_range
  min: 1
  max: 100
```

```yaml
gen:
  type: float_range
  min: 0.0
  max: 1.0
  precision: 4
```

```yaml
gen:
  type: decimal_range
  min: "10.00"
  max: "999.99"
  scale: 2
```

```yaml
gen:
  type: fluctuating
  data_type: int
  start: 1000
  min: 0
  max: 10000
  initial_direction: up
  step_min: 1
  step_max: 10
  flip_chance: 0.03
```

```yaml
gen:
  type: fluctuating
  data_type: float
  start: 100.0
  min: 0.0
  max: 1000.0
  initial_direction: down
  step_min: 0.1
  step_max: 1.5
  flip_chance: 0.05
  precision: 3
```

Fluctuating generator behavior:

1. Fluctuating generators use `type: fluctuating`.
2. Supported `data_type` values:
   - `int`
   - `float`
   - `double`
   - `decimal`
3. Fluctuating generators are stateful.
4. State includes:
   - current value
   - current direction
5. Allowed `initial_direction` values:
   - `up`
   - `down`
   - `random`
6. Each generated value chooses a random positive step in `step_min..=step_max`.
7. If direction is `up`, the step is added to the current value.
8. If direction is `down`, the step is subtracted from the current value.
9. If the next value would exceed `max`, clamp or bounce at `max` and set direction to `down`.
10. If the next value would go below `min`, clamp or bounce at `min` and set direction to `up`.
11. `flip_chance` is a probability from `0.0` to `1.0` checked on each generated value.
12. When `flip_chance` triggers, the direction flips before applying the next step.
13. `precision` applies to `float` and `double`.
14. `scale` applies to `decimal`.
15. This should produce stock-price-like movement instead of a flat random distribution.

Date/time generators:

```yaml
gen:
  type: datetime_around
  # base: omit to use current time at generation time
  offset_seconds_min: -3600
  offset_seconds_max: 3600
  format: "%Y-%m-%dT%H:%M:%S"
```

```yaml
gen:
  type: datetime_around
  base: "2020-01-01T00:00:00Z"
  offset_seconds_min: -86400
  offset_seconds_max: 86400
  format: "%Y-%m-%d %H:%M:%S"
```

```yaml
gen:
  type: datetime_range
  start: "2020-01-01T00:00:00Z"
  end: "2030-01-01T00:00:00Z"
  format: "%Y-%m-%d"
```

Choice generators:

```yaml
gen:
  type: choice
  values:
    - red
    - green
    - blue
```

```yaml
gen:
  type: weighted_choice
  values:
    - value: bronze
      weight: 70
    - value: silver
      weight: 20
    - value: gold
      weight: 10
```

Nullability:

Any generator can optionally include `null_rate`, where `0.0` means never null and `1.0` means always null.

```yaml
gen:
  type: email
  null_rate: 0.05
```

When a generated value is null, the CSV writer emits the configured `csv.null` value.

Open design questions to review:

1. Should the spec support TOML in addition to YAML?
2. Should rate limiting count the header row for rows/s mode, or only data rows?

Backpressure and shutdown:

1. The MPSC channel should have only 3 buffered items.
2. Channel items should be batches, not individual rows.
3. Producers must not over-generate beyond what the bounded channel can absorb.
4. When the consumer decides to stop because `--time` or `--rows` is reached, it should drop the receiver.
5. Once the receiver is dropped, producer sends will fail.
6. Producers should treat send failure as the shutdown signal and quit cleanly.
