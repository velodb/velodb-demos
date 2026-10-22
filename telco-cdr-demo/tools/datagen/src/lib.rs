//! Spec-driven record generation.
//!
//! A spec names fields and a generator for each; `compile` turns it into a
//! [`CompiledSpec`] whose generators run in dependency order to fill a
//! [`RowContext`] per row. What happens to the rows is up to the caller: the
//! [`text`] module renders them as CSV or newline-delimited JSON, and other
//! crates build Parquet from the same values.

pub mod compile;
pub mod composite;
pub mod config;
pub mod encode;
pub mod generators;
pub mod presets;
pub mod probe;
pub mod registry;
pub mod schema;
pub mod sink;
pub mod spec;
pub mod stream;
pub mod text;
pub mod units;
pub mod value;
mod value_json;

pub use compile::{
    build_generation_order, compile_spec, CompiledField, CompiledSpec, CsvSpecRuntime,
    GeneratorKind, RowContext,
};
pub use generators::{FieldGenerator, Generator};
pub use spec::{ContextReset, GeneratorSpec, RawSpec, RawSpecToml};
pub use value::Value;

/// Parse YAML and compile it in one step.
pub fn compile_yaml(yaml: &str) -> anyhow::Result<CompiledSpec> {
    let raw: RawSpec = serde_yaml::from_str(yaml).map_err(|error| anyhow::anyhow!("failed to parse YAML spec: {}", error))?;
    compile_spec(raw)
}

/// Parse a TOML spec (`[fields.<path>]` tables) and compile it.
pub fn compile_toml(text: &str) -> anyhow::Result<CompiledSpec> {
    let raw: RawSpecToml = toml::from_str(text).map_err(|error| anyhow::anyhow!("failed to parse TOML spec: {}", error))?;
    compile_spec(raw.into())
}

