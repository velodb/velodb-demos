//! Every field generator, and the helpers they share.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use anyhow::{anyhow, bail, Context, Result};
use base64::{engine::general_purpose, Engine as _};
use chrono::{DateTime, NaiveDateTime, TimeZone, Utc};
use handlebars::Handlebars;
use rand::{distributions::Alphanumeric, prelude::*, rngs::StdRng};
use rust_decimal::prelude::ToPrimitive;
use rust_decimal::Decimal;

use crate::compile::{GeneratorKind, RowContext};
use crate::composite;
use crate::spec::*;
use crate::value::Value;

#[derive(Clone)]
pub enum Generator {
    Constant(ConstantGenerator),
    Sequence(SequenceGenerator),
    SequenceString(SequenceStringGenerator),
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
    JavaScript(JavaScriptGenerator),
    Array(composite::ArrayGenerator),
    Map(composite::MapGenerator),
    Struct(composite::StructGenerator),
    Ipv4(composite::Ipv4Generator),
    Ipv6(composite::Ipv6Generator),
}

impl Generator {
    /// The generators nested inside this one, for checks that must reach
    /// every level: capacity, statefulness.
    /// Tell every JavaScript generator, at any nesting depth, which field
    /// names it may be handed.
    pub fn expose_fields(&mut self, names: &[String]) {
        match self {
            // `deps` says what the function reads, so hand it exactly that.
            // Building the argument object is most of a call's cost: on a
            // 34-field row, narrowing it this way is about 25% of the time.
            // With no deps declared the function gets the whole row.
            Generator::JavaScript(generator) => {
                generator.exposed = if generator.deps.is_empty() {
                    names.to_vec()
                } else {
                    generator.deps.clone()
                }
            }
            Generator::Array(generator) => generator.element.expose_fields(names),
            Generator::Map(generator) => {
                generator.key.expose_fields(names);
                generator.value.expose_fields(names);
            }
            Generator::Struct(generator) => {
                for (_, child) in generator.fields.iter_mut() {
                    child.expose_fields(names);
                }
            }
            _ => {}
        }
    }

    pub fn children(&self) -> Vec<&Generator> {
        match self {
            Generator::Array(generator) => vec![&*generator.element],
            Generator::Map(generator) => vec![&*generator.key, &*generator.value],
            Generator::Struct(generator) => generator.fields.iter().map(|(_, g)| g).collect(),
            _ => Vec::new(),
        }
    }

    /// Refuse a run that would outgrow a fixed-width counter partway through,
    /// rather than letting it discover the ceiling in hour forty.
    pub fn check_capacity(&self, highest_index: u64) -> Result<()> {
        for child in self.children() {
            // A counter inside an array advances once per item, not per row,
            // so this under-counts it. It still catches the common case.
            child.check_capacity(highest_index)?;
        }
        let Generator::SequenceString(generator) = self else {
            return Ok(());
        };
        let highest = generator
            .start
            .saturating_add(highest_index.saturating_mul(generator.step));
        if highest > generator.max_counter {
            bail!(
                "this run reaches sequence_string counter {}, past the {} that {} digits \
                 hold. Widen `width` or shorten the template's literal text.",
                highest,
                generator.max_counter,
                generator.digits
            );
        }
        Ok(())
    }

    pub fn kind(&self) -> GeneratorKind {
        match self {
            // `sequence_string` is deliberately absent: it carries state, but
            // its counter is shared and claimed in blocks, so producers stay
            // independent. Listing it here would clamp generation to one
            // thread and cost far more than the UUIDs it replaces.
            Generator::Sequence(_) | Generator::Fluctuating(_) => GeneratorKind::StatefulOrdered,
            // Each producer builds its own JavaScript runtime, so globals are
            // per-thread, not shared. Running on every thread is therefore
            // safe unless the script uses a global to produce values that
            // must be unique or ordered across the whole run, which is why
            // it stays opt-in.
            Generator::JavaScript(generator) => {
                if generator.parallel {
                    GeneratorKind::Stateless
                } else {
                    GeneratorKind::StatefulOrdered
                }
            }
            // Nested generators inherit their children's constraint.
            _ if self
                .children()
                .iter()
                .any(|child| child.kind() == GeneratorKind::StatefulOrdered) =>
            {
                GeneratorKind::StatefulOrdered
            }
            _ => GeneratorKind::Stateless,
        }
    }

