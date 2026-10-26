//! Doris types to Arrow, and generated values to Arrow arrays.
//!
//! The mapping follows what Doris itself writes when it exports Parquet, on
//! the principle that Doris certainly reads its own output back:
//!
//! | Doris                     | Arrow / Parquet                              |
//! |---------------------------|----------------------------------------------|
//! | BOOLEAN                   | Boolean                                      |
//! | TINYINT..BIGINT           | Int8..Int64                                  |
//! | LARGEINT                  | Utf8 decimal digits, full 128-bit range      |
//! | FLOAT / DOUBLE            | Float32 / Float64                            |
//! | DECIMAL(p<=38)            | Decimal128                                   |
//! | DECIMAL(39..76)           | Decimal256, needs `enable_decimal256`        |
//! | DATE                      | Date32                                       |
//! | DATETIME(p)               | Timestamp(us), no zone, truncated to p       |
//! | CHAR / VARCHAR / STRING   | Utf8, length checked in UTF-8 bytes          |
//! | JSON / VARIANT            | Utf8, validated JSON                         |
//! | IPV4 / IPV6               | Utf8 in canonical text form                  |
//! | ARRAY / MAP / STRUCT      | List / Map / Struct, nested to any depth     |
//! | BITMAP / HLL / Q._STATE   | source values: Int64 / Utf8 / Float64        |
//!
//! Two deliberate departures. Doris exports every DECIMAL as fixed-length
//! bytes, while the Parquet writer here uses INT32 up to 9 digits and INT64
//! up to 18, which is equally valid Parquet and what Spark writes. And sketch
//! types have no Parquet form Doris loads directly, so they carry the values
//! a load turns into sketches with `to_bitmap`, `hll_hash` or
//! `to_quantile_state`.

use std::borrow::Cow;
use std::collections::HashSet;
use std::net::{Ipv4Addr, Ipv6Addr};
use std::sync::Arc;

use anyhow::{anyhow, bail, Context, Result};
use arrow::array::{
    ArrayRef, BooleanBuilder, Date32Builder, Decimal128Builder, Decimal256Builder, Float32Builder,
    Float64Builder, Int16Builder, Int32Builder, Int64Builder, Int8Builder, ListArray, MapArray,
    StringBuilder, StructArray, TimestampMicrosecondBuilder,
};
use arrow::buffer::{NullBuffer, OffsetBuffer};
use arrow::datatypes::{i256, DataType, Field, Fields, Schema, SchemaRef, TimeUnit};
use chrono::{NaiveDate, NaiveDateTime};

use crate::schema::{Column, DorisType, DECIMAL128_MAX_PRECISION};
use datagen::generators::format_decimal_units;
use datagen::Value;

/// LARGEINT holds ±(2^127 - 1); Doris excludes i128::MIN.
const LARGEINT_MAX: i128 = i128::MAX;
const LARGEINT_MIN: i128 = -i128::MAX;

/// Doris DATE and DATETIME span 0000-01-01 to 9999-12-31 23:59:59.999999.
const MIN_DATE_DAYS: i32 = -719_528;
const MAX_DATE_DAYS: i32 = 2_932_896;
const MICROS_PER_DAY: i64 = 24 * 60 * 60 * 1_000_000;
const MIN_DATETIME_MICROS: i64 = MIN_DATE_DAYS as i64 * MICROS_PER_DAY;
const MAX_DATETIME_MICROS: i64 = (MAX_DATE_DAYS as i64 + 1) * MICROS_PER_DAY - 1;

/// Parquet field names from the format spec for LIST and MAP.
const LIST_ELEMENT: &str = "element";
const MAP_ENTRIES: &str = "key_value";

pub fn arrow_type(ty: &DorisType) -> Result<DataType> {
    let mapped = match ty {
        DorisType::Boolean => DataType::Boolean,
        DorisType::TinyInt => DataType::Int8,
        DorisType::SmallInt => DataType::Int16,
        DorisType::Int => DataType::Int32,
        DorisType::BigInt => DataType::Int64,
        // Doris exports LARGEINT as text, and text is the only Parquet form
        // that holds all 39 digits of its range.
        DorisType::LargeInt => DataType::Utf8,
        DorisType::Float => DataType::Float32,
        DorisType::Double => DataType::Float64,
        DorisType::Decimal { precision, scale } if *precision <= DECIMAL128_MAX_PRECISION => {
            DataType::Decimal128(*precision, *scale as i8)
        }
        DorisType::Decimal { precision, scale } => DataType::Decimal256(*precision, *scale as i8),
        DorisType::Date => DataType::Date32,
        // Microseconds covers every DATETIME scale; no zone, because DATETIME
        // is a wall-clock value that Doris stores without conversion.
        DorisType::DateTime { .. } => DataType::Timestamp(TimeUnit::Microsecond, None),
        DorisType::Char { .. }
        | DorisType::Varchar { .. }
        | DorisType::String
        | DorisType::Json
        | DorisType::Variant
        | DorisType::Ipv4
        | DorisType::Ipv6 => DataType::Utf8,
        DorisType::Array(element) => DataType::List(Arc::new(Field::new(
            LIST_ELEMENT,
            arrow_type(element)?,
            true,
        ))),
        DorisType::Map(key, value) => {
            if matches!(**key, DorisType::Array(_) | DorisType::Map(..) | DorisType::Struct(_)) {
                bail!("MAP keys must be a scalar type, not {}", key.sql_name());
            }
            DataType::Map(Arc::new(map_entries_field(key, value)?), false)
        }
        DorisType::Struct(fields) => DataType::Struct(struct_fields(fields)?),
        // Sketch columns carry the values the load turns into sketches.
        DorisType::Bitmap => DataType::Int64,
        DorisType::Hll => DataType::Utf8,
        DorisType::QuantileState => DataType::Float64,
        DorisType::AggState(signature) => bail!(
            "AGG_STATE<{}> cannot be generated: its load expression depends on the aggregate. \
             Drop the column from the schema, or load it separately",
            signature
        ),
    };
    Ok(mapped)
}

