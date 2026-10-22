//! The YAML spec: what a user writes to describe rows.

use anyhow::{anyhow, bail, Result};
use rust_decimal::Decimal;
use serde::Deserialize;

use crate::value::Value;

#[derive(Debug, Deserialize)]
pub struct RawSpec {
    pub version: u32,
    #[serde(default)]
    pub csv: CsvSpec,
    #[serde(default)]
    pub context: ContextSpec,
    #[serde(default)]
    pub batch: BatchSpec,
    pub fields: Vec<FieldSpec>,
}

#[derive(Debug, Deserialize)]
pub struct CsvSpec {
    #[serde(default = "default_delimiter")]
    pub delimiter: char,
    #[serde(default = "default_quote")]
    pub quote: char,
    #[serde(default = "default_escape")]
    pub escape: char,
    #[serde(default = "default_newline")]
    pub newline: String,
    #[serde(default)]
    pub null: String,
}

impl Default for CsvSpec {
    fn default() -> Self {
        Self {
            delimiter: default_delimiter(),
            quote: default_quote(),
            escape: default_escape(),
            newline: default_newline(),
            null: String::new(),
        }
    }
}

#[derive(Debug, Default, Deserialize)]
pub struct ContextSpec {
    #[serde(default = "default_context_reset")]
    pub reset: ContextReset,
}

#[derive(Debug, Clone, Copy, Default, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ContextReset {
    #[default]
    Row,
    Batch,
    Never,
}

#[derive(Debug, Deserialize)]
pub struct BatchSpec {
    #[serde(default = "default_batch_rows")]
    pub rows: usize,
}

impl Default for BatchSpec {
    fn default() -> Self {
        Self {
            rows: default_batch_rows(),
        }
    }
}