    pub fn dependencies(&self) -> &[String] {
        match self {
            Generator::Template(generator) => &generator.dependencies,
            Generator::JavaScript(generator) => &generator.deps,
            Generator::Array(generator) => &generator.dependencies,
            Generator::Map(generator) => &generator.dependencies,
            Generator::Struct(generator) => &generator.dependencies,
            _ => &[],
        }
    }

    pub fn generate(&mut self, ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        match self {
            Generator::Constant(generator) => generator.generate(ctx, rng),
            Generator::Sequence(generator) => generator.generate(ctx, rng),
            Generator::SequenceString(generator) => generator.generate(ctx, rng),
            Generator::Name(generator) => generator.generate(ctx, rng),
            Generator::Email(generator) => generator.generate(ctx, rng),
            Generator::Lorem(generator) => generator.generate(ctx, rng),
            Generator::Address(generator) => generator.generate(ctx, rng),
            Generator::Template(generator) => generator.generate(ctx, rng),
            Generator::IntRange(generator) => generator.generate(ctx, rng),
            Generator::FloatRange(generator) => generator.generate(ctx, rng),
            Generator::DecimalRange(generator) => generator.generate(ctx, rng),
            Generator::Fluctuating(generator) => generator.generate(ctx, rng),
            Generator::DateTimeAround(generator) => generator.generate(ctx, rng),
            Generator::DateTimeRange(generator) => generator.generate(ctx, rng),
            Generator::Choice(generator) => generator.generate(ctx, rng),
            Generator::WeightedChoice(generator) => generator.generate(ctx, rng),
            Generator::Uuid(generator) => generator.generate(ctx, rng),
            Generator::RandomBytes(generator) => generator.generate(ctx, rng),
            Generator::JavaScript(generator) => generator.generate(ctx, rng),
            Generator::Array(generator) => generator.generate(ctx, rng),
            Generator::Map(generator) => generator.generate(ctx, rng),
            Generator::Struct(generator) => generator.generate(ctx, rng),
            Generator::Ipv4(generator) => generator.generate(ctx, rng),
            Generator::Ipv6(generator) => generator.generate(ctx, rng),
        }
    }
}

pub trait FieldGenerator {
    fn generate(&mut self, ctx: &RowContext, rng: &mut StdRng) -> Result<Value>;
}

#[derive(Clone)]
pub struct ConstantGenerator {
    pub value: Value,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for ConstantGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        Ok(self.value.clone())
    }
}

#[derive(Clone)]
pub struct SequenceGenerator {
    pub next: i64,
    pub step: i64,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for SequenceGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let value = self.next;
        self.next = self.next.saturating_add(self.step);
        Ok(Value::I64(value))
    }
}

/// How many distinct counters `digits` decimal characters hold, saturating at
/// `u64::MAX`, which is the counter's own ceiling anyway.
pub fn sequence_string_capacity(digits: usize) -> u64 {
    let mut capacity: u64 = 1;
    for _ in 0..digits {
        match capacity.checked_mul(10) {
            Some(next) => capacity = next,
            None => return u64::MAX,
        }
    }
    capacity
}

/// How many counter values a generator claims from the shared cursor at once.
/// Claiming in blocks keeps the atomic off the per-row path, so the cost per
/// row is a compare and an add.
pub const SEQUENCE_STRING_BLOCK: u64 = 1 << 16;

/// A counter rendered into a fixed-width string, as a cheap stand-in for
/// `uuid` when a column only needs to be unique and the right size. Producing
/// a v4 UUID costs a draw from the OS entropy pool per value; this costs an
/// increment and a format.
///
/// Every clone shares one cursor and claims its own block of the counter, so
/// values stay unique across generator threads. That is what a key column
/// needs, and it is why this is not simply `sequence` with a format applied.
pub struct SequenceStringGenerator {
    /// Literal text before and after the counter, split from the template.
    pub prefix: String,
    pub suffix: String,
    /// Zero-padding width for the counter, so the whole value hits `width`.
    pub digits: usize,
    /// Largest counter that still fits `digits`. Rust's zero-padding widens
    /// rather than truncates, so without this the values would quietly grow a
    /// character partway through a long run instead of failing.
    pub max_counter: u64,
    pub start: u64,
    pub step: u64,
    /// Next unclaimed index, shared by every clone of this generator.
    pub cursor: Arc<AtomicU64>,
    /// The half-open block of indices this instance still owns.
    pub next_index: u64,
    pub block_end: u64,
    pub null_rate: Option<f64>,
}