/// A MAP's entries: a required key and a nullable value, per the Parquet spec.
fn map_entries_field(key: &DorisType, value: &DorisType) -> Result<Field> {
    Ok(Field::new(
        MAP_ENTRIES,
        DataType::Struct(Fields::from(vec![
            Field::new("key", arrow_type(key)?, false),
            Field::new("value", arrow_type(value)?, true),
        ])),
        false,
    ))
}

fn struct_fields(fields: &[(String, DorisType)]) -> Result<Fields> {
    fields
        .iter()
        .map(|(name, ty)| {
            arrow_type(ty)
                .map(|arrow| Field::new(name, arrow, true))
                .with_context(|| format!("STRUCT field `{}`", name))
        })
        .collect::<Result<Vec<_>>>()
        .map(Fields::from)
}

pub fn arrow_schema(columns: &[Column]) -> Result<SchemaRef> {
    let fields = columns
        .iter()
        .map(|column| {
            arrow_type(&column.ty)
                .map(|ty| Field::new(&column.name, ty, column.nullable))
                .with_context(|| format!("column `{}`", column.name))
        })
        .collect::<Result<Vec<_>>>()?;
    Ok(Arc::new(Schema::new(fields)))
}

/// Sketch columns and the load expression each needs, e.g.
/// ("uv", "to_bitmap(`uv`)"), for the startup banner.
pub fn sketch_load_expressions(columns: &[Column]) -> Vec<(String, String)> {
    columns
        .iter()
        .filter_map(|column| {
            column.ty.load_function().map(|function| {
                let expression = if matches!(column.ty, DorisType::QuantileState) {
                    format!("{}(`{}`, 2048)", function, column.name)
                } else {
                    format!("{}(`{}`)", function, column.name)
                };
                (column.name.clone(), expression)
            })
        })
        .collect()
}

/// Push one value through the real conversion for `column`, exactly as the
/// writer would. Startup validation uses this so a spec that cannot fit the
/// schema fails before the first row rather than an hour into a run.
pub fn probe(column: &Column, value: &Value) -> Result<()> {
    if matches!(value, Value::Null) && !column.nullable {
        bail!("column is NOT NULL but the generator can emit null");
    }
    let mut builder = ColumnBuilder::new(&column.ty, 1)?;
    builder.append(value)?;
    builder.finish()?;
    Ok(())
}

/// Checks applied to text before it becomes a Utf8 value.
#[derive(Debug, Clone, Copy)]
enum TextCheck {
    None,
    /// CHAR and VARCHAR lengths count UTF-8 bytes, not characters.
    MaxBytes { limit: usize, char_type: bool },
    LargeInt,
    Json,
    Ipv4,
    Ipv6,
}

/// One typed accumulator per column.
enum ColumnBuilder {
    Boolean(BooleanBuilder),
    Int8(Int8Builder),
    Int16(Int16Builder),
    Int32(Int32Builder),
    /// `unsigned` for BITMAP sources, which `to_bitmap` requires to be >= 0.
    Int64 { builder: Int64Builder, unsigned: bool },
    Float32(Float32Builder),
    Float64(Float64Builder),
    Decimal128 { builder: Decimal128Builder, precision: u8, scale: u8 },
    Decimal256 { builder: Decimal256Builder, precision: u8, scale: u8 },
    Date32(Date32Builder),
    Timestamp { builder: TimestampMicrosecondBuilder, scale: u8 },
    Utf8 { builder: StringBuilder, check: TextCheck },
    /// ARRAY, MAP and STRUCT keep their values until the batch is built,
    /// then assemble the nested arrays in one pass.
    Nested { ty: DorisType, values: Vec<Value> },
}

impl ColumnBuilder {
    fn new(ty: &DorisType, capacity: usize) -> Result<Self> {
        let utf8 = |check| ColumnBuilder::Utf8 {
            builder: StringBuilder::with_capacity(capacity, capacity * 16),
            check,
        };
        let builder = match ty {
            DorisType::Boolean => ColumnBuilder::Boolean(BooleanBuilder::with_capacity(capacity)),
            DorisType::TinyInt => ColumnBuilder::Int8(Int8Builder::with_capacity(capacity)),
            DorisType::SmallInt => ColumnBuilder::Int16(Int16Builder::with_capacity(capacity)),
            DorisType::Int => ColumnBuilder::Int32(Int32Builder::with_capacity(capacity)),
            DorisType::BigInt => ColumnBuilder::Int64 {
                builder: Int64Builder::with_capacity(capacity),
                unsigned: false,
            },
            DorisType::Bitmap => ColumnBuilder::Int64 {
                builder: Int64Builder::with_capacity(capacity),
                unsigned: true,
            },
            DorisType::Float => ColumnBuilder::Float32(Float32Builder::with_capacity(capacity)),
            DorisType::Double | DorisType::QuantileState => {
                ColumnBuilder::Float64(Float64Builder::with_capacity(capacity))
            }
            DorisType::Decimal { precision, scale } if *precision <= DECIMAL128_MAX_PRECISION => {
                ColumnBuilder::Decimal128 {
                    builder: Decimal128Builder::with_capacity(capacity),
                    precision: *precision,
                    scale: *scale,
                }
            }
            DorisType::Decimal { precision, scale } => ColumnBuilder::Decimal256 {
                builder: Decimal256Builder::with_capacity(capacity),
                precision: *precision,
                scale: *scale,
            },
            DorisType::Date => ColumnBuilder::Date32(Date32Builder::with_capacity(capacity)),
            DorisType::DateTime { scale } => ColumnBuilder::Timestamp {
                builder: TimestampMicrosecondBuilder::with_capacity(capacity),
                scale: *scale,
            },
            DorisType::Char { len } => utf8(TextCheck::MaxBytes { limit: *len as usize, char_type: true }),
            DorisType::Varchar { len } => {
                utf8(TextCheck::MaxBytes { limit: *len as usize, char_type: false })
            }
            DorisType::String | DorisType::Hll => utf8(TextCheck::None),
            DorisType::LargeInt => utf8(TextCheck::LargeInt),
            DorisType::Json | DorisType::Variant => utf8(TextCheck::Json),
            DorisType::Ipv4 => utf8(TextCheck::Ipv4),
            DorisType::Ipv6 => utf8(TextCheck::Ipv6),
            DorisType::Array(_) | DorisType::Map(..) | DorisType::Struct(_) => {
                // Build the Arrow type now so an unsupported shape fails here.
                arrow_type(ty)?;
                ColumnBuilder::Nested { ty: ty.clone(), values: Vec::with_capacity(capacity) }
            }
            DorisType::AggState(_) => {
                arrow_type(ty)?;
                unreachable!("arrow_type refuses AGG_STATE")
            }
        };
        Ok(builder)
    }

