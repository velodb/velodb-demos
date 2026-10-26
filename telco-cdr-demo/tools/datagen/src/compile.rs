//! From a parsed spec to something that can generate rows: generator
//! construction, the field dependency graph, and the row context.

use std::collections::{HashMap, HashSet};
use std::sync::atomic::AtomicU64;
use std::sync::Arc;

use anyhow::{bail, Context, Result};
use handlebars::{
    Context as HbContext, Handlebars, Helper, HelperResult, JsonRender, Output, RenderContext,
    RenderError, RenderErrorReason,
};
use rand::{thread_rng, Rng};

use crate::composite;
use crate::generators::*;
use crate::spec::*;
use crate::value::Value;

pub type RowContext = HashMap<String, Value>;
#[derive(Clone)]
pub struct CompiledSpec {
    pub csv: CsvSpecRuntime,
    pub context_reset: ContextReset,
    pub batch_rows: usize,
    pub fields: Arc<Vec<CompiledField>>,
    pub generation_order: Arc<Vec<usize>>,
    pub output_order: Arc<Vec<usize>>,
    pub has_stateful_ordered: bool,
}

impl CompiledSpec {
    /// Check every fixed-width counter against the highest index this run can
    /// reach. Generator threads claim counter values in blocks and abandon
    /// whatever they have not used, so allow one unfinished block each.
    pub fn check_sequence_capacity(&self, rows: Option<u64>, threads: usize) -> Result<()> {
        // Size- and time-bounded runs have no row count to check against; the
        // per-row guard still stops them, it just cannot warn up front.
        let Some(rows) = rows else {
            return Ok(());
        };
        // A thread reaches its highest index only after every other thread has
        // claimed a block ahead of it, so the waste is bounded by the blocks
        // those `threads - 1` others hold, not by one block each.
        let slack = (threads.saturating_sub(1) as u64).saturating_mul(SEQUENCE_STRING_BLOCK);
        let highest_index = rows.saturating_sub(1).saturating_add(slack);
        for field in self.fields.iter() {
            field
                .generator
                .check_capacity(highest_index)
                .with_context(|| format!("field `{}`", field.name))?;
        }
        Ok(())
    }
}

#[derive(Clone)]
pub struct CsvSpecRuntime {
    pub delimiter: u8,
    pub quote: u8,
    pub escape: u8,
    pub newline: Vec<u8>,
    pub null: String,
}