#[derive(Debug, Deserialize)]
pub struct FieldSpec {
    pub name: String,
    #[serde(default)]
    pub hidden: bool,
    #[serde(default)]
    pub order: i64,
    pub gen: GeneratorSpec,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum GeneratorSpec {
    Constant(ConstantSpec),
    Sequence(SequenceSpec),
    SequenceString(SequenceStringSpec),
    Name(NameSpec),
    Email(EmailSpec),
    Lorem(LoremSpec),
    Address(AddressSpec),
    Template(TemplateSpec),
    IntRange(IntRangeSpec),
    FloatRange(FloatRangeSpec),
    DecimalRange(DecimalRangeSpec),
    Fluctuating(FluctuatingSpec),
    #[serde(rename = "datetime_around")]
    DateTimeAround(DateTimeAroundSpec),
    #[serde(rename = "datetime_range")]
    DateTimeRange(DateTimeRangeSpec),
    Choice(ChoiceSpec),
    WeightedChoice(WeightedChoiceSpec),
    Uuid(UuidSpec),
    RandomBytes(RandomBytesSpec),
    #[serde(rename = "javascript")]
    JavaScript(JavaScriptSpec),
    Array(ArraySpec),
    Map(MapSpec),
    Struct(StructSpec),
    Ipv4(IpSpec),
    Ipv6(IpSpec),
}

pub fn default_max_len() -> usize {
    5
}

/// An ARRAY: `element` is any generator, run once per item.
#[derive(Debug, Clone, Deserialize)]
pub struct ArraySpec {
    pub element: Box<GeneratorSpec>,
    #[serde(default)]
    pub min_len: usize,
    #[serde(default = "default_max_len")]
    pub max_len: usize,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

/// A MAP. Keys are drawn until they are unique; a key generator with fewer
/// distinct values than `max_len` yields smaller maps rather than duplicates.
#[derive(Debug, Clone, Deserialize)]
pub struct MapSpec {
    pub key: Box<GeneratorSpec>,
    pub value: Box<GeneratorSpec>,
    #[serde(default)]
    pub min_len: usize,
    #[serde(default = "default_max_len")]
    pub max_len: usize,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

/// A STRUCT, or a JSON object when the column is JSON or VARIANT.
#[derive(Debug, Clone, Deserialize)]
pub struct StructSpec {
    pub fields: Vec<StructFieldSpec>,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct StructFieldSpec {
    pub name: String,
    pub gen: GeneratorSpec,
}

/// An IPV4 or IPV6 address, optionally inside `cidr`.
#[derive(Debug, Clone, Deserialize)]
pub struct IpSpec {
    #[serde(default)]
    pub cidr: Option<String>,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ConstantSpec {
    pub value: Value,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SequenceSpec {
    #[serde(default = "default_sequence_start")]
    pub start: i64,
    #[serde(default = "default_sequence_step")]
    pub step: i64,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SequenceStringSpec {
    /// Literal text with one `{}` where the counter goes.
    #[serde(default = "default_sequence_string_template")]
    pub template: String,
    #[serde(default = "default_sequence_string_start")]
    pub start: u64,
    #[serde(default = "default_sequence_string_step")]
    pub step: u64,
    /// Total width of the rendered value. The counter is zero-padded to fill
    /// whatever the literal text leaves over.
    #[serde(default = "default_sequence_string_width")]
    pub width: usize,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct NameSpec {
    #[serde(default = "default_name_part")]
    pub part: NamePart,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum NamePart {
    First,
    Last,
    Full,
}

#[derive(Debug, Clone, Deserialize)]
pub struct EmailSpec {
    #[serde(default)]
    pub style: Option<String>,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct LoremSpec {
    #[serde(default = "default_words_min")]
    pub words_min: usize,
    #[serde(default = "default_words_max")]
    pub words_max: usize,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct AddressSpec {
    #[serde(default = "default_address_part")]
    pub part: AddressPart,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AddressPart {
    Street,
    City,
    State,
    Country,
    PostalCode,
    Full,
}

#[derive(Debug, Clone, Deserialize)]
pub struct TemplateSpec {
    pub value: String,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct IntRangeSpec {
    /// 128-bit so LARGEINT columns can use their whole range. Bounds past
    /// the 64-bit range must be quoted in YAML: the spec is buffered through
    /// serde's tagged-enum machinery, which has no 128-bit integers.
    #[serde(deserialize_with = "deserialize_wide_int")]
    pub min: i128,
    #[serde(deserialize_with = "deserialize_wide_int")]
    pub max: i128,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct FloatRangeSpec {
    pub min: f64,
    pub max: f64,
    #[serde(default)]
    pub precision: Option<usize>,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct DecimalRangeSpec {
    pub min: Decimal,
    pub max: Decimal,
    pub scale: u32,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct FluctuatingSpec {
    pub data_type: NumericDataType,
    pub start: Decimal,
    pub min: Decimal,
    pub max: Decimal,
    #[serde(default = "default_initial_direction")]
    pub initial_direction: InitialDirection,
    pub step_min: Decimal,
    pub step_max: Decimal,
    /// Jitter added to each step, drawn from -noise..=noise. A noise larger
    /// than the step can flip the sign, so a run headed up still dips.
    /// Zero, the default, leaves every step following the direction.
    #[serde(default)]
    pub noise: Decimal,
    #[serde(default)]
    pub flip_chance: f64,
    #[serde(default)]
    pub precision: Option<usize>,
    #[serde(default)]
    pub scale: Option<u32>,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Copy, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum NumericDataType {
    Int,
    Float,
    Double,
    Decimal,
}

#[derive(Debug, Clone, Copy, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum InitialDirection {
    Up,
    Down,
    Random,
}

#[derive(Debug, Clone, Deserialize)]
pub struct DateTimeAroundSpec {
    #[serde(default)]
    pub base: Option<String>,
    #[serde(default)]
    pub offset_seconds_min: i64,
    #[serde(default)]
    pub offset_seconds_max: i64,
    #[serde(default = "default_datetime_format")]
    pub format: String,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct DateTimeRangeSpec {
    pub start: String,
    pub end: String,
    #[serde(default = "default_datetime_format")]
    pub format: String,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ChoiceSpec {
    pub values: Vec<Value>,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WeightedChoiceSpec {
    pub values: Vec<WeightedValue>,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WeightedValue {
    pub value: Value,
    pub weight: f64,
}

#[derive(Debug, Clone, Deserialize)]
pub struct UuidSpec {
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct RandomBytesSpec {
    pub min_bytes: usize,
    pub max_bytes: usize,
    pub encoding: RandomBytesEncoding,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct JavaScriptSpec {
    pub function: String,
    /// Resolved against the working directory first, then against the
    /// spec's directory when the spec was loaded from a file.
    pub file: String,
    /// The fields the function reads. Declaring them both orders the field
    /// graph and narrows what the call has to build, which is most of its
    /// cost. With none declared the function receives the whole row.
    #[serde(default)]
    pub deps: Vec<String>,
    /// Allow generation to keep every thread. Off by default because each
    /// thread has its own globals, so a counter in a global would restart
    /// per thread and repeat values.
    #[serde(default)]
    pub parallel: bool,
    #[serde(default)]
    pub null_rate: Option<f64>,
}

#[derive(Debug, Clone, Copy, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum RandomBytesEncoding {
    Hex,
    Base64,
    Base64url,
}

/// An integer written as a YAML number or, past 64 bits, as a quoted string.
pub fn deserialize_wide_int<'de, D>(deserializer: D) -> std::result::Result<i128, D::Error>
where
    D: serde::Deserializer<'de>,
{
    struct WideInt;

    impl<'de> serde::de::Visitor<'de> for WideInt {
        type Value = i128;

        fn expecting(&self, formatter: &mut std::fmt::Formatter) -> std::fmt::Result {
            formatter.write_str("an integer, quoted if it exceeds 64 bits")
        }

        fn visit_i64<E: serde::de::Error>(self, value: i64) -> std::result::Result<i128, E> {
            Ok(value as i128)
        }

        fn visit_u64<E: serde::de::Error>(self, value: u64) -> std::result::Result<i128, E> {
            Ok(value as i128)
        }

        fn visit_i128<E: serde::de::Error>(self, value: i128) -> std::result::Result<i128, E> {
            Ok(value)
        }

        fn visit_str<E: serde::de::Error>(self, value: &str) -> std::result::Result<i128, E> {
            value
                .trim()
                .parse::<i128>()
                .map_err(|_| E::custom(format!("`{}` is not an integer", value)))
        }
    }

    deserializer.deserialize_any(WideInt)
}

pub fn default_delimiter() -> char {
    ','
}

pub fn default_quote() -> char {
    '"'
}

pub fn default_escape() -> char {
    '"'
}

pub fn default_newline() -> String {
    "\n".to_string()
}

pub fn default_context_reset() -> ContextReset {
    ContextReset::Row
}

pub fn default_batch_rows() -> usize {
    1000
}

pub fn default_sequence_start() -> i64 {
    1
}

pub fn default_sequence_step() -> i64 {
    1
}

pub fn default_sequence_string_template() -> String {
    "{}".to_string()
}

pub fn default_sequence_string_start() -> u64 {
    1
}

pub fn default_sequence_string_step() -> u64 {
    1
}

/// 36 characters, the width of a hyphenated UUID, so swapping `uuid` for
/// `sequence_string` leaves column widths and file sizes where they were.
pub fn default_sequence_string_width() -> usize {
    36
}

/// Split `prefix{}suffix` into its two literal halves.
pub fn split_sequence_template(template: &str) -> Result<(String, String)> {
    let (prefix, rest) = template
        .split_once("{}")
        .ok_or_else(|| anyhow!("sequence_string template `{}` needs a `{{}}` placeholder", template))?;
    if rest.contains("{}") {
        bail!(
            "sequence_string template `{}` has more than one `{{}}` placeholder",
            template
        );
    }
    Ok((prefix.to_string(), rest.to_string()))
}

pub fn default_name_part() -> NamePart {
    NamePart::Full
}

pub fn default_words_min() -> usize {
    3
}

pub fn default_words_max() -> usize {
    12
}

pub fn default_address_part() -> AddressPart {
    AddressPart::Full
}

pub fn default_initial_direction() -> InitialDirection {
    InitialDirection::Up
}

pub fn default_datetime_format() -> String {
    "%Y-%m-%dT%H:%M:%S".to_string()
}


/// The TOML layout: `[fields.<path>]` tables keyed by the field's fully
/// qualified name. A dotted path such as `customer.name` places the value
/// inside a nested record when the output format has records; text formats
/// keep it as a dotted column name.
///
/// Each table holds the generator's own keys (`type` and its parameters)
/// plus the optional `hidden` and `order` field attributes. Table order in
/// the file is the column order.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RawSpecToml {
    pub version: u32,
    #[serde(default)]
    pub csv: CsvSpec,
    #[serde(default)]
    pub context: ContextSpec,
    #[serde(default)]
    pub batch: BatchSpec,
    pub fields: indexmap::IndexMap<String, FieldEntry>,
}

#[derive(Debug, Deserialize)]
pub struct FieldEntry {
    #[serde(default)]
    pub hidden: bool,
    #[serde(default)]
    pub order: i64,
    #[serde(flatten)]
    pub gen: GeneratorSpec,
}

impl From<RawSpecToml> for RawSpec {
    fn from(spec: RawSpecToml) -> Self {
        RawSpec {
            version: spec.version,
            csv: spec.csv,
            context: spec.context,
            batch: spec.batch,
            fields: spec
                .fields
                .into_iter()
                .map(|(name, entry)| FieldSpec {
                    name,
                    hidden: entry.hidden,
                    order: entry.order,
                    gen: entry.gen,
                })
                .collect(),
        }
    }
}

/// Split a fully qualified field name into its path segments.
pub fn field_path(name: &str) -> Vec<&str> {
    name.split('.').collect()
}

impl GeneratorSpec {
    /// Point every relative `javascript.file` at `base` when the file is
    /// not found from the working directory, so a spec can be run from
    /// anywhere and still find the script next to it.
    pub fn resolve_script_paths(&mut self, base: &std::path::Path) {
        match self {
            GeneratorSpec::JavaScript(spec) => {
                let path = std::path::Path::new(&spec.file);
                if path.is_relative() && !path.exists() {
                    let beside = base.join(path);
                    if beside.exists() {
                        spec.file = beside.to_string_lossy().into_owned();
                    }
                }
            }
            GeneratorSpec::Array(spec) => spec.element.resolve_script_paths(base),
            GeneratorSpec::Map(spec) => {
                spec.key.resolve_script_paths(base);
                spec.value.resolve_script_paths(base);
            }
            GeneratorSpec::Struct(spec) => {
                for field in &mut spec.fields {
                    field.gen.resolve_script_paths(base);
                }
            }
            _ => {}
        }
    }
}

impl RawSpec {
    /// See [`GeneratorSpec::resolve_script_paths`].
    pub fn resolve_script_paths(&mut self, base: &std::path::Path) {
        for field in &mut self.fields {
            field.gen.resolve_script_paths(base);
        }
    }
}