    fn append(&mut self, value: &Value) -> Result<()> {
        if matches!(value, Value::Null) {
            self.append_null();
            return Ok(());
        }
        match self {
            ColumnBuilder::Boolean(builder) => builder.append_value(as_bool(value)?),
            ColumnBuilder::Int8(builder) => {
                builder.append_value(as_int(value, i8::MIN as i128, i8::MAX as i128, "TINYINT")? as i8)
            }
            ColumnBuilder::Int16(builder) => builder
                .append_value(as_int(value, i16::MIN as i128, i16::MAX as i128, "SMALLINT")? as i16),
            ColumnBuilder::Int32(builder) => {
                builder.append_value(as_int(value, i32::MIN as i128, i32::MAX as i128, "INT")? as i32)
            }
            ColumnBuilder::Int64 { builder, unsigned } => {
                let (min, label) = if *unsigned {
                    (0, "BITMAP source (to_bitmap needs a non-negative BIGINT)")
                } else {
                    (i64::MIN as i128, "BIGINT")
                };
                builder.append_value(as_int(value, min, i64::MAX as i128, label)? as i64)
            }
            ColumnBuilder::Float32(builder) => {
                let number = as_f64(value)?;
                let narrowed = number as f32;
                if number.is_finite() && !narrowed.is_finite() {
                    bail!("{} is outside FLOAT's range", number);
                }
                builder.append_value(narrowed)
            }
            ColumnBuilder::Float64(builder) => builder.append_value(as_f64(value)?),
            ColumnBuilder::Decimal128 { builder, precision, scale } => {
                builder.append_value(as_decimal(value, *precision, *scale)?)
            }
            ColumnBuilder::Decimal256 { builder, precision, scale } => {
                builder.append_value(as_decimal256(value, *precision, *scale)?)
            }
            ColumnBuilder::Date32(builder) => {
                let days = as_date(value)?;
                if !(MIN_DATE_DAYS..=MAX_DATE_DAYS).contains(&days) {
                    bail!("{} is outside Doris's DATE range of 0000-01-01 to 9999-12-31", as_str(value));
                }
                builder.append_value(days)
            }
            ColumnBuilder::Timestamp { builder, scale } => {
                let micros = as_timestamp_micros(value)?;
                if !(MIN_DATETIME_MICROS..=MAX_DATETIME_MICROS).contains(&micros) {
                    bail!(
                        "{} is outside Doris's DATETIME range of 0000-01-01 to 9999-12-31",
                        as_str(value)
                    );
                }
                // Keep only the digits DATETIME(p) stores, so the file holds
                // exactly the value Doris will, with no rounding on load.
                // Flooring, so a value never rolls into the next second.
                let unit = 10i64.pow(6 - *scale as u32);
                builder.append_value(micros.div_euclid(unit) * unit)
            }
            ColumnBuilder::Utf8 { builder, check } => builder.append_value(checked_text(value, *check)?),
            ColumnBuilder::Nested { ty, values } => {
                // Shape the value now, so a mismatch is reported against this
                // row rather than later when the whole batch is assembled.
                values.push(shape_nested(ty, value)?.into_owned())
            }
        }
        Ok(())
    }

    fn append_null(&mut self) {
        match self {
            ColumnBuilder::Boolean(builder) => builder.append_null(),
            ColumnBuilder::Int8(builder) => builder.append_null(),
            ColumnBuilder::Int16(builder) => builder.append_null(),
            ColumnBuilder::Int32(builder) => builder.append_null(),
            ColumnBuilder::Int64 { builder, .. } => builder.append_null(),
            ColumnBuilder::Float32(builder) => builder.append_null(),
            ColumnBuilder::Float64(builder) => builder.append_null(),
            ColumnBuilder::Decimal128 { builder, .. } => builder.append_null(),
            ColumnBuilder::Decimal256 { builder, .. } => builder.append_null(),
            ColumnBuilder::Date32(builder) => builder.append_null(),
            ColumnBuilder::Timestamp { builder, .. } => builder.append_null(),
            ColumnBuilder::Utf8 { builder, .. } => builder.append_null(),
            ColumnBuilder::Nested { values, .. } => values.push(Value::Null),
        }
    }

    fn finish(&mut self) -> Result<ArrayRef> {
        let array: ArrayRef = match self {
            ColumnBuilder::Boolean(builder) => Arc::new(builder.finish()),
            ColumnBuilder::Int8(builder) => Arc::new(builder.finish()),
            ColumnBuilder::Int16(builder) => Arc::new(builder.finish()),
            ColumnBuilder::Int32(builder) => Arc::new(builder.finish()),
            ColumnBuilder::Int64 { builder, .. } => Arc::new(builder.finish()),
            ColumnBuilder::Float32(builder) => Arc::new(builder.finish()),
            ColumnBuilder::Float64(builder) => Arc::new(builder.finish()),
            ColumnBuilder::Decimal128 { builder, precision, scale } => Arc::new(
                builder
                    .finish()
                    .with_precision_and_scale(*precision, *scale as i8)?,
            ),
            ColumnBuilder::Decimal256 { builder, precision, scale } => Arc::new(
                builder
                    .finish()
                    .with_precision_and_scale(*precision, *scale as i8)?,
            ),
            ColumnBuilder::Date32(builder) => Arc::new(builder.finish()),
            ColumnBuilder::Timestamp { builder, .. } => Arc::new(builder.finish()),
            ColumnBuilder::Utf8 { builder, .. } => Arc::new(builder.finish()),
            ColumnBuilder::Nested { ty, values } => build_nested(ty, std::mem::take(values))?,
        };
        Ok(array)
    }
}