#[derive(Clone)]
pub struct CompiledField {
    pub name: String,
    pub hidden: bool,
    pub order: i64,
    pub definition_index: usize,
    pub generator: Generator,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum GeneratorKind {
    Stateless,
    StatefulOrdered,
}

pub fn compile_spec(raw: RawSpec) -> Result<CompiledSpec> {
    if raw.version != 1 {
        bail!(
            "unsupported spec version {}; only version 1 is supported",
            raw.version
        );
    }
    if raw.fields.is_empty() {
        bail!("spec must define at least one field");
    }

    let csv = compile_csv(raw.csv)?;
    let mut seen = HashSet::new();
    let mut fields = Vec::with_capacity(raw.fields.len());
    for (definition_index, field) in raw.fields.into_iter().enumerate() {
        if field.name.trim().is_empty() {
            bail!("field at index {definition_index} has an empty name");
        }
        if !seen.insert(field.name.clone()) {
            bail!("duplicate field name '{}'", field.name);
        }
        let generator = compile_generator(field.gen)
            .with_context(|| format!("failed to compile field '{}'", field.name))?;
        fields.push(CompiledField {
            name: field.name,
            hidden: field.hidden,
            order: field.order,
            definition_index,
            generator,
        });
    }

    // A JavaScript function sees every field generated before it, so hand it
    // the whole field list. Interning these names once, here, is what keeps
    // the per-row cost off the hot path.
    let all_names: Vec<String> = fields.iter().map(|field| field.name.clone()).collect();
    for field in fields.iter_mut() {
        field.generator.expose_fields(&all_names);
    }

    let generation_order = build_generation_order(&fields)?;
    let mut output_order: Vec<usize> = fields
        .iter()
        .enumerate()
        .filter_map(|(index, field)| (!field.hidden).then_some(index))
        .collect();
    output_order.sort_by_key(|&index| (fields[index].order, fields[index].definition_index));
    let has_stateful_ordered = fields
        .iter()
        .any(|field| field.generator.kind() == GeneratorKind::StatefulOrdered);

    Ok(CompiledSpec {
        csv,
        context_reset: raw.context.reset,
        batch_rows: raw.batch.rows.max(1),
        fields: Arc::new(fields),
        generation_order: Arc::new(generation_order),
        output_order: Arc::new(output_order),
        has_stateful_ordered,
    })
}

pub fn compile_csv(spec: CsvSpec) -> Result<CsvSpecRuntime> {
    let delimiter = char_to_single_byte(spec.delimiter, "csv.delimiter")?;
    let quote = char_to_single_byte(spec.quote, "csv.quote")?;
    let escape = char_to_single_byte(spec.escape, "csv.escape")?;
    Ok(CsvSpecRuntime {
        delimiter,
        quote,
        escape,
        newline: spec.newline.into_bytes(),
        null: spec.null,
    })
}

pub fn compile_generator(spec: GeneratorSpec) -> Result<Generator> {
    let generator = match spec {
        GeneratorSpec::Constant(spec) => Generator::Constant(ConstantGenerator {
            value: spec.value,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::Sequence(spec) => Generator::Sequence(SequenceGenerator {
            next: spec.start,
            step: spec.step,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::SequenceString(spec) => {
            let (prefix, suffix) = split_sequence_template(&spec.template)?;
            let literal = prefix.len() + suffix.len();
            if literal > spec.width {
                bail!(
                    "sequence_string template `{}` has {} literal characters, \
                     which does not fit width {}",
                    spec.template,
                    literal,
                    spec.width
                );
            }
            if spec.step == 0 {
                bail!("sequence_string step must be at least 1");
            }
            let digits = spec.width - literal;
            if digits == 0 {
                bail!(
                    "sequence_string template `{}` fills the whole width of {}, \
                     leaving no room for the counter",
                    spec.template,
                    spec.width
                );
            }
            Generator::SequenceString(SequenceStringGenerator {
                prefix,
                suffix,
                digits,
                max_counter: sequence_string_capacity(digits).saturating_sub(1),
                start: spec.start,
                step: spec.step,
                cursor: Arc::new(AtomicU64::new(0)),
                next_index: 0,
                block_end: 0,
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::Name(spec) => Generator::Name(NameGenerator {
            part: spec.part,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::Email(spec) => Generator::Email(EmailGenerator {
            _style: spec.style,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::Lorem(spec) => Generator::Lorem(LoremGenerator {
            words_min: spec.words_min,
            words_max: spec.words_max,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::Address(spec) => Generator::Address(AddressGenerator {
            part: spec.part,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::Template(spec) => {
            let mut handlebars = handlebars();
            handlebars
                .register_template_string("value", &spec.value)
                .context("invalid Handlebars template")?;
            let dependencies = extract_template_dependencies(&spec.value)?;
            Generator::Template(TemplateGenerator {
                handlebars: Arc::new(handlebars),
                dependencies,
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::IntRange(spec) => Generator::IntRange(IntRangeGenerator {
            min: spec.min,
            max: spec.max,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::FloatRange(spec) => Generator::FloatRange(FloatRangeGenerator {
            min: spec.min,
            max: spec.max,
            precision: spec.precision,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::DecimalRange(spec) => {
            ensure_decimal_range(spec.min, spec.max, "decimal_range")?;
            if spec.scale > MAX_DECIMAL_SCALE {
                bail!(
                    "decimal_range scale {} is above the maximum of {}",
                    spec.scale,
                    MAX_DECIMAL_SCALE
                );
            }
            Generator::DecimalRange(DecimalRangeGenerator {
                min_units: decimal_to_units(spec.min, spec.scale, "decimal_range min")?,
                max_units: decimal_to_units(spec.max, spec.scale, "decimal_range max")?,
                scale: spec.scale,
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::Fluctuating(spec) => {
            ensure_decimal_range(spec.min, spec.max, "fluctuating")?;
            ensure_decimal_range(spec.step_min, spec.step_max, "fluctuating step")?;
            if spec.noise.is_sign_negative() {
                bail!("fluctuating noise must not be negative; it is applied as -noise..=noise");
            }
            if !(0.0..=1.0).contains(&spec.flip_chance) {
                bail!("fluctuating flip_chance must be between 0.0 and 1.0");
            }
            let direction = match spec.initial_direction {
                InitialDirection::Up => 1,
                InitialDirection::Down => -1,
                InitialDirection::Random => {
                    if thread_rng().gen_bool(0.5) {
                        1
                    } else {
                        -1
                    }
                }
            };
            Generator::Fluctuating(FluctuatingGenerator {
                data_type: spec.data_type,
                current: spec.start,
                min: spec.min,
                max: spec.max,
                direction,
                step_min: spec.step_min,
                step_max: spec.step_max,
                noise: spec.noise,
                flip_chance: spec.flip_chance,
                precision: spec.precision,
                scale: spec.scale,
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::DateTimeAround(spec) => {
            ensure_range(
                spec.offset_seconds_min,
                spec.offset_seconds_max,
                "datetime offset",
            )?;
            Generator::DateTimeAround(DateTimeAroundGenerator {
                base: spec.base.as_deref().map(parse_datetime).transpose()?,
                offset_micros_min: seconds_to_micros(spec.offset_seconds_min)?,
                offset_micros_max: seconds_to_micros(spec.offset_seconds_max)?,
                format: Arc::from(spec.format),
                cached_now: None,
                rows_until_refresh: 0,
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::DateTimeRange(spec) => {
            let start_seconds = parse_datetime(&spec.start)?.timestamp();
            let end_seconds = parse_datetime(&spec.end)?.timestamp();
            ensure_range(start_seconds, end_seconds, "datetime_range")?;
            Generator::DateTimeRange(DateTimeRangeGenerator {
                start_seconds,
                end_seconds,
                format: Arc::from(spec.format),
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::Choice(spec) => Generator::Choice(ChoiceGenerator {
            values: spec.values,
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::WeightedChoice(spec) => {
            let total_weight = spec.values.iter().try_fold(0.0, |acc, entry| {
                if !entry.weight.is_finite() || entry.weight <= 0.0 {
                    bail!("weighted_choice weights must be positive finite numbers");
                }
                Ok(acc + entry.weight)
            })?;
            Generator::WeightedChoice(WeightedChoiceGenerator {
                values: spec.values,
                total_weight,
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::Array(spec) => {
            if spec.min_len > spec.max_len {
                bail!("array min_len {} exceeds max_len {}", spec.min_len, spec.max_len);
            }
            let element = compile_generator(*spec.element).context("array element")?;
            let dependencies = element.dependencies().to_vec();
            Generator::Array(composite::ArrayGenerator {
                element: Box::new(element),
                min_len: spec.min_len,
                max_len: spec.max_len,
                null_rate: spec.null_rate,
                dependencies,
            })
        }
        GeneratorSpec::Map(spec) => {
            if spec.min_len > spec.max_len {
                bail!("map min_len {} exceeds max_len {}", spec.min_len, spec.max_len);
            }
            let key = compile_generator(*spec.key).context("map key")?;
            let value = compile_generator(*spec.value).context("map value")?;
            let dependencies = merge_dependencies([&key, &value]);
            Generator::Map(composite::MapGenerator {
                key: Box::new(key),
                value: Box::new(value),
                min_len: spec.min_len,
                max_len: spec.max_len,
                null_rate: spec.null_rate,
                dependencies,
            })
        }
        GeneratorSpec::Struct(spec) => {
            if spec.fields.is_empty() {
                bail!("struct needs at least one field");
            }
            let mut fields = Vec::with_capacity(spec.fields.len());
            for field in spec.fields {
                if fields.iter().any(|(name, _): &(String, Generator)| name == &field.name) {
                    bail!("struct declares field `{}` twice", field.name);
                }
                let generator = compile_generator(field.gen)
                    .with_context(|| format!("struct field `{}`", field.name))?;
                fields.push((field.name, generator));
            }
            let dependencies = merge_dependencies(fields.iter().map(|(_, g)| g));
            Generator::Struct(composite::StructGenerator {
                fields,
                null_rate: spec.null_rate,
                dependencies,
            })
        }
        GeneratorSpec::Ipv4(spec) => Generator::Ipv4(composite::Ipv4Generator::new(
            spec.cidr.as_deref(),
            spec.null_rate,
        )?),
        GeneratorSpec::Ipv6(spec) => Generator::Ipv6(composite::Ipv6Generator::new(
            spec.cidr.as_deref(),
            spec.null_rate,
        )?),
        GeneratorSpec::Uuid(spec) => Generator::Uuid(UuidGenerator {
            null_rate: spec.null_rate,
        }),
        GeneratorSpec::RandomBytes(spec) => {
            ensure_range(spec.min_bytes, spec.max_bytes, "random_bytes length")?;
            Generator::RandomBytes(RandomBytesGenerator {
                min_bytes: spec.min_bytes,
                max_bytes: spec.max_bytes,
                encoding: spec.encoding,
                null_rate: spec.null_rate,
            })
        }
        GeneratorSpec::JavaScript(spec) => {
            let script_source = std::fs::read_to_string(&spec.file)
                .with_context(|| format!("failed to read JavaScript file '{}'", spec.file))?;
            Generator::JavaScript(JavaScriptGenerator {
                function_name: spec.function,
                script_source: Arc::new(script_source),
                deps: spec.deps,
                parallel: spec.parallel,
                null_rate: spec.null_rate,
                state: None,
                // Filled in by compile_spec, which knows every field name.
                exposed: Vec::new(),
            })
        }
    };
    Ok(generator)
}

pub fn build_generation_order(fields: &[CompiledField]) -> Result<Vec<usize>> {
    let name_to_index: HashMap<_, _> = fields
        .iter()
        .enumerate()
        .map(|(index, field)| (field.name.as_str(), index))
        .collect();
    let mut indegree = vec![0usize; fields.len()];
    let mut outgoing = vec![Vec::new(); fields.len()];

    for (field_index, field) in fields.iter().enumerate() {
        for dependency in field.generator.dependencies() {
            // `customer.name` may be a field of that exact name, or a lookup
            // into a struct-valued field called `customer`: try the full
            // path first, then each shorter prefix.
            let resolved = std::iter::successors(Some(dependency.as_str()), |path| {
                path.rsplit_once('.').map(|(prefix, _)| prefix)
            })
            .find_map(|path| name_to_index.get(path).copied());
            let Some(dependency_index) = resolved else {
                bail!(
                    "field '{}' references missing dependency '{}'",
                    field.name,
                    dependency
                );
            };
            if dependency_index == field_index {
                bail!("field '{}' depends on itself", field.name);
            }
            if outgoing[dependency_index].contains(&field_index) {
                continue;
            }
            indegree[field_index] += 1;
            outgoing[dependency_index].push(field_index);
        }
    }

    // Priority queue: among simultaneously-ready fields, prefer lower order then earlier definition.
    // std::collections::BinaryHeap is a max-heap, so negate the keys.
    use std::collections::BinaryHeap;
    use std::cmp::Reverse;
    let mut ready: BinaryHeap<Reverse<(i64, usize, usize)>> = indegree
        .iter()
        .enumerate()
        .filter_map(|(index, &count)| {
            (count == 0)
                .then_some(Reverse((fields[index].order, fields[index].definition_index, index)))
        })
        .collect();
    let mut order = Vec::with_capacity(fields.len());

    while let Some(Reverse((_, _, index))) = ready.pop() {
        order.push(index);
        for &dependent in &outgoing[index] {
            indegree[dependent] -= 1;
            if indegree[dependent] == 0 {
                let f = &fields[dependent];
                ready.push(Reverse((f.order, f.definition_index, dependent)));
            }
        }
    }

    if order.len() != fields.len() {
        let cyclic = indegree
            .iter()
            .enumerate()
            .filter_map(|(index, &count)| (count > 0).then_some(fields[index].name.as_str()))
            .collect::<Vec<_>>()
            .join(", ");
        bail!("field dependency DAG cannot be satisfied; possible cycle involving: {cyclic}");
    }

    Ok(order)
}

pub fn handlebars() -> Handlebars<'static> {
    let mut handlebars = Handlebars::new();
    handlebars.set_strict_mode(true);
    handlebars.register_helper(
        "lower",
        Box::new(string_helper(|value| value.to_ascii_lowercase())),
    );
    handlebars.register_helper(
        "upper",
        Box::new(string_helper(|value| value.to_ascii_uppercase())),
    );
    handlebars.register_helper("title", Box::new(string_helper(title_case)));
    handlebars.register_helper("slug", Box::new(string_helper(slug)));
    handlebars
}

pub fn string_helper<F>(
    transform: F,
) -> impl Fn(&Helper, &Handlebars, &HbContext, &mut RenderContext, &mut dyn Output) -> HelperResult
       + Send
       + Sync
       + 'static
where
    F: Fn(&str) -> String + Send + Sync + 'static,
{
    move |h: &Helper,
          _r: &Handlebars,
          _ctx: &HbContext,
          _rc: &mut RenderContext,
          out: &mut dyn Output| {
        let value = h
            .param(0)
            .ok_or_else(|| {
                RenderError::from(RenderErrorReason::Other("missing helper parameter".into()))
            })?
            .value()
            .render();
        out.write(&transform(&value))?;
        Ok(())
    }
}

pub fn extract_template_dependencies(template: &str) -> Result<Vec<String>> {
    let mut deps = HashSet::new();
    let mut rest = template;
    while let Some(start) = rest.find("{{") {
        rest = &rest[start + 2..];
        let Some(end) = rest.find("}}") else {
            bail!("unclosed template expression");
        };
        let expr = rest[..end].trim();
        rest = &rest[end + 2..];
        if expr.is_empty()
            || expr.starts_with('#')
            || expr.starts_with('/')
            || expr.starts_with('!')
        {
            continue;
        }
        let parts = expr.split_whitespace().collect::<Vec<_>>();
        let dep = match parts.as_slice() {
            [single] => *single,
            [helper, field] if matches!(*helper, "lower" | "upper" | "title" | "slug") => *field,
            _ => continue,
        };
        let dep = dep.trim_matches(|ch| ch == '"' || ch == '\'');
        // `{{[customer.name]}}` is Handlebars' literal-segment syntax; the
        // brackets are not part of the name.
        let dep = dep.trim_start_matches('[').trim_end_matches(']');
        if is_field_path(dep) {
            deps.insert(dep.to_string());
        }
    }
    let mut deps = deps.into_iter().collect::<Vec<_>>();
    deps.sort();
    Ok(deps)
}

pub fn is_identifier(value: &str) -> bool {
    let mut chars = value.chars();
    let Some(first) = chars.next() else {
        return false;
    };
    (first == '_' || first.is_ascii_alphabetic())
        && chars.all(|ch| ch == '_' || ch.is_ascii_alphanumeric())
}

/// An identifier, or identifiers joined by dots: `id`, `customer.name`.
pub fn is_field_path(value: &str) -> bool {
    !value.is_empty() && value.split('.').all(is_identifier)
}

/// Every field a set of generators reads from the row, without repeats.
pub fn merge_dependencies<'a>(generators: impl IntoIterator<Item = &'a Generator>) -> Vec<String> {
    let mut merged: Vec<String> = Vec::new();
    for generator in generators {
        for dependency in generator.dependencies() {
            if !merged.contains(dependency) {
                merged.push(dependency.clone());
            }
        }
    }
    merged
}