/// Read and compile a spec file, choosing the parser by extension: `.toml`
/// for the path-keyed layout, anything else for the YAML list layout.
pub fn compile_spec_file(path: &std::path::Path) -> anyhow::Result<CompiledSpec> {
    use anyhow::Context as _;
    let text = std::fs::read_to_string(path)
        .with_context(|| format!("failed to read spec {}", path.display()))?;
    let is_toml = path
        .extension()
        .is_some_and(|extension| extension.eq_ignore_ascii_case("toml"));
    let mut raw: RawSpec = if is_toml {
        toml::from_str::<RawSpecToml>(&text)
            .map_err(|error| anyhow::anyhow!("failed to parse TOML spec: {}", error))
            .map(RawSpec::from)
    } else {
        serde_yaml::from_str(&text).map_err(|error| anyhow::anyhow!("failed to parse YAML spec: {}", error))
    }
    .with_context(|| format!("spec {}", path.display()))?;
    if let Some(base) = path.parent().filter(|base| !base.as_os_str().is_empty()) {
        raw.resolve_script_paths(base);
    }
    compile_spec(raw).with_context(|| format!("spec {}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::compile::{compile_generator, CompiledSpec};
    use chrono::Utc;
    use crate::generators::*;
    use crate::spec::*;
    use crate::value::Value;
    use anyhow::Result;
    use rand::{rngs::StdRng, SeedableRng};
    use std::collections::HashSet;

    fn compile_yaml(yaml: &str) -> Result<CompiledSpec> {
        let raw = serde_yaml::from_str::<RawSpec>(yaml)?;
        compile_spec(raw)
    }

    /// Pull `count` values out of one generator instance.
    fn take_values(generator: &mut Generator, count: usize) -> Vec<String> {
        let mut rng = StdRng::seed_from_u64(7);
        let ctx = RowContext::new();
        (0..count)
            .map(|_| match generator.generate(&ctx, &mut rng).unwrap() {
                Value::String(value) => value,
                other => panic!("expected a string, got {:?}", other),
            })
            .collect()
    }

    #[test]
    fn renders_fixed_point_decimals() {
        assert_eq!(format_decimal_units(4512, 2), "45.12");
        assert_eq!(format_decimal_units(-4512, 2), "-45.12");
        // The fraction is zero-padded, not trimmed.
        assert_eq!(format_decimal_units(5, 2), "0.05");
        assert_eq!(format_decimal_units(70000, 4), "7.0000");
        assert_eq!(format_decimal_units(-5, 2), "-0.05");
        assert_eq!(format_decimal_units(42, 0), "42");
    }

    #[test]
    fn decimal_range_stays_in_bounds_at_the_declared_scale() {
        let mut generator =
            compile_gen("type: decimal_range\nmin: \"1.00\"\nmax: \"99.99\"\nscale: 2\n").unwrap();
        for value in take_raw_values(&mut generator, 500) {
            match value {
                Value::Decimal { units, scale } => {
                    assert_eq!(scale, 2);
                    assert!((100..=9999).contains(&units), "out of range: {}", units);
                }
                other => panic!("expected a decimal, got {:?}", other),
            }
        }
    }

    #[test]
    fn decimal_range_rejects_an_inverted_range() {
        let error = compile_gen_err(
            "type: decimal_range\nmin: \"9.00\"\nmax: \"1.00\"\nscale: 2\n",
            "min above max",
        );
        assert!(error.to_string().contains("min must be"), "{}", error);
    }

    #[test]
    fn datetime_around_keeps_the_configured_format() {
        let mut generator = compile_gen(
            "type: datetime_around\nbase: \"2020-06-15T12:00:00Z\"\noffset_seconds_min: 0\noffset_seconds_max: 0\nformat: \"%Y-%m-%d %H:%M:%S\"\n",
        )
        .unwrap();
        let values = take_raw_values(&mut generator, 2);
        assert!(matches!(values[0], Value::Timestamp { .. }));
        assert_eq!(values[0].csv_string(""), "2020-06-15 12:00:00");
    }

    /// The cached clock must not leak between fields: an explicit base always
    /// wins, and a generator without one still lands near now.
    #[test]
    fn datetime_around_without_a_base_tracks_the_clock() {
        let mut generator = compile_gen(
            "type: datetime_around\noffset_seconds_min: 0\noffset_seconds_max: 0\nformat: \"%Y-%m-%d %H:%M:%S\"\n",
        )
        .unwrap();
        let before = Utc::now().timestamp_micros();
        let values = take_raw_values(&mut generator, NOW_REFRESH_ROWS as usize + 10);
        let after = Utc::now().timestamp_micros();
        for value in values {
            match value {
                Value::Timestamp { micros, .. } => {
                    assert!(micros >= before && micros <= after, "{} out of window", micros);
                }
                other => panic!("expected a timestamp, got {:?}", other),
            }
        }
    }

    #[test]
    fn datetime_range_rejects_an_inverted_range() {
        let error = compile_gen_err(
            "type: datetime_range\nstart: \"2021-01-01T00:00:00Z\"\nend: \"2020-01-01T00:00:00Z\"\nformat: \"%Y-%m-%d\"\n",
            "start after end",
        );
        assert!(error.to_string().contains("min must be"), "{}", error);
    }

    /// Pull `count` values out of one generator instance, untouched.
    fn take_raw_values(generator: &mut Generator, count: usize) -> Vec<Value> {
        let mut rng = StdRng::seed_from_u64(11);
        let ctx = RowContext::new();
        (0..count)
            .map(|_| generator.generate(&ctx, &mut rng).unwrap())
            .collect()
    }

    use uuid::Uuid;

    fn compile_gen(yaml: &str) -> Result<Generator> {
        let spec = serde_yaml::from_str::<GeneratorSpec>(yaml)?;
        compile_generator(spec)
    }

    /// `Generator` is not `Debug`, so `expect_err` is not available here.
    fn compile_gen_err(yaml: &str, why: &str) -> anyhow::Error {
        match compile_gen(yaml) {
            Ok(_) => panic!("expected a failure: {}", why),
            Err(error) => error,
        }
    }

    #[test]
    fn sequence_string_pads_to_the_configured_width() {
        let mut generator = compile_gen("type: sequence_string\n").unwrap();
        let values = take_values(&mut generator, 3);
        assert_eq!(values[0].len(), 36, "{}", values[0]);
        assert_eq!(values[0], "0".repeat(35) + "1");
        assert_eq!(values[2], "0".repeat(35) + "3");
    }

    #[test]
    fn sequence_string_fills_a_template_to_uuid_shape() {
        let mut generator = compile_gen(
            "type: sequence_string\ntemplate: \"00000000-0000-4000-8000-{}\"\n",
        )
        .unwrap();
        let values = take_values(&mut generator, 2);
        assert_eq!(values[0], "00000000-0000-4000-8000-000000000001");
        assert_eq!(values[1], "00000000-0000-4000-8000-000000000002");
        assert!(values.iter().all(|value| value.len() == 36));
    }

    #[test]
    fn sequence_string_honours_start_and_step() {
        let mut generator =
            compile_gen("type: sequence_string\nstart: 10\nstep: 5\nwidth: 4\n")
                .unwrap();
        assert_eq!(take_values(&mut generator, 3), vec!["0010", "0015", "0020"]);
    }

    /// The point of the shared cursor: clones are what the generator threads
    /// get, and a key column cannot afford them repeating each other.
    #[test]
    fn sequence_string_clones_do_not_collide() {
        let generator = compile_gen("type: sequence_string\n").unwrap();
        let mut first = generator.clone();
        let mut second = generator.clone();
        // More than one block each, so the refill path is covered too.
        let count = (SEQUENCE_STRING_BLOCK as usize) + 100;
        let mut seen: HashSet<String> = take_values(&mut first, count).into_iter().collect();
        for value in take_values(&mut second, count) {
            assert!(seen.insert(value.clone()), "duplicate value {}", value);
        }
        assert_eq!(seen.len(), count * 2);
    }

    /// Zero-padding in Rust widens rather than truncates, so a counter that
    /// outgrows its width would silently start emitting 37-character values.
    #[test]
    fn sequence_string_refuses_to_outgrow_its_width() {
        let mut generator =
            compile_gen("type: sequence_string\nwidth: 4\nstart: 9998\n").unwrap();
        let mut rng = StdRng::seed_from_u64(3);
        let ctx = RowContext::new();
        for expected in ["9998", "9999"] {
            match generator.generate(&ctx, &mut rng).unwrap() {
                Value::String(value) => assert_eq!(value, expected),
                other => panic!("expected a string, got {:?}", other),
            }
        }
        let error = generator
            .generate(&ctx, &mut rng)
            .expect_err("10000 does not fit four digits");
        assert!(error.to_string().contains("no longer fits"), "{}", error);
    }

    /// The same ceiling, caught before a long run starts rather than partway
    /// through it.
    #[test]
    fn a_run_that_would_outgrow_the_width_is_refused_up_front() {
        let spec = compile_yaml(
            "version: 1\nfields:\n  - name: id\n    gen:\n      type: sequence_string\n      width: 4\n",
        )
        .unwrap();
        assert!(spec.check_sequence_capacity(Some(1_000), 1).is_ok());
        let error = spec
            .check_sequence_capacity(Some(100_000), 1)
            .expect_err("100k rows do not fit four digits");
        let text = error.to_string();
        assert!(text.contains("id") || format!("{:#}", error).contains("id"), "{:#}", error);
        // No row count to check against: the per-row guard is the backstop.
        assert!(spec.check_sequence_capacity(None, 1).is_ok());
    }

    /// 36 digits is what sample.spec uses, and it has to be beyond reach.
    #[test]
    fn a_full_width_counter_has_room_for_any_run() {
        let spec = compile_yaml(
            "version: 1\nfields:\n  - name: id\n    gen:\n      type: sequence_string\n      width: 36\n",
        )
        .unwrap();
        // 1e13 rows is roughly ten times the 40 TB this data compresses to.
        assert!(spec.check_sequence_capacity(Some(10_000_000_000_000), 64).is_ok());
    }

    #[test]
    fn sequence_string_rejects_a_template_that_does_not_fit() {
        let too_wide = compile_gen_err(
            "type: sequence_string\ntemplate: \"invoice-{}\"\nwidth: 4\n",
            "literal text is wider than the width",
        );
        assert!(too_wide.to_string().contains("does not fit"), "{}", too_wide);

        let no_placeholder = compile_gen_err(
            "type: sequence_string\ntemplate: \"invoice\"\n",
            "no placeholder",
        );
        assert!(
            no_placeholder.to_string().contains("placeholder"),
            "{}",
            no_placeholder
        );
    }

    fn generate_one_row(spec: &CompiledSpec) -> Result<RowContext> {
        let mut rng = StdRng::seed_from_u64(42);
        let mut fields = (*spec.fields).clone();
        let mut ctx = RowContext::new();
        for &field_index in spec.generation_order.iter() {
            let field = &mut fields[field_index];
            let value = field.generator.generate(&ctx, &mut rng)?;
            ctx.insert(field.name.clone(), value);
        }
        Ok(ctx)
    }

    #[test]
    fn generates_each_generator_type() {
        let spec = compile_yaml(
            r#"
version: 1
fields:
  - name: constant_field
    gen:
      type: constant
      value: hello
  - name: sequence_field
    gen:
      type: sequence
      start: 7
      step: 2
  - name: name_field
    gen:
      type: name
      part: full
  - name: email_field
    gen:
      type: email
      style: random
  - name: lorem_field
    gen:
      type: lorem
      words_min: 2
      words_max: 4
  - name: address_field
    gen:
      type: address
      part: full
  - name: template_source
    hidden: true
    gen:
      type: constant
      value: Mixed Case
  - name: template_field
    gen:
      type: template
      value: "{{slug template_source}}"
  - name: int_range_field
    gen:
      type: int_range
      min: 1
      max: 3
  - name: float_range_field
    gen:
      type: float_range
      min: 1.0
      max: 3.0
      precision: 2
  - name: decimal_range_field
    gen:
      type: decimal_range
      min: "10.00"
      max: "20.00"
      scale: 2
  - name: fluctuating_field
    gen:
      type: fluctuating
      data_type: int
      start: 50
      min: 1
      max: 100
      initial_direction: up
      step_min: 1
      step_max: 2
      flip_chance: 0.0
  - name: datetime_around_now_field
    gen:
      type: datetime_around
      offset_seconds_min: -1
      offset_seconds_max: 1
      format: "%Y"
  - name: datetime_around_field
    gen:
      type: datetime_around
      base: "2020-01-01T00:00:00Z"
      format: "%Y-%m-%d"
  - name: datetime_range_field
    gen:
      type: datetime_range
      start: "2020-01-01T00:00:00Z"
      end: "2020-01-01T00:00:00Z"
      format: "%Y-%m-%d"
  - name: choice_field
    gen:
      type: choice
      values:
        - red
        - blue
  - name: weighted_choice_field
    gen:
      type: weighted_choice
      values:
        - value: only
          weight: 1
  - name: uuid_field
    gen:
      type: uuid
  - name: random_bytes_field
    gen:
      type: random_bytes
      min_bytes: 4
      max_bytes: 4
      encoding: hex
"#,
        )
        .expect("spec should compile");

        let row = generate_one_row(&spec).expect("row should generate");

        assert!(
            matches!(row.get("constant_field"), Some(Value::String(value)) if value == "hello")
        );
        assert!(matches!(row.get("sequence_field"), Some(Value::I64(7))));
        assert!(matches!(row.get("name_field"), Some(Value::String(value)) if value.contains(' ')));
        assert!(
            matches!(row.get("email_field"), Some(Value::String(value)) if value.contains('@'))
        );
        assert!(
            matches!(row.get("lorem_field"), Some(Value::String(value)) if value.split_whitespace().count() >= 2)
        );
        assert!(
            matches!(row.get("address_field"), Some(Value::String(value)) if value.contains(','))
        );
        assert!(
            matches!(row.get("template_field"), Some(Value::String(value)) if value == "mixed-case")
        );
        assert!(
            matches!(row.get("int_range_field"), Some(Value::I64(value)) if (1..=3).contains(value))
        );
        assert!(
            matches!(row.get("float_range_field"), Some(Value::F64(value)) if *value >= 1.0 && *value <= 3.0)
        );
        assert!(
            matches!(row.get("decimal_range_field"), Some(value @ Value::Decimal { .. }) if !value.csv_string("").is_empty())
        );
        assert!(
            matches!(row.get("fluctuating_field"), Some(Value::I64(value)) if *value >= 1 && *value <= 100)
        );
        assert!(
            matches!(row.get("datetime_around_now_field"), Some(value @ Value::Timestamp { .. }) if value.csv_string("").len() == 4)
        );
        assert!(
            matches!(row.get("datetime_around_field"), Some(value @ Value::Timestamp { .. }) if value.csv_string("") == "2020-01-01")
        );
        assert!(
            matches!(row.get("datetime_range_field"), Some(value @ Value::Timestamp { .. }) if value.csv_string("") == "2020-01-01")
        );
        assert!(
            matches!(row.get("choice_field"), Some(Value::String(value)) if value == "red" || value == "blue")
        );
        assert!(
            matches!(row.get("weighted_choice_field"), Some(Value::String(value)) if value == "only")
        );
        assert!(
            matches!(row.get("uuid_field"), Some(Value::String(value)) if Uuid::parse_str(value).is_ok())
        );
        assert!(
            matches!(row.get("random_bytes_field"), Some(Value::String(value)) if value.len() == 8)
        );
    }

    #[test]
    fn rejects_dependency_cycles() {
        let result = compile_yaml(
            r#"
version: 1
fields:
  - name: field_a
    gen:
      type: template
      value: "{{field_b}}"
  - name: field_b
    gen:
      type: template
      value: "{{field_a}}"
"#,
        );

        let message = format!("{:#}", result.err().expect("cycle should fail validation"));
        assert!(message.contains("DAG cannot be satisfied"));
    }

    #[test]
    fn supports_hidden_later_field_references() {
        let spec = compile_yaml(
            r#"
version: 1
fields:
  - name: visible
    gen:
      type: template
      value: "{{hidden_value}}"
  - name: hidden_value
    hidden: true
    gen:
      type: constant
      value: ok
"#,
        )
        .expect("spec should compile");

        assert_eq!(spec.output_order.len(), 1);
        assert_eq!(spec.fields[spec.output_order[0]].name, "visible");
        let generation_names = spec
            .generation_order
            .iter()
            .map(|&index| spec.fields[index].name.as_str())
            .collect::<Vec<_>>();
        assert_eq!(generation_names, vec!["hidden_value", "visible"]);
    }

    fn temp_dir(tag: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("dpsg-{}-{}", tag, std::process::id()));
        std::fs::create_dir_all(&dir).expect("create temp dir");
        dir
    }

    /// Generate `rows` rows from a compiled spec with a fixed seed.
    fn generate_rows(spec: &CompiledSpec, rows: usize) -> Vec<RowContext> {
        let mut rng = StdRng::seed_from_u64(19);
        let mut fields = (*spec.fields).clone();
        let mut out = Vec::with_capacity(rows);
        for _ in 0..rows {
            let mut ctx = RowContext::new();
            for &index in spec.generation_order.iter() {
                let field = &mut fields[index];
                let value = field
                    .generator
                    .generate(&ctx, &mut rng)
                    .unwrap_or_else(|error| panic!("field `{}`: {:#}", field.name, error));
                ctx.insert(field.name.clone(), value);
            }
            out.push(ctx);
        }
        out
    }

    #[test]
    fn javascript_sees_exactly_its_declared_deps() {
        let dir = temp_dir("jsdeps");
        let script = dir.join("peek.js");
        std::fs::write(
            &script,
            "function peek(ctx) { return (ctx.a === undefined ? 'no-a' : 'a') + '/' +
                                         (ctx.b === undefined ? 'no-b' : 'b'); }",
        )
        .unwrap();
        let spec_for = |deps: &str| {
            format!(
                "version: 1\nfields:\n  - name: a\n    gen: {{type: constant, value: 1}}\n  \
                 - name: b\n    gen: {{type: constant, value: 2}}\n  - name: seen\n    gen:\n      \
                 type: javascript\n      file: \"{}\"\n      function: peek\n      deps: {}\n",
                script.display(),
                deps
            )
        };
        let read = |deps: &str| {
            let spec = compile_yaml(&spec_for(deps)).expect("compiles");
            generate_rows(&spec, 1)[0].get("seen").unwrap().csv_string("")
        };
        // Declared deps narrow what the call builds, which is most of its cost.
        assert_eq!(read("[a]"), "a/no-b");
        assert_eq!(read("[a, b]"), "a/b");
        // With none declared, the function still receives the whole row.
        assert_eq!(read("[]"), "a/b");
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn parallel_javascript_lifts_the_single_thread_clamp() {
        let dir = temp_dir("jspar");
        let script = dir.join("one.js");
        std::fs::write(&script, "function one() { return 1; }").unwrap();
        let spec_for = |parallel: bool| {
            format!(
                "version: 1\nfields:\n  - name: v\n    gen:\n      type: javascript\n      \
                 file: \"{}\"\n      function: one\n      parallel: {}\n",
                script.display(),
                parallel
            )
        };
        assert!(
            compile_yaml(&spec_for(false)).unwrap().has_stateful_ordered,
            "JavaScript is single-threaded unless told otherwise"
        );
        assert!(
            !compile_yaml(&spec_for(true)).unwrap().has_stateful_ordered,
            "parallel: true must keep every thread"
        );
        std::fs::remove_dir_all(&dir).ok();
    }

    /// Generate a fluctuating series as numbers.
    fn fluctuating_series(extra: &str, rows: usize) -> Vec<f64> {
        let yaml = format!(
            "version: 1\nfields:\n  - name: v\n    gen:\n      type: fluctuating\n      \
             data_type: double\n      start: 500.0\n      min: 0.0\n      max: 1000.0\n      \
             initial_direction: up\n      step_min: 1.0\n      step_max: 2.0\n      \
             flip_chance: 0.0\n{}",
            extra
        );
        let spec = compile_yaml(&yaml).expect("fluctuating spec compiles");
        generate_rows(&spec, rows)
            .iter()
            .map(|ctx| match ctx.get("v").unwrap() {
                Value::F64(number) => *number,
                other => panic!("expected a number, got {:?}", other),
            })
            .collect()
    }

    fn drops(series: &[f64]) -> usize {
        series.windows(2).filter(|pair| pair[1] < pair[0]).count()
    }

    #[test]
    fn fluctuating_noise_defaults_to_none_and_never_reverses() {
        // No noise: an upward run only ever rises, until it reaches max.
        let series = fluctuating_series("", 200);
        assert_eq!(drops(&series), 0, "a step without noise must follow the direction");
        assert!(series.last().unwrap() > series.first().unwrap());

        // Stating it explicitly is the same as leaving it out.
        assert_eq!(drops(&fluctuating_series("      noise: 0.0\n", 200)), 0);
    }

    #[test]
    fn fluctuating_noise_can_outweigh_the_step_and_drop() {
        // Noise wider than the step, so an upward run dips on the way.
        let series = fluctuating_series("      noise: 6.0\n", 500);
        let falls = drops(&series);
        assert!(falls > 50, "noise 6 over a step of 1..2 should dip often, saw {}", falls);
        assert!(falls < 450, "it should still trend upward, saw {} drops", falls);

        // Small noise relative to the step cannot reverse it.
        assert_eq!(
            drops(&fluctuating_series("      noise: 0.5\n", 300)),
            0,
            "noise below step_min can never flip a step's sign"
        );
    }

    #[test]
    fn fluctuating_noise_still_respects_the_bounds() {
        // Noise far larger than the range must not escape min..max.
        let series = fluctuating_series("      noise: 5000.0\n", 2000);
        assert!(
            series.iter().all(|value| (0.0..=1000.0).contains(value)),
            "a value left the declared range"
        );
        assert!(series.iter().any(|value| *value >= 900.0), "never neared max");
        assert!(series.iter().any(|value| *value <= 100.0), "never neared min");
    }

    #[test]
    fn fluctuating_rejects_negative_noise() {
        let error = compile_yaml(
            "version: 1\nfields:\n  - name: v\n    gen:\n      type: fluctuating\n      \
             data_type: int\n      start: 5\n      min: 0\n      max: 10\n      \
             initial_direction: up\n      step_min: 1\n      step_max: 2\n      noise: -1\n",
        )
        .err()
        .expect("negative noise is meaningless");
        assert!(format!("{:#}", error).contains("noise"), "{:#}", error);
    }

    #[test]
    fn the_csv_example_still_compiles() {
        let spec = compile_spec_file(std::path::Path::new("examples/basic.yaml"))
            .expect("the console example compiles, its script found beside it");
        let rows = generate_rows(&spec, 50);
        assert_eq!(rows.len(), 50);
        // It writes CSV, so every visible field must render as text.
        for ctx in &rows {
            for index in spec.output_order.iter() {
                let field = &spec.fields[*index];
                ctx.get(&field.name).expect("value present").csv_string("");
            }
        }
    }
}