/// Text for a Utf8 column, after the column type's own checks.
fn checked_text(value: &Value, check: TextCheck) -> Result<Cow<'_, str>> {
    Ok(match check {
        TextCheck::None => as_text(value),
        TextCheck::MaxBytes { limit, char_type } => {
            let text = as_text(value);
            if text.len() > limit {
                bail!(
                    "`{}` is {} bytes but {}({}) holds {} bytes (Doris counts UTF-8 bytes, not characters)",
                    preview(&text),
                    text.len(),
                    if char_type { "CHAR" } else { "VARCHAR" },
                    limit,
                    limit
                );
            }
            text
        }
        TextCheck::LargeInt => {
            Cow::Owned(as_int(value, LARGEINT_MIN, LARGEINT_MAX, "LARGEINT")?.to_string())
        }
        TextCheck::Json => match value {
            // Text must already be JSON. Plain words are not: quote them, or
            // build the document with the array, map or struct generators.
            Value::String(text) => {
                serde_json::from_str::<serde::de::IgnoredAny>(text).map_err(|error| {
                    anyhow!("`{}` is not valid JSON ({})", preview(text), error)
                })?;
                Cow::Borrowed(text.as_str())
            }
            other => Cow::Owned(other.to_json_string()),
        },
        TextCheck::Ipv4 => {
            let address = match value {
                // An integer is the address's 32-bit form, as Doris stores it.
                Value::I64(number) => u32::try_from(*number)
                    .map(Ipv4Addr::from)
                    .map_err(|_| anyhow!("{} is not a 32-bit IPv4 address", number))?,
                other => {
                    let text = as_text(other);
                    text.trim()
                        .parse::<Ipv4Addr>()
                        .map_err(|_| anyhow!("`{}` is not an IPv4 address", preview(&text)))?
                }
            };
            Cow::Owned(address.to_string())
        }
        TextCheck::Ipv6 => {
            let address = match value {
                Value::I128(number) if *number >= 0 => Ipv6Addr::from(*number as u128),
                other => {
                    let text = as_text(other);
                    text.trim()
                        .parse::<Ipv6Addr>()
                        .map_err(|_| anyhow!("`{}` is not an IPv6 address", preview(&text)))?
                }
            };
            Cow::Owned(address.to_string())
        }
    })
}

/// The first few dozen characters of a value, for an error message.
fn preview(text: &str) -> String {
    const LIMIT: usize = 40;
    if text.chars().count() <= LIMIT {
        text.to_string()
    } else {
        format!("{}...", text.chars().take(LIMIT).collect::<String>())
    }
}

/// Bring a value into the shape a nested type expects. JSON text is parsed,
/// so templates and JavaScript can feed nested columns; STRUCT-shaped values
/// are accepted for MAP columns, since a YAML or JSON object is written that
/// way.
fn shape_nested<'a>(ty: &DorisType, value: &'a Value) -> Result<Cow<'a, Value>> {
    let value = match value {
        Value::String(text) => Cow::Owned(Value::from_json(
            serde_json::from_str(text)
                .map_err(|error| anyhow!("`{}` is not valid JSON for {} ({})", preview(text), ty.sql_name(), error))?,
        )),
        other => Cow::Borrowed(other),
    };
    let fits = matches!(
        (ty, value.as_ref()),
        (_, Value::Null)
            | (DorisType::Array(_), Value::List(_))
            | (DorisType::Map(..), Value::Map(_) | Value::Struct(_))
            | (DorisType::Struct(_), Value::Struct(_) | Value::List(_))
    );
    if !fits {
        bail!("{} cannot hold `{}`", ty.sql_name(), preview(&value.csv_string("null")));
    }
    Ok(value)
}

fn null_buffer(valid: Vec<bool>) -> Option<NullBuffer> {
    if valid.iter().all(|flag| *flag) {
        None
    } else {
        Some(NullBuffer::from(valid))
    }
}

fn offset(count: usize) -> Result<i32> {
    i32::try_from(count).map_err(|_| anyhow!("a batch holds more than 2^31 nested elements; lower --batch-rows"))
}

/// Assemble an Arrow array for any type, recursing through nesting. Scalars
/// go through the same builders as top-level columns, so every check applies
/// at every depth.
fn build_array(ty: &DorisType, values: Vec<Value>) -> Result<ArrayRef> {
    match ty {
        DorisType::Array(_) | DorisType::Map(..) | DorisType::Struct(_) => {
            let shaped = values
                .into_iter()
                .map(|value| shape_nested(ty, &value).map(Cow::into_owned))
                .collect::<Result<Vec<_>>>()?;
            build_nested(ty, shaped)
        }
        scalar => {
            let mut builder = ColumnBuilder::new(scalar, values.len())?;
            for value in &values {
                builder.append(value)?;
            }
            builder.finish()
        }
    }
}

