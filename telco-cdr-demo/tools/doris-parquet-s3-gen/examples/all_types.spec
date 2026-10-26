version: 1

# Every generator this tool supports, against examples/all_types.sql:
#
#   doris-parquet-s3-gen --schema examples/all_types.sql \
#       --spec examples/all_types.spec --out-dir /tmp/all --rows 100000
#
# Run it from the repository root: `file:` paths for JavaScript resolve
# against the working directory, not the spec's directory.

# How long a row's values stay visible to later fields.
#   row   clear after every row (the default)
#   batch keep across a batch, then clear
#   never keep for the life of the producer
context:
  reset: row

batch:
  rows: 1000

# Only used when writing CSV, which is what you get with no --out-dir or
# --s3-config. Parquet output ignores this section.
csv:
  delimiter: ","
  quote: "\""
  escape: "\""
  newline: "\n"
  null: ""

fields:
  # ---- hidden helpers -------------------------------------------------
  # Hidden fields are generated and can be referenced, but never written.
  # In Parquet mode any field that is not a schema column is hidden anyway.

  - name: h_first
    hidden: true
    gen:
      type: name
      part: first          # first | last | full

  - name: h_last
    hidden: true
    gen:
      type: name
      part: last

  - name: h_email
    hidden: true
    gen:
      type: email

  - name: h_city
    hidden: true
    gen:
      type: address
      part: city           # street | city | state | country | postal_code | full

  # A counter rendered into fixed-width text. Unlike `sequence` it is safe on
  # every thread, because producers claim counter blocks.
  - name: h_sku
    hidden: true
    gen:
      type: sequence_string
      template: "SKU-{}"
      start: 1
      step: 1
      width: 14

  - name: h_channel
    hidden: true
    gen:
      type: constant
      value: web

  # Branching that a template cannot express.
  #
  # `parallel: true` lets generation keep every thread. Each thread has its
  # own runtime and its own globals, so this is safe here: the global only
  # alternates a label. It is not safe when a global must be unique or
  # ordered across the whole run, as in variant_doc below.
  - name: h_tier
    hidden: true
    gen:
      type: javascript
      file: examples/all_types.js
      function: tier_label
      deps: [c_dec_9]
      parallel: true

  # ---- schema columns -------------------------------------------------

  # `order` sets the column order in CSV output and breaks ties in the
  # generation graph. Parquet always follows the DDL's order.
  - name: id
    order: -100
    gen:
      type: sequence
      start: 1
      step: 1

  - name: c_bool
    gen:
      type: choice
      values: [true, false]

  - name: c_tinyint
    gen:
      type: int_range
      min: -128
      max: 127

  # A value that walks up and down rather than jumping, for realistic series.
  - name: c_smallint
    gen:
      type: fluctuating
      data_type: int       # int | float | double | decimal
      start: 15000
      min: 0
      max: 30000
      initial_direction: random
      step_min: 1
      step_max: 40
      flip_chance: 0.05

  - name: c_int
    gen:
      type: weighted_choice
      values:
        - value: 10
          weight: 70
        - value: 200
          weight: 25
        - value: 3000
          weight: 5

  # LARGEINT spans past 64 bits, so bounds above that must be quoted.
  - name: c_largeint
    gen:
      type: int_range
      min: 0
      max: "18446744073709551615"

  - name: c_float
    gen:
      type: float_range
      min: -100.0
      max: 1000.0
      precision: 3

  # `noise` jitters each step by -noise..=noise. Wider than the step, it
  # flips the sign now and then, so a run headed up still dips. Default 0,
  # which makes every step follow the direction exactly.
  - name: c_double
    gen:
      type: fluctuating
      data_type: double
      start: 50.0
      min: 0.0
      max: 100.0
      initial_direction: up
      step_min: 0.01
      step_max: 1.5
      noise: 2.0
      flip_chance: 0.1
      precision: 4

  - name: c_dec_9
    gen:
      type: decimal_range
      min: "0.00"
      max: "9999999.99"
      scale: 2

  - name: c_dec_38
    gen:
      type: decimal_range
      min: "-99999999.9999999999"
      max: "99999999.9999999999"
      scale: 10

  # The column holds 20 decimal places; generating 18 is fine, the writer
  # pads the rest. Generating more than the column holds is refused.
  - name: c_dec_76
    gen:
      type: decimal_range
      min: "0.000000000000000000"
      max: "999999999.999999999999999999"
      scale: 18

  - name: c_date
    gen:
      type: datetime_range
      start: "2020-01-01T00:00:00Z"
      end: "2030-12-31T00:00:00Z"
      format: "%Y-%m-%d"

  # Relative to now unless `base` is set. Offsets may be negative.
  - name: c_datetime
    gen:
      type: datetime_around
      offset_seconds_min: -2592000
      offset_seconds_max: 0
      format: "%Y-%m-%d %H:%M:%S%.3f"

  - name: c_char
    gen:
      type: random_bytes
      min_bytes: 8
      max_bytes: 8
      encoding: hex        # hex | base64 | base64url

  # Templates read other fields by name and pick up their dependencies
  # automatically. Helpers: lower, upper, title, slug.
  - name: c_varchar
    gen:
      type: template
      value: "{{h_sku}} {{title h_first}} {{title h_last}} <{{lower h_email}}> {{h_city}}/{{upper h_channel}}/{{h_tier}}"

  # Any generator can emit nulls at a rate, on a nullable column.
  - name: c_string
    gen:
      type: lorem
      words_min: 3
      words_max: 12
      null_rate: 0.05

  # A struct becomes a JSON object when the column is JSON or VARIANT.
  - name: c_json
    gen:
      type: struct
      fields:
        - name: sku
          gen:
            type: template
            value: "{{h_sku}}"
        - name: score
          gen:
            type: float_range
            min: 0.0
            max: 100.0
            precision: 2
        - name: labels
          gen:
            type: array
            min_len: 1
            max_len: 3
            element:
              type: choice
              values: [alpha, bravo, cedar]

  # No `parallel` here: variant_doc numbers rows from a global, and every
  # thread would start that counter again. One stateful JavaScript field is
  # enough to put the whole run on a single generator thread, which costs
  # roughly 7x on this machine, so reach for `template` where you can.
  #
  # `deps` also decides what the function can see. Declare everything it
  # reads; with no deps at all it receives the whole row, which is slower.
  - name: c_variant
    gen:
      type: javascript
      file: examples/all_types.js
      function: variant_doc
      deps: [id, c_largeint, c_array]

  - name: c_ipv4
    gen:
      type: ipv4
      cidr: "10.0.0.0/8"   # omit for the whole address space

  - name: c_ipv6
    gen:
      type: ipv6
      cidr: "2001:db8::/32"

  - name: c_array
    gen:
      type: array
      min_len: 0
      max_len: 4
      element:
        type: int_range
        min: 1
        max: 999

  # Nesting goes as deep as the column does.
  - name: c_nested
    gen:
      type: array
      min_len: 1
      max_len: 2
      element:
        type: array
        min_len: 0
        max_len: 3
        element:
          type: lorem
          words_min: 1
          words_max: 2

  # Keys are redrawn until unique, and can never be null.
  - name: c_map
    gen:
      type: map
      min_len: 0
      max_len: 4
      key:
        type: lorem
        words_min: 1
        words_max: 1
      value:
        type: decimal_range
        min: "0.01"
        max: "99999999.99"
        scale: 2

  - name: c_struct
    gen:
      type: struct
      fields:
        - name: city
          gen:
            type: address
            part: city
        - name: zip
          gen:
            type: int_range
            min: 10000
            max: 99999
        - name: seen_at
          gen:
            type: datetime_around
            offset_seconds_min: -86400
            offset_seconds_max: 0
            format: "%Y-%m-%d %H:%M:%S%.3f"

  # Sketch columns carry the values the load turns into sketches. The startup
  # banner prints the expression each one needs.
  - name: c_bitmap
    gen:
      type: int_range
      min: 1
      max: 10000000

  - name: c_hll
    gen:
      type: uuid

  - name: c_quantile
    gen:
      type: float_range
      min: 0.0
      max: 5000.0
      precision: 3