/// Cloning hands the copy an empty block so it claims indices of its own
/// rather than replaying the ones this instance is partway through.
impl Clone for SequenceStringGenerator {
    fn clone(&self) -> Self {
        Self {
            prefix: self.prefix.clone(),
            suffix: self.suffix.clone(),
            digits: self.digits,
            max_counter: self.max_counter,
            start: self.start,
            step: self.step,
            cursor: self.cursor.clone(),
            next_index: 0,
            block_end: 0,
            null_rate: self.null_rate,
        }
    }
}

impl SequenceStringGenerator {
    pub fn claim_block(&mut self) {
        let first = self
            .cursor
            .fetch_add(SEQUENCE_STRING_BLOCK, Ordering::Relaxed);
        self.next_index = first;
        self.block_end = first.saturating_add(SEQUENCE_STRING_BLOCK);
    }
}

impl FieldGenerator for SequenceStringGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        if self.next_index >= self.block_end {
            self.claim_block();
        }
        let index = self.next_index;
        self.next_index += 1;
        let counter = self.start.saturating_add(index.saturating_mul(self.step));
        if counter > self.max_counter {
            bail!(
                "sequence_string counter {} no longer fits {} digits; widen `width` \
                 or shorten the template's literal text",
                counter,
                self.digits
            );
        }
        Ok(Value::String(format!(
            "{}{:0width$}{}",
            self.prefix,
            counter,
            self.suffix,
            width = self.digits
        )))
    }
}

#[derive(Clone)]
pub struct NameGenerator {
    pub part: NamePart,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for NameGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let first = pick(FIRST_NAMES, rng);
        let last = pick(LAST_NAMES, rng);
        let value = match self.part {
            NamePart::First => first.to_string(),
            NamePart::Last => last.to_string(),
            NamePart::Full => format!("{first} {last}"),
        };
        Ok(Value::String(value))
    }
}

#[derive(Clone)]
pub struct EmailGenerator {
    pub _style: Option<String>,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for EmailGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let local: String = (&mut *rng)
            .sample_iter(&Alphanumeric)
            .take(12)
            .map(char::from)
            .collect::<String>()
            .to_ascii_lowercase();
        Ok(Value::String(format!(
            "{local}@{}",
            pick(EMAIL_DOMAINS, rng)
        )))
    }
}

#[derive(Clone)]
pub struct LoremGenerator {
    pub words_min: usize,
    pub words_max: usize,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for LoremGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        ensure_range(self.words_min, self.words_max, "lorem words")?;
        let count = rng.gen_range(self.words_min..=self.words_max);
        let mut words = Vec::with_capacity(count);
        for _ in 0..count {
            words.push(pick(LOREM_WORDS, rng));
        }
        Ok(Value::String(words.join(" ")))
    }
}

#[derive(Clone)]
pub struct AddressGenerator {
    pub part: AddressPart,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for AddressGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let street = format!(
            "{} {} {}",
            rng.gen_range(100..=9999),
            pick(STREET_NAMES, rng),
            pick(STREET_SUFFIXES, rng)
        );
        let city = pick(CITIES, rng);
        let state = pick(STATES, rng);
        let country = pick(COUNTRIES, rng);
        let postal_code = format!("{:05}", rng.gen_range(10000..=99999));
        let value = match self.part {
            AddressPart::Street => street,
            AddressPart::City => city.to_string(),
            AddressPart::State => state.to_string(),
            AddressPart::Country => country.to_string(),
            AddressPart::PostalCode => postal_code,
            AddressPart::Full => format!("{street}, {city}, {state} {postal_code}, {country}"),
        };
        Ok(Value::String(value))
    }
}