/// Build a nested array from values already shaped by `shape_nested`.
fn build_nested(ty: &DorisType, values: Vec<Value>) -> Result<ArrayRef> {
    match ty {
        DorisType::Array(element) => {
            let mut offsets = Vec::with_capacity(values.len() + 1);
            offsets.push(0i32);
            let mut valid = Vec::with_capacity(values.len());
            let mut children = Vec::new();
            for value in values {
                match value {
                    Value::List(items) => {
                        children.extend(items);
                        valid.push(true);
                    }
                    _ => valid.push(false),
                }
                offsets.push(offset(children.len())?);
            }
            let child = build_array(element, children).context("ARRAY element")?;
            let field = Arc::new(Field::new(LIST_ELEMENT, arrow_type(element)?, true));
            Ok(Arc::new(ListArray::try_new(
                field,
                OffsetBuffer::new(offsets.into()),
                child,
                null_buffer(valid),
            )?))
        }
        DorisType::Map(key_type, value_type) => {
            let mut offsets = Vec::with_capacity(values.len() + 1);
            offsets.push(0i32);
            let mut valid = Vec::with_capacity(values.len());
            let mut keys = Vec::new();
            let mut items = Vec::new();
            for value in values {
                let entries: Vec<(Value, Value)> = match value {
                    Value::Map(entries) => entries,
                    Value::Struct(fields) => fields
                        .into_iter()
                        .map(|(name, value)| (Value::String(name), value))
                        .collect(),
                    _ => {
                        valid.push(false);
                        offsets.push(offset(keys.len())?);
                        continue;
                    }
                };
                let mut seen = HashSet::with_capacity(entries.len());
                for (key, item) in entries {
                    if matches!(key, Value::Null) {
                        bail!("MAP keys cannot be null");
                    }
                    if !seen.insert(key.csv_string("")) {
                        bail!("MAP has key `{}` twice", key.csv_string(""));
                    }
                    keys.push(key);
                    items.push(item);
                }
                valid.push(true);
                offsets.push(offset(keys.len())?);
            }
            let keys = build_array(key_type, keys).context("MAP key")?;
            let items = build_array(value_type, items).context("MAP value")?;
            let entries_field = map_entries_field(key_type, value_type)?;
            let DataType::Struct(entry_fields) = entries_field.data_type().clone() else {
                unreachable!("map entries are a struct");
            };
            let entries = StructArray::try_new(entry_fields, vec![keys, items], None)?;
            Ok(Arc::new(MapArray::try_new(
                Arc::new(entries_field),
                OffsetBuffer::new(offsets.into()),
                entries,
                null_buffer(valid),
                false,
            )?))
        }
        DorisType::Struct(fields) => {
            let mut valid = Vec::with_capacity(values.len());
            let mut columns: Vec<Vec<Value>> = vec![Vec::with_capacity(values.len()); fields.len()];
            for value in values {
                match value {
                    Value::Struct(named) => {
                        let mut slots: Vec<Option<Value>> = vec![None; fields.len()];
                        for (name, item) in named {
                            let index = fields
                                .iter()
                                .position(|(field, _)| field == &name)
                                .ok_or_else(|| {
                                    anyhow!(
                                        "STRUCT has no field `{}`; its fields are {}",
                                        name,
                                        fields.iter().map(|(field, _)| field.as_str()).collect::<Vec<_>>().join(", ")
                                    )
                                })?;
                            slots[index] = Some(item);
                        }
                        // A field left out is null, as it would be in JSON.
                        for (column, slot) in columns.iter_mut().zip(slots) {
                            column.push(slot.unwrap_or(Value::Null));
                        }
                        valid.push(true);
                    }
                    // A list fills the fields by position.
                    Value::List(items) => {
                        if items.len() != fields.len() {
                            bail!(
                                "STRUCT has {} fields but the value has {} items",
                                fields.len(),
                                items.len()
                            );
                        }
                        for (column, item) in columns.iter_mut().zip(items) {
                            column.push(item);
                        }
                        valid.push(true);
                    }
                    _ => {
                        for column in columns.iter_mut() {
                            column.push(Value::Null);
                        }
                        valid.push(false);
                    }
                }
            }
            let arrays = fields
                .iter()
                .zip(columns)
                .map(|((name, field_type), values)| {
                    build_array(field_type, values).with_context(|| format!("STRUCT field `{}`", name))
                })
                .collect::<Result<Vec<_>>>()?;
            Ok(Arc::new(StructArray::try_new(struct_fields(fields)?, arrays, null_buffer(valid))?))
        }
        scalar => build_array(scalar, values),
    }
}

fn as_text(value: &Value) -> Cow<'_, str> {
    match value {
        Value::String(text) => Cow::Borrowed(text.as_str()),
        other => Cow::Owned(as_str(other)),
    }
}

fn as_str(value: &Value) -> String {
    match value {
        Value::String(text) => text.clone(),
        Value::Bool(flag) => flag.to_string(),
        Value::I64(number) => number.to_string(),
        Value::I128(number) => number.to_string(),
        Value::F64(number) => number.to_string(),
        Value::Timestamp { .. } | Value::Decimal { .. } => value.csv_string(""),
        Value::List(_) | Value::Map(_) | Value::Struct(_) => value.to_json_string(),
        Value::Null => String::new(),
    }
}

fn as_bool(value: &Value) -> Result<bool> {
    match value {
        Value::Bool(flag) => Ok(*flag),
        Value::I64(number) => Ok(*number != 0),
        Value::I128(number) => Ok(*number != 0),
        Value::String(text) => match text.trim().to_ascii_lowercase().as_str() {
            "true" | "t" | "1" | "yes" => Ok(true),
            "false" | "f" | "0" | "no" => Ok(false),
            other => bail!("`{}` is not a boolean", other),
        },
        Value::Decimal { units, .. } => Ok(*units != 0),
        other => bail!("cannot read a boolean from {}", describe(other)),
    }
}

fn as_int(value: &Value, min: i128, max: i128, type_name: &str) -> Result<i128> {
    let number = match value {
        Value::I64(number) => *number as i128,
        Value::I128(number) => *number,
        Value::Bool(flag) => *flag as i128,
        Value::F64(number) => {
            if !number.is_finite() {
                bail!("{} is not a finite number", number);
            }
            number.trunc() as i128
        }
        Value::String(text) => text
            .trim()
            .parse::<i128>()
            .map_err(|_| anyhow!("`{}` is not an integer", preview(text)))?,
        // Truncates towards zero, matching the F64 arm above.
        Value::Decimal { units, scale } => units / 10i128.pow(*scale),
        other => bail!("cannot read an integer from {}", describe(other)),
    };
    if number < min || number > max {
        bail!("{} is outside {}'s range of {} to {}", number, type_name, min, max);
    }
    Ok(number)
}

fn as_f64(value: &Value) -> Result<f64> {
    match value {
        Value::F64(number) => Ok(*number),
        Value::I64(number) => Ok(*number as f64),
        Value::I128(number) => Ok(*number as f64),
        Value::String(text) => text
            .trim()
            .parse::<f64>()
            .map_err(|_| anyhow!("`{}` is not a number", preview(text))),
        Value::Decimal { units, scale } => Ok(*units as f64 / 10f64.powi(*scale as i32)),
        other => bail!("cannot read a number from {}", describe(other)),
    }
}

/// Name a value's kind for an error, without dumping a whole nested value.
fn describe(value: &Value) -> String {
    match value {
        Value::Timestamp { .. } => "a timestamp".into(),
        Value::List(_) => "a list".into(),
        Value::Map(_) => "a map".into(),
        Value::Struct(_) => "a struct".into(),
        other => format!("`{}`", preview(&other.csv_string("null"))),
    }
}

/// Split decimal text into sign, whole digits and fraction digits.
fn split_decimal_text(text: &str) -> Result<(bool, &str, &str)> {
    let (negative, digits) = match text.strip_prefix('-') {
        Some(rest) => (true, rest),
        None => (false, text.strip_prefix('+').unwrap_or(text)),
    };
    let (whole, fraction) = digits.split_once('.').unwrap_or((digits, ""));
    if (whole.is_empty() && fraction.is_empty())
        || !whole.chars().all(|ch| ch.is_ascii_digit())
        || !fraction.chars().all(|ch| ch.is_ascii_digit())
    {
        bail!("`{}` is not a decimal", preview(text));
    }
    Ok((negative, whole, fraction))
}

/// Decimal digits of a value, for the text-based decimal paths.
fn decimal_text(value: &Value) -> Result<String> {
    Ok(match value {
        Value::Decimal { units, scale } => format_decimal_units(*units, *scale),
        Value::String(text) => text.trim().to_string(),
        Value::I64(number) => number.to_string(),
        Value::I128(number) => number.to_string(),
        Value::F64(number) if number.is_finite() => format!("{}", number),
        other => bail!("cannot read a decimal from {}", describe(other)),
    })
}

/// Parse a decimal into its unscaled 128-bit form at the column's scale.
fn as_decimal(value: &Value, precision: u8, scale: u8) -> Result<i128> {
    // A generator that already knows the number hands it over directly. Only
    // literals from a spec file take the text path below.
    match value {
        Value::Decimal { units, scale: value_scale } => {
            return rescale_decimal(*units, *value_scale, precision, scale)
        }
        Value::I64(number) => return rescale_decimal(*number as i128, 0, precision, scale),
        Value::I128(number) => return rescale_decimal(*number, 0, precision, scale),
        _ => {}
    }
    let text = decimal_text(value)?;
    let (negative, whole, fraction) = split_decimal_text(&text)?;
    // Refuse to silently drop digits the column cannot hold.
    if fraction.len() > scale as usize {
        bail!("`{}` has {} decimal places but the column holds {}", text, fraction.len(), scale);
    }
    let padded = format!("{}{:0<width$}", whole, fraction, width = scale as usize);
    let unscaled: i128 = padded
        .parse()
        .map_err(|_| anyhow!("`{}` does not fit a 128-bit decimal", text))?;
    let limit = 10i128
        .checked_pow(precision as u32)
        .ok_or_else(|| anyhow!("precision {} is too large", precision))?;
    if unscaled >= limit {
        bail!("`{}` exceeds the column's precision of {}", text, precision);
    }
    Ok(if negative { -unscaled } else { unscaled })
}

/// Move an unscaled decimal from `from_scale` to the column's scale,
/// refusing anything that would drop digits or overflow the precision.
fn rescale_decimal(units: i128, from_scale: u32, precision: u8, scale: u8) -> Result<i128> {
    let target = scale as u32;
    let unscaled = if from_scale == target {
        units
    } else if from_scale < target {
        units
            .checked_mul(10i128.pow(target - from_scale))
            .ok_or_else(|| anyhow!("decimal does not fit a 128-bit value"))?
    } else {
        let divisor = 10i128.pow(from_scale - target);
        if units % divisor != 0 {
            bail!(
                "`{}` has {} decimal places but the column holds {}",
                format_units(units, from_scale),
                from_scale,
                scale
            );
        }
        units / divisor
    };
    let limit = 10i128
        .checked_pow(precision as u32)
        .ok_or_else(|| anyhow!("precision {} is too large", precision))?;
    if unscaled.unsigned_abs() >= limit.unsigned_abs() {
        bail!(
            "`{}` exceeds the column's precision of {}",
            format_units(units, from_scale),
            precision
        );
    }
    Ok(unscaled)
}

/// DECIMAL above 38 digits, which needs 256 bits.
fn as_decimal256(value: &Value, precision: u8, scale: u8) -> Result<i256> {
    let text = decimal_text(value)?;
    let (negative, whole, fraction) = split_decimal_text(&text)?;
    if fraction.len() > scale as usize {
        bail!("`{}` has {} decimal places but the column holds {}", text, fraction.len(), scale);
    }
    let padded = format!("{}{:0<width$}", whole, fraction, width = scale as usize);
    let significant = padded.trim_start_matches('0');
    if significant.len() > precision as usize {
        bail!("`{}` exceeds the column's precision of {}", text, precision);
    }
    let magnitude = if significant.is_empty() {
        i256::from_i128(0)
    } else {
        i256::from_string(significant)
            .ok_or_else(|| anyhow!("`{}` does not fit a 256-bit decimal", text))?
    };
    Ok(if negative { magnitude.wrapping_neg() } else { magnitude })
}

/// Only used to name a value in an error message.
fn format_units(units: i128, scale: u32) -> String {
    Value::Decimal { units, scale }.csv_string("")
}

fn as_date(value: &Value) -> Result<i32> {
    if let Value::Timestamp { micros, .. } = value {
        return Ok(micros.div_euclid(MICROS_PER_DAY) as i32);
    }
    let text = as_str(value);
    let trimmed = text.trim();
    // Accept a full timestamp and keep only the date part.
    let date_part = trimmed.split(['T', ' ']).next().unwrap_or(trimmed);
    let date = NaiveDate::parse_from_str(date_part, "%Y-%m-%d")
        .map_err(|_| anyhow!("`{}` is not a date (expected YYYY-MM-DD)", preview(trimmed)))?;
    let epoch = NaiveDate::from_ymd_opt(1970, 1, 1).expect("valid epoch");
    Ok((date - epoch).num_days() as i32)
}