#[derive(Clone)]
pub struct TemplateGenerator {
    pub handlebars: Arc<Handlebars<'static>>,
    pub dependencies: Vec<String>,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for TemplateGenerator {
    fn generate(&mut self, ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let mut data = serde_json::Map::with_capacity(ctx.len());
        for (key, value) in ctx.iter() {
            let json = value.template_value();
            // A dotted field is reachable both ways: `{{[customer.name]}}`
            // by its literal key and `{{customer.name}}` through the nested
            // object built here.
            if key.contains('.') {
                insert_nested(&mut data, key, json.clone());
            }
            data.insert(key.clone(), json);
        }
        let rendered = self.handlebars.render("value", &data)?;
        Ok(Value::String(rendered))
    }
}

/// Place `value` at a dotted path inside `data`, creating objects on the way.
/// An intermediate that is not an object is left alone: that is a field
/// named like the prefix, and the two cannot both exist once validated.
fn insert_nested(data: &mut serde_json::Map<String, serde_json::Value>, path: &str, value: serde_json::Value) {
    let Some((head, rest)) = path.split_once('.') else {
        data.insert(path.to_string(), value);
        return;
    };
    let entry = data
        .entry(head.to_string())
        .or_insert_with(|| serde_json::Value::Object(serde_json::Map::new()));
    if let serde_json::Value::Object(child) = entry {
        insert_nested(child, rest, value);
    }
}

#[derive(Clone)]
pub struct IntRangeGenerator {
    pub min: i128,
    pub max: i128,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for IntRangeGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        ensure_range(self.min, self.max, "int_range")?;
        // Stay on the 64-bit path whenever the range allows: it is faster to
        // draw, and it keeps values ordinary numbers for templates and JS.
        if let (Ok(min), Ok(max)) = (i64::try_from(self.min), i64::try_from(self.max)) {
            return Ok(Value::I64(rng.gen_range(min..=max)));
        }
        let value = rng.gen_range(self.min..=self.max);
        Ok(match i64::try_from(value) {
            Ok(small) => Value::I64(small),
            Err(_) => Value::I128(value),
        })
    }
}

#[derive(Clone)]
pub struct FloatRangeGenerator {
    pub min: f64,
    pub max: f64,
    pub precision: Option<usize>,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for FloatRangeGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        ensure_float_range(self.min, self.max, "float_range")?;
        let mut value = rng.gen_range(self.min..=self.max);
        if let Some(precision) = self.precision {
            value = round_to_precision(value, precision);
        }
        Ok(Value::F64(value))
    }
}

/// Bounds are held as unscaled units at `scale`, converted once when the spec
/// is compiled. Drawing an integer in those units avoids a round trip through
/// f64 and `Decimal`, and avoids rendering a string the Parquet writer would
/// only parse back.
#[derive(Clone)]
pub struct DecimalRangeGenerator {
    pub min_units: i128,
    pub max_units: i128,
    pub scale: u32,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for DecimalRangeGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let units = if self.min_units == self.max_units {
            self.min_units
        } else {
            rng.gen_range(self.min_units..=self.max_units)
        };
        Ok(Value::Decimal {
            units,
            scale: self.scale,
        })
    }
}

#[derive(Clone)]
pub struct FluctuatingGenerator {
    pub data_type: NumericDataType,
    pub current: Decimal,
    pub min: Decimal,
    pub max: Decimal,
    pub direction: i8,
    pub step_min: Decimal,
    pub step_max: Decimal,
    pub noise: Decimal,
    pub flip_chance: f64,
    pub precision: Option<usize>,
    pub scale: Option<u32>,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for FluctuatingGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        if rng.gen_bool(self.flip_chance) {
            self.direction *= -1;
        }
        let min = decimal_to_f64(self.step_min)?;
        let max = decimal_to_f64(self.step_max)?;
        ensure_float_range(min, max, "fluctuating step")?;
        let step = Decimal::from_f64_retain(rng.gen_range(min..=max))
            .ok_or_else(|| anyhow!("failed to generate fluctuating step"))?;
        // The step follows the direction; the noise does not. Once the noise
        // outweighs the step, an upward run produces the occasional drop,
        // which is what real measurements look like.
        let mut delta = if self.direction >= 0 { step } else { -step };
        if !self.noise.is_zero() {
            let spread = decimal_to_f64(self.noise)?;
            delta += Decimal::from_f64_retain(rng.gen_range(-spread..=spread))
                .ok_or_else(|| anyhow!("failed to generate fluctuating noise"))?;
        }
        let next = self.current + delta;
        if next > self.max {
            self.direction = -1;
            self.current = self.max;
        } else if next < self.min {
            self.direction = 1;
            self.current = self.min;
        } else {
            self.current = next;
        }
        self.emit(self.current)
    }
}

impl FluctuatingGenerator {
    /// The value this generator writes for a given position. Shared with
    /// startup validation, so what it checks is exactly what is emitted.
    pub fn emit(&self, current: Decimal) -> Result<Value> {
        match self.data_type {
            NumericDataType::Int => Ok(Value::I64(
                decimal_to_f64(current)?
                    .round()
                    .clamp(i64::MIN as f64, i64::MAX as f64) as i64,
            )),
            NumericDataType::Float | NumericDataType::Double => {
                let mut value = decimal_to_f64(current)?;
                if let Some(precision) = self.precision {
                    value = round_to_precision(value, precision);
                }
                Ok(Value::F64(value))
            }
            NumericDataType::Decimal => {
                let value = self.scale.map(|scale| current.round_dp(scale)).unwrap_or(current);
                Ok(Value::String(value.to_string()))
            }
        }
    }
}