fn as_timestamp_micros(value: &Value) -> Result<i64> {
    // The whole point of Value::Timestamp: no format, no parse.
    if let Value::Timestamp { micros, .. } = value {
        return Ok(*micros);
    }
    let text = as_str(value);
    let trimmed = text.trim();
    const FORMATS: [&str; 4] = [
        "%Y-%m-%d %H:%M:%S%.f",
        "%Y-%m-%dT%H:%M:%S%.f",
        "%Y-%m-%d %H:%M:%S",
        "%Y-%m-%dT%H:%M:%S",
    ];
    for format in FORMATS {
        if let Ok(parsed) = NaiveDateTime::parse_from_str(trimmed, format) {
            return Ok(parsed.and_utc().timestamp_micros());
        }
    }
    // A bare date is a valid timestamp at midnight.
    if let Ok(date) = NaiveDate::parse_from_str(trimmed, "%Y-%m-%d") {
        return Ok(date
            .and_hms_opt(0, 0, 0)
            .expect("midnight is valid")
            .and_utc()
            .timestamp_micros());
    }
    bail!("`{}` is not a timestamp", preview(trimmed))
}

/// Accumulates generated rows and turns them into Arrow record batches.
pub struct BatchBuilder {
    schema: SchemaRef,
    columns: Vec<Column>,
    builders: Vec<ColumnBuilder>,
    rows: usize,
    capacity: usize,
}

impl BatchBuilder {
    pub fn new(columns: Vec<Column>, capacity: usize) -> Result<Self> {
        let schema = arrow_schema(&columns)?;
        let builders = columns
            .iter()
            .map(|column| ColumnBuilder::new(&column.ty, capacity))
            .collect::<Result<Vec<_>>>()?;
        Ok(Self {
            schema,
            columns,
            builders,
            rows: 0,
            capacity,
        })
    }

    pub fn schema(&self) -> SchemaRef {
        self.schema.clone()
    }

    pub fn rows(&self) -> usize {
        self.rows
    }

    pub fn is_empty(&self) -> bool {
        self.rows == 0
    }

    /// Append one row, taking each column's value from `lookup`.
    pub fn append_row<'a, F>(&mut self, lookup: F) -> Result<()>
    where
        F: Fn(&str) -> Option<&'a Value>,
    {
        for (index, column) in self.columns.iter().enumerate() {
            let value = lookup(&column.name).ok_or_else(|| {
                anyhow!("no generated value for column `{}`", column.name)
            })?;
            if matches!(value, Value::Null) && !column.nullable {
                bail!("column `{}` is NOT NULL but its generator produced null", column.name);
            }
            self.builders[index]
                .append(value)
                .with_context(|| format!("column `{}` ({})", column.name, column.ty.sql_name()))?;
        }
        self.rows += 1;
        Ok(())
    }

    /// Take the accumulated rows as a record batch, resetting the builder.
    pub fn finish(&mut self) -> Result<arrow::record_batch::RecordBatch> {
        let arrays = self
            .builders
            .iter_mut()
            .zip(&self.columns)
            .map(|(builder, column)| {
                builder
                    .finish()
                    .with_context(|| format!("column `{}` ({})", column.name, column.ty.sql_name()))
            })
            .collect::<Result<Vec<_>>>()?;
        let batch = arrow::record_batch::RecordBatch::try_new(self.schema.clone(), arrays)?;
        self.rows = 0;
        // Builders reset themselves on finish, but capacity is not retained.
        for (index, column) in self.columns.iter().enumerate() {
            self.builders[index] = ColumnBuilder::new(&column.ty, self.capacity)?;
        }
        Ok(batch)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::schema::parse_schema;

    fn column(sql_type: &str) -> Column {
        let sql = format!("CREATE TABLE t (c {}) ENGINE=OLAP", sql_type);
        parse_schema(&sql).expect("parse").remove(0).columns.remove(0)
    }

    #[test]
    fn maps_doris_types_to_arrow() {
        assert_eq!(arrow_type(&column("BOOLEAN").ty).unwrap(), DataType::Boolean);
        assert_eq!(arrow_type(&column("INT").ty).unwrap(), DataType::Int32);
        assert_eq!(arrow_type(&column("BIGINT").ty).unwrap(), DataType::Int64);
        // Doris exports LARGEINT as text; it is the only form holding 39 digits.
        assert_eq!(arrow_type(&column("LARGEINT").ty).unwrap(), DataType::Utf8);
        assert_eq!(
            arrow_type(&column("DECIMAL(19,4)").ty).unwrap(),
            DataType::Decimal128(19, 4)
        );
        assert_eq!(arrow_type(&column("DATE").ty).unwrap(), DataType::Date32);
        assert_eq!(
            arrow_type(&column("DATETIME(6)").ty).unwrap(),
            DataType::Timestamp(TimeUnit::Microsecond, None)
        );
        assert_eq!(arrow_type(&column("VARCHAR(256)").ty).unwrap(), DataType::Utf8);
    }

    #[test]
    fn schema_carries_nullability_from_the_ddl() {
        let tables = parse_schema("CREATE TABLE t (a INT NOT NULL, b INT) ENGINE=OLAP").unwrap();
        let schema = arrow_schema(&tables[0].columns).unwrap();
        assert!(!schema.field(0).is_nullable());
        assert!(schema.field(1).is_nullable());
    }

    /// The typed fast path exists only as an optimisation, so it has to agree
    /// with parsing the text the same value renders to.
    #[test]
    fn typed_decimals_agree_with_the_text_path() {
        for (units, scale) in [(451_200i128, 4u32), (-451_200, 4), (10_000, 4), (7, 4)] {
            let typed = Value::Decimal { units, scale };
            let text = Value::String(typed.csv_string(""));
            assert_eq!(
                as_decimal(&typed, 19, 4).unwrap(),
                as_decimal(&text, 19, 4).unwrap(),
                "disagreement on {:?}",
                typed
            );
        }
        // A coarser value than the column scales up, exactly as the text does.
        let coarse = Value::Decimal {
            units: 4512,
            scale: 2,
        };
        assert_eq!(as_decimal(&coarse, 19, 4).unwrap(), 451_200);
    }

    #[test]
    fn typed_decimals_refuse_to_drop_digits_or_overflow() {
        // 0.123456 at scale 6 does not fit a scale-4 column.
        let too_precise = Value::Decimal {
            units: 123_456,
            scale: 6,
        };
        assert!(as_decimal(&too_precise, 19, 4).is_err());
        // 12345.60 does not fit precision 5.
        let too_large = Value::Decimal {
            units: 1_234_560,
            scale: 2,
        };
        assert!(as_decimal(&too_large, 5, 2).is_err());
    }

    #[test]
    fn typed_timestamps_agree_with_the_text_path() {
        let format: Arc<str> = Arc::from("%Y-%m-%d %H:%M:%S%.6f");
        for micros in [1_757_500_000_123_456i64, 0, -86_400_000_000] {
            let typed = Value::Timestamp {
                micros,
                format: format.clone(),
            };
            let text = Value::String(typed.csv_string(""));
            assert_eq!(as_timestamp_micros(&typed).unwrap(), micros);
            assert_eq!(as_timestamp_micros(&text).unwrap(), micros);
            assert_eq!(
                as_date(&typed).unwrap(),
                as_date(&text).unwrap(),
                "date disagreement at {}",
                micros
            );
        }
    }

    #[test]
    fn parses_decimals_at_the_column_scale() {
        // Two decimal places into a scale-4 column pads with zeros.
        assert_eq!(as_decimal(&Value::String("45.12".into()), 19, 4).unwrap(), 451_200);
        assert_eq!(as_decimal(&Value::String("1.00".into()), 19, 4).unwrap(), 10_000);
        assert_eq!(as_decimal(&Value::String("-3.5".into()), 19, 4).unwrap(), -35_000);
        assert_eq!(as_decimal(&Value::String("7".into()), 19, 4).unwrap(), 70_000);
    }

    #[test]
    fn refuses_decimals_that_would_lose_data() {
        let too_precise = as_decimal(&Value::String("1.234567".into()), 19, 4);
        assert!(too_precise.is_err(), "excess decimal places must not be truncated");

        let too_large = as_decimal(&Value::String("12345.6".into()), 5, 2);
        assert!(too_large.is_err(), "value must fit the declared precision");

        assert!(as_decimal(&Value::String("abc".into()), 19, 4).is_err());
    }

    #[test]
    fn parses_dates_and_timestamps() {
        let epoch = as_date(&Value::String("1970-01-01".into())).unwrap();
        assert_eq!(epoch, 0);
        assert_eq!(as_date(&Value::String("1970-01-02".into())).unwrap(), 1);
        // A timestamp is accepted where a date is wanted.
        assert_eq!(as_date(&Value::String("1970-01-02 13:45:00".into())).unwrap(), 1);

        assert_eq!(
            as_timestamp_micros(&Value::String("1970-01-01 00:00:01".into())).unwrap(),
            1_000_000
        );
        // Microsecond precision survives, matching datetime(6).
        assert_eq!(
            as_timestamp_micros(&Value::String("1970-01-01 00:00:00.123456".into())).unwrap(),
            123_456
        );
        assert!(as_timestamp_micros(&Value::String("not a time".into())).is_err());
    }

    #[test]
    fn integer_range_is_enforced_per_column_width() {
        assert!(as_int(&Value::I64(200), i8::MIN as i128, i8::MAX as i128, "TINYINT").is_err());
        assert!(as_int(&Value::I64(100), i8::MIN as i128, i8::MAX as i128, "TINYINT").is_ok());
    }

    #[test]
    fn builds_a_record_batch_from_generated_values() {
        let tables = parse_schema(
            "CREATE TABLE t (id BIGINT NOT NULL, amount DECIMAL(19,4), ts DATETIME(6), note VARCHAR(64)) ENGINE=OLAP",
        )
        .unwrap();
        let columns = tables[0].columns.clone();
        let mut builder = BatchBuilder::new(columns, 8).unwrap();

        let row: Vec<(String, Value)> = vec![
            ("id".into(), Value::I64(7)),
            ("amount".into(), Value::String("45.12".into())),
            ("ts".into(), Value::String("2026-09-10 12:00:00.500000".into())),
            ("note".into(), Value::String("hello".into())),
        ];
        builder
            .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, value)| value))
            .unwrap();

        // A null in a nullable column is fine.
        let with_null: Vec<(String, Value)> = vec![
            ("id".into(), Value::I64(8)),
            ("amount".into(), Value::Null),
            ("ts".into(), Value::Null),
            ("note".into(), Value::Null),
        ];
        builder
            .append_row(|name| {
                with_null.iter().find(|(key, _)| key == name).map(|(_, value)| value)
            })
            .unwrap();

        assert_eq!(builder.rows(), 2);
        let batch = builder.finish().unwrap();
        assert_eq!(batch.num_rows(), 2);
        assert_eq!(batch.num_columns(), 4);
        assert!(builder.is_empty(), "builder resets after finish");
    }

    #[test]
    fn missing_column_value_is_an_error() {
        let tables = parse_schema("CREATE TABLE t (a INT, b INT) ENGINE=OLAP").unwrap();
        let mut builder = BatchBuilder::new(tables[0].columns.clone(), 4).unwrap();
        let row = vec![("a".to_string(), Value::I64(1))];
        let err = builder
            .append_row(|name| row.iter().find(|(key, _)| key == name).map(|(_, value)| value))
            .expect_err("column b is missing");
        assert!(err.to_string().contains('b'), "{}", err);
    }
}

#[cfg(test)]
mod inspect {
    /// Prints the schema and first rows of a generated file, for eyeballing.
    #[test]
    #[ignore]
    fn dump() {
        let path = std::env::var("DUMP_PARQUET").expect("set DUMP_PARQUET");
        let file = std::fs::File::open(&path).expect("open");
        let builder =
            parquet::arrow::arrow_reader::ParquetRecordBatchReaderBuilder::try_new(file).unwrap();
        let meta = builder.metadata().clone();
        println!("rows: {}", meta.file_metadata().num_rows());
        println!("row groups: {}", meta.num_row_groups());
        println!("compression: {:?}", meta.row_group(0).column(0).compression());
        println!("\nschema:");
        for field in builder.schema().fields() {
            println!(
                "  {:22} {:34} nullable={}",
                field.name(),
                format!("{:?}", field.data_type()),
                field.is_nullable()
            );
        }
        let mut reader = builder.with_batch_size(2).build().unwrap();
        let batch = reader.next().unwrap().unwrap();
        println!("\nfirst row:");
        for (index, field) in batch.schema().fields().iter().enumerate() {
            let column = batch.column(index);
            println!("  {:22} {}", field.name(), arrow::util::display::array_value_to_string(column, 0).unwrap());
        }
    }
}