/// Rows between clock reads when the spec gives no explicit `base`. Offsets
/// here span hours or days, so a base that trails real time by the few
/// milliseconds it takes to emit this many rows is invisible in the data, and
/// it removes a clock read per field per row.
///
/// Caching the clock would flatten the sub-second digits, which used to come
/// from `Utc::now()` and gave the column most of its cardinality. The offset
/// is drawn in microseconds rather than seconds to put that back, so the
/// values stay as distinct as they were. That matters: a datetime column that
/// suddenly compresses well would quietly change what a load test measures.
pub const NOW_REFRESH_ROWS: u32 = 4096;

#[derive(Clone)]
pub struct DateTimeAroundGenerator {
    pub base: Option<DateTime<Utc>>,
    pub offset_micros_min: i64,
    pub offset_micros_max: i64,
    pub format: Arc<str>,
    pub cached_now: Option<DateTime<Utc>>,
    pub rows_until_refresh: u32,
    pub null_rate: Option<f64>,
}

impl DateTimeAroundGenerator {
    pub fn base(&mut self) -> DateTime<Utc> {
        if let Some(base) = self.base {
            return base;
        }
        match self.cached_now {
            Some(now) if self.rows_until_refresh > 0 => {
                self.rows_until_refresh -= 1;
                now
            }
            _ => {
                let now = Utc::now();
                self.cached_now = Some(now);
                self.rows_until_refresh = NOW_REFRESH_ROWS;
                now
            }
        }
    }
}

impl FieldGenerator for DateTimeAroundGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let offset = if self.offset_micros_min == self.offset_micros_max {
            self.offset_micros_min
        } else {
            rng.gen_range(self.offset_micros_min..=self.offset_micros_max)
        };
        Ok(Value::Timestamp {
            micros: self.base().timestamp_micros().saturating_add(offset),
            format: self.format.clone(),
        })
    }
}

#[derive(Clone)]
pub struct DateTimeRangeGenerator {
    pub start_seconds: i64,
    pub end_seconds: i64,
    pub format: Arc<str>,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for DateTimeRangeGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let seconds = if self.start_seconds == self.end_seconds {
            self.start_seconds
        } else {
            rng.gen_range(self.start_seconds..=self.end_seconds)
        };
        Ok(Value::Timestamp {
            micros: seconds.saturating_mul(1_000_000),
            format: self.format.clone(),
        })
    }
}

#[derive(Clone)]
pub struct ChoiceGenerator {
    pub values: Vec<Value>,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for ChoiceGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        if self.values.is_empty() {
            bail!("choice requires at least one value");
        }
        Ok(self.values[rng.gen_range(0..self.values.len())].clone())
    }
}

#[derive(Clone)]
pub struct WeightedChoiceGenerator {
    pub values: Vec<WeightedValue>,
    pub total_weight: f64,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for WeightedChoiceGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        if self.values.is_empty() || self.total_weight <= 0.0 {
            bail!("weighted_choice requires positive weighted values");
        }
        let mut threshold = rng.gen_range(0.0..self.total_weight);
        for entry in &self.values {
            threshold -= entry.weight;
            if threshold <= 0.0 {
                return Ok(entry.value.clone());
            }
        }
        Ok(self.values[self.values.len() - 1].value.clone())
    }
}

#[derive(Clone)]
pub struct UuidGenerator {
    pub null_rate: Option<f64>,
}

impl FieldGenerator for UuidGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        // `Uuid::new_v4` draws from the OS entropy pool, which is a getrandom
        // syscall per value on any kernel without the vDSO entry point added
        // in 6.11. A spec with three UUID columns made three syscalls a row.
        // The generator is already handed a ChaCha12 stream seeded from that
        // same pool once per thread, so take the sixteen bytes from there: the
        // result is a v4 UUID by the same construction, just without the trip
        // into the kernel.
        let mut bytes = [0u8; 16];
        rng.fill_bytes(&mut bytes);
        let uuid = uuid::Builder::from_random_bytes(bytes).into_uuid();
        // encode_lower writes into a stack buffer. `to_string` would go
        // through Display and the formatting machinery to reach the same 36
        // characters.
        let mut buffer = [0u8; uuid::fmt::Hyphenated::LENGTH];
        Ok(Value::String(
            uuid.hyphenated().encode_lower(&mut buffer).to_string(),
        ))
    }
}

#[derive(Clone)]
pub struct RandomBytesGenerator {
    pub min_bytes: usize,
    pub max_bytes: usize,
    pub encoding: RandomBytesEncoding,
    pub null_rate: Option<f64>,
}

impl FieldGenerator for RandomBytesGenerator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        ensure_range(self.min_bytes, self.max_bytes, "random_bytes length")?;
        let len = rng.gen_range(self.min_bytes..=self.max_bytes);
        let mut bytes = vec![0u8; len];
        rng.fill_bytes(&mut bytes);
        let value = match self.encoding {
            RandomBytesEncoding::Hex => hex_encode(&bytes),
            RandomBytesEncoding::Base64 => general_purpose::STANDARD.encode(bytes),
            RandomBytesEncoding::Base64url => general_purpose::URL_SAFE_NO_PAD.encode(bytes),
        };
        Ok(Value::String(value))
    }
}

pub struct JsState {
    pub context: rquickjs::Context,
    /// The function handle, resolved once. Looking it up on the global object
    /// every row costs an atom lookup and a wrapper for no gain.
    pub function: rquickjs::Persistent<rquickjs::Function<'static>>,
    /// Field names, interned once. `Object::set` interns its key on every
    /// call otherwise, which for a wide row is most of the per-row cost.
    pub keys: Vec<(String, rquickjs::Persistent<rquickjs::Atom<'static>>)>,
    // Runtime must outlive Context; declare it after so it is dropped after.
    pub _runtime: rquickjs::Runtime,
}

impl JsState {
    pub fn new(source: &str, function_name: &str, exposed: &[String]) -> Result<Self> {
        let runtime =
            rquickjs::Runtime::new().context("failed to create JavaScript runtime")?;
        let context =
            rquickjs::Context::full(&runtime).context("failed to create JavaScript context")?;
        let (function, keys) = context
            .with(|ctx| -> rquickjs::Result<_> {
                ctx.eval::<(), _>(source)?;
                let function: rquickjs::Function = ctx.globals().get(function_name)?;
                let mut keys = Vec::with_capacity(exposed.len());
                for name in exposed {
                    let atom = rquickjs::Atom::from_str(ctx.clone(), name)?;
                    keys.push((name.clone(), rquickjs::Persistent::save(&ctx, atom)));
                }
                Ok((rquickjs::Persistent::save(&ctx, function), keys))
            })
            .with_context(|| {
                format!("failed to evaluate the JavaScript file or find `{}`", function_name)
            })?;
        Ok(Self {
            context,
            function,
            keys,
            _runtime: runtime,
        })
    }
}

pub struct JavaScriptGenerator {
    pub function_name: String,
    pub script_source: Arc<String>,
    pub deps: Vec<String>,
    /// Field names handed to the function, in a stable order.
    pub exposed: Vec<String>,
    /// Run on every generator thread. Each thread gets its own runtime and
    /// its own globals, so only scripts whose globals must be unique or
    /// ordered across the run need the default single thread.
    pub parallel: bool,
    pub null_rate: Option<f64>,
    // Initialized on first generate() call; each producer clone starts with None.
    pub state: Option<Box<JsState>>,
}

// SAFETY: JavaScriptGenerator is StatefulOrdered (forces single-producer execution).
// Only one task ever holds a JavaScriptGenerator, generate() is synchronous with no
// await points, so the QuickJS runtime is never touched from two threads simultaneously.
// rquickjs's !Send/!Sync comes from Rc<> reference counting, not thread-unsafe global
// state. With exclusive single-producer access this is safe.
unsafe impl Send for JsState {}
unsafe impl Sync for JsState {}
unsafe impl Send for JavaScriptGenerator {}
unsafe impl Sync for JavaScriptGenerator {}

impl Clone for JavaScriptGenerator {
    fn clone(&self) -> Self {
        Self {
            function_name: self.function_name.clone(),
            script_source: Arc::clone(&self.script_source),
            deps: self.deps.clone(),
            exposed: self.exposed.clone(),
            parallel: self.parallel,
            null_rate: self.null_rate,
            // Each producer builds its own runtime on first use.
            state: None,
        }
    }
}

impl FieldGenerator for JavaScriptGenerator {
    fn generate(&mut self, ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        if self.state.is_none() {
            // The script is read, parsed and evaluated once per producer, not
            // once per row; only the call below repeats.
            self.state = Some(Box::new(JsState::new(
                &self.script_source,
                &self.function_name,
                &self.exposed,
            )?));
        }
        let state = self.state.as_mut().unwrap();
        let exposed = &self.exposed;
        state
            .context
            .with(|js_ctx| {
                let func = state.function.clone().restore(&js_ctx)?;
                let obj = rquickjs::Object::new(js_ctx.clone())?;
                for (key, atom) in &state.keys {
                    let Some(val) = ctx.get(key) else { continue };
                    let atom = atom.clone().restore(&js_ctx)?;
                    match val {
                        Value::Null => {} // appears as undefined in JS
                        Value::Bool(b) => obj.set(atom, *b)?,
                        // JavaScript numbers are doubles: integers past 2^53
                        // are not exact. Hand those over as text instead of
                        // quietly rounding a BIGINT or LARGEINT.
                        Value::I64(n) if n.unsigned_abs() <= (1u64 << 53) => {
                            obj.set(atom, *n as f64)?
                        }
                        Value::I64(n) => obj.set(atom, n.to_string().as_str())?,
                        Value::F64(f) => obj.set(atom, *f)?,
                        Value::String(s) => obj.set(atom, s.as_str())?,
                        // JS has no decimal or timestamp type, and its numbers
                        // lose integers past 2^53, so hand these over as the
                        // text a template would have seen. Nested values
                        // arrive as JSON text: JSON.parse(ctx.field).
                        Value::Timestamp { .. }
                        | Value::Decimal { .. }
                        | Value::I128(_)
                        | Value::List(_)
                        | Value::Map(_)
                        | Value::Struct(_) => {
                            obj.set(atom, val.csv_string("").as_str())?
                        }
                    }
                }
                let _ = exposed;
                let result: rquickjs::Value = func.call((obj,))?;
                // Coerce every primitive return type to String.
                let s = if result.is_null() || result.is_undefined() {
                    String::new()
                } else if let Some(b) = result.as_bool() {
                    b.to_string()
                } else if let Some(n) = result.as_int() {
                    n.to_string()
                } else if let Some(f) = result.as_float() {
                    f.to_string()
                } else if let Some(js_str) = result.as_string() {
                    js_str.to_string()?
                } else {
                    return Err(rquickjs::Error::Unknown);
                };
                Ok(Value::String(s))
            })
            .map_err(|e| anyhow!("JavaScript function '{}' failed: {e}", self.function_name))
    }
}

pub fn should_emit_null(null_rate: Option<f64>, rng: &mut StdRng) -> Result<bool> {
    let Some(rate) = null_rate else {
        return Ok(false);
    };
    if !(0.0..=1.0).contains(&rate) {
        bail!("null_rate must be between 0.0 and 1.0");
    }
    Ok(rng.gen_bool(rate))
}

pub fn ensure_range<T>(min: T, max: T, name: &str) -> Result<()>
where
    T: PartialOrd + std::fmt::Display,
{
    if min > max {
        bail!("{name} min must be less than or equal to max");
    }
    Ok(())
}

pub fn ensure_float_range(min: f64, max: f64, name: &str) -> Result<()> {
    if !min.is_finite() || !max.is_finite() {
        bail!("{name} bounds must be finite");
    }
    ensure_range(min, max, name)
}

pub fn ensure_decimal_range(min: Decimal, max: Decimal, name: &str) -> Result<()> {
    if min > max {
        bail!("{name} min must be less than or equal to max");
    }
    Ok(())
}

pub fn parse_datetime(value: &str) -> Result<DateTime<Utc>> {
    if let Ok(dt) = DateTime::parse_from_rfc3339(value) {
        return Ok(dt.with_timezone(&Utc));
    }
    let naive = NaiveDateTime::parse_from_str(value, "%Y-%m-%d %H:%M:%S")
        .with_context(|| format!("invalid datetime '{value}'"))?;
    Ok(DateTime::<Utc>::from_naive_utc_and_offset(naive, Utc))
}

pub fn format_datetime(dt: DateTime<Utc>, format: &str) -> String {
    dt.format(format).to_string()
}

/// Render epoch microseconds with a strftime pattern. Only the text outputs
/// call this; the Parquet writer wants the number.
pub fn format_timestamp_micros(micros: i64, format: &str) -> String {
    match Utc.timestamp_micros(micros).single() {
        Some(dt) => format_datetime(dt, format),
        // Unreachable for values a generator produced, and a panic here would
        // take down a writer thread over one row.
        None => String::new(),
    }
}

/// Render a fixed-point decimal without building a `Decimal` to do it.
pub fn format_decimal_units(units: i128, scale: u32) -> String {
    if scale == 0 {
        return units.to_string();
    }
    let magnitude = units.unsigned_abs();
    let divisor = 10u128.pow(scale);
    format!(
        "{}{}.{:0width$}",
        if units < 0 { "-" } else { "" },
        magnitude / divisor,
        magnitude % divisor,
        width = scale as usize
    )
}

/// Convert a spec's decimal bound into unscaled units at `scale`, once, so the
/// per-row path is an integer draw.
pub fn decimal_to_units(value: Decimal, scale: u32, name: &str) -> Result<i128> {
    let factor = Decimal::from_i128_with_scale(10i128.pow(scale), 0);
    (value.round_dp(scale) * factor)
        .round()
        .to_i128()
        .ok_or_else(|| anyhow!("{} bound {} does not fit a 128-bit decimal", name, value))
}

/// `rust_decimal` carries at most this many fractional digits.
pub const MAX_DECIMAL_SCALE: u32 = 28;

pub fn seconds_to_micros(seconds: i64) -> Result<i64> {
    seconds
        .checked_mul(1_000_000)
        .ok_or_else(|| anyhow!("offset of {} seconds is too large to express in microseconds", seconds))
}

pub fn decimal_to_f64(value: Decimal) -> Result<f64> {
    value
        .to_string()
        .parse::<f64>()
        .context("failed to convert decimal to f64")
}

pub fn round_to_precision(value: f64, precision: usize) -> f64 {
    let factor = 10_f64.powi(precision as i32);
    (value * factor).round() / factor
}

pub fn title_case(value: &str) -> String {
    value
        .split_whitespace()
        .map(|word| {
            let mut chars = word.chars();
            match chars.next() {
                Some(first) => {
                    first.to_uppercase().collect::<String>() + &chars.as_str().to_ascii_lowercase()
                }
                None => String::new(),
            }
        })
        .collect::<Vec<_>>()
        .join(" ")
}

pub fn slug(value: &str) -> String {
    let mut output = String::new();
    let mut last_dash = false;
    for ch in value.chars().flat_map(char::to_lowercase) {
        if ch.is_ascii_alphanumeric() {
            output.push(ch);
            last_dash = false;
        } else if !last_dash && !output.is_empty() {
            output.push('-');
            last_dash = true;
        }
    }
    output.trim_matches('-').to_string()
}

pub fn hex_encode(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(bytes.len() * 2);
    for &byte in bytes {
        output.push(HEX[(byte >> 4) as usize] as char);
        output.push(HEX[(byte & 0x0f) as usize] as char);
    }
    output
}

pub fn char_to_single_byte(ch: char, name: &str) -> Result<u8> {
    if ch.len_utf8() != 1 {
        bail!("{name} must be a single-byte character");
    }
    Ok(ch as u8)
}

pub fn pick<'a>(values: &'a [&'a str], rng: &mut StdRng) -> &'a str {
    values[rng.gen_range(0..values.len())]
}

pub const FIRST_NAMES: &[&str] = &[
    "Adam",
    "Ava",
    "Benjamin",
    "Charlotte",
    "Daniel",
    "Evelyn",
    "Henry",
    "Liam",
    "Mia",
    "Sophia",
    "Zoe",
];

pub const LAST_NAMES: &[&str] = &[
    "Anderson", "Brown", "Chen", "Davis", "Garcia", "Johnson", "Miller", "Smith", "Taylor",
    "Wilson",
];

pub const EMAIL_DOMAINS: &[&str] = &["example.com", "example.net", "example.org"];
pub const STREET_NAMES: &[&str] = &[
    "Cedar", "Elm", "Harbor", "Maple", "Oak", "Pine", "River", "Sunset",
];
pub const STREET_SUFFIXES: &[&str] = &["Avenue", "Boulevard", "Drive", "Lane", "Road", "Street"];
pub const CITIES: &[&str] = &[
    "Singapore",
    "Tokyo",
    "Sydney",
    "San Francisco",
    "London",
    "Berlin",
];
pub const STATES: &[&str] = &["CA", "NY", "TX", "WA", "ON", "NSW"];
pub const COUNTRIES: &[&str] = &[
    "USA",
    "Singapore",
    "Japan",
    "Australia",
    "Germany",
    "United Kingdom",
];
pub const LOREM_WORDS: &[&str] = &[
    "alpha", "bravo", "cedar", "delta", "ember", "forest", "harbor", "indigo", "juniper", "kernel",
    "lunar", "matrix", "north", "orbit", "prairie", "quartz", "river", "signal", "thunder",
    "violet",
];

