//! The Arrow schema behind Parquet output: inferred from the generators, or
//! taken from an Avro schema when one is configured.

use std::collections::HashMap;
use std::sync::Arc;

use anyhow::{anyhow, bail, Context, Result};
use apache_avro::schema::{ResolvedSchema, Schema as AvroSchema};
use arrow::datatypes::{DataType, Field, Fields, Schema, TimeUnit};

use crate::compile::CompiledSpec;
use crate::schema::tree::{Node, RowTree};
use crate::value::Value;

/// Every column nullable, typed by what its generator can produce.
/// Decimals and 128-bit integers become `Decimal128(38, scale)`, timestamps
/// microsecond timestamps, and a generator whose output cannot be predicted
/// (`template`, `javascript`) a string.
pub fn infer_schema(spec: &CompiledSpec) -> Result<Schema> {
    let tree = RowTree::from_spec(spec)?;
    let probes: HashMap<&str, Vec<Value>> = spec
        .fields
        .iter()
        .map(|field| (field.name.as_str(), field.generator.probe_values()))
        .collect();
    Ok(Schema::new(fields_for(&tree.root, &probes)))
}

fn fields_for(nodes: &[(String, Node)], probes: &HashMap<&str, Vec<Value>>) -> Vec<Field> {
    nodes
        .iter()
        .map(|(name, node)| match node {
            Node::Leaf(row_name) => Field::new(
                name,
                type_of_values(probes.get(row_name.as_str()).map(Vec::as_slice).unwrap_or(&[])),
                true,
            ),
            Node::Record(children) => Field::new(name, DataType::Struct(fields_for(children, probes).into()), true),
        })
        .collect()
}

/// Parquet's widest decimal.
const DECIMAL_DIGITS: usize = 38;

fn type_of_values(values: &[Value]) -> DataType {
    // Take the widest view: a decimal's scale from any probe, the widest
    // digit count (the probes are the generator's bounds), and nested
    // shapes from the first non-null one.
    let mut scale: Option<u32> = None;
    let mut digits = 0usize;
    let mut wide = false;
    let mut first = None;
    for value in values {
        match value {
            Value::Null => continue,
            Value::Decimal { units, scale: s } => {
                scale = Some(scale.map_or(*s, |current| current.max(*s)));
                digits = digits.max(units.unsigned_abs().to_string().len());
                wide = true;
            }
            Value::I128(number) => {
                digits = digits.max(number.unsigned_abs().to_string().len());
                wide = true;
            }
            _ => {}
        }
        first.get_or_insert(value);
    }
    match first {
        None => DataType::Utf8,
        // A range with one bound past 64 bits is decimal-wide as a whole;
        // beyond 38 digits nothing numeric in Parquet holds the value
        // exactly, so it travels as text.
        Some(_) if wide && digits > DECIMAL_DIGITS => DataType::Utf8,
        Some(_) if wide => DataType::Decimal128(DECIMAL_DIGITS as u8, scale.unwrap_or(0) as i8),
        Some(value) => type_of(value),
    }
}

fn type_of(value: &Value) -> DataType {
    match value {
        Value::Null => DataType::Utf8,
        Value::Bool(_) => DataType::Boolean,
        Value::I64(_) => DataType::Int64,
        Value::I128(_) => DataType::Decimal128(DECIMAL_DIGITS as u8, 0),
        Value::F64(_) => DataType::Float64,
        Value::String(_) => DataType::Utf8,
        Value::Timestamp { .. } => DataType::Timestamp(TimeUnit::Microsecond, None),
        Value::Decimal { scale, .. } => DataType::Decimal128(DECIMAL_DIGITS as u8, *scale as i8),
        Value::List(items) => DataType::List(Arc::new(Field::new("item", type_of_values(items), true))),
        Value::Struct(fields) => DataType::Struct(
            fields
                .iter()
                .map(|(name, value)| Field::new(name, type_of(value), true))
                .collect::<Fields>(),
        ),
        Value::Map(entries) => {
            let values: Vec<Value> = entries.iter().map(|(_, value)| value.clone()).collect();
            map_type(type_of_values(&values))
        }
    }
}

fn map_type(values: DataType) -> DataType {
    DataType::Map(
        Arc::new(Field::new(
            "key_value",
            DataType::Struct(Fields::from(vec![
                Field::new("key", DataType::Utf8, false),
                Field::new("value", values, true),
            ])),
            false,
        )),
        false,
    )
}

/// The Arrow schema an Avro record maps to. The generated JSON must decode
/// into it, so types that JSON cannot carry losslessly (bytes, fixed, date
/// and time-of-day logical types, durations) are refused with a pointer to
/// what to use instead.
pub fn schema_from_avro(text: &str) -> Result<Schema> {
    let avro = AvroSchema::parse_str(text).context("failed to parse the Avro schema")?;
    let resolved = ResolvedSchema::new(&avro).context("failed to resolve named types in the Avro schema")?;
    let names: HashMap<String, AvroSchema> = resolved
        .get_names()
        .iter()
        .map(|(name, schema)| (name.fullname(None), (*schema).clone()))
        .collect();
    let AvroSchema::Record(record) = &avro else {
        bail!("the Avro schema for Parquet must be a record at the top level");
    };
    let fields = record
        .fields
        .iter()
        .map(|field| {
            let (data_type, nullable) = avro_type(&field.schema, &names)
                .with_context(|| format!("Avro field `{}`", field.name))?;
            Ok(Field::new(&field.name, data_type, nullable || field.default.is_some()))
        })
        .collect::<Result<Vec<_>>>()?;
    Ok(Schema::new(fields))
}

fn avro_type(schema: &AvroSchema, names: &HashMap<String, AvroSchema>) -> Result<(DataType, bool)> {
    let resolve = |schema: &AvroSchema| -> Result<AvroSchema> {
        match schema {
            AvroSchema::Ref { name } => names
                .get(&name.fullname(None))
                .cloned()
                .ok_or_else(|| anyhow!("unknown type `{}`", name.fullname(None))),
            other => Ok(other.clone()),
        }
    };
    let schema = resolve(schema)?;
    Ok(match &schema {
        AvroSchema::Null => (DataType::Null, true),
        AvroSchema::Boolean => (DataType::Boolean, false),
        AvroSchema::Int => (DataType::Int32, false),
        AvroSchema::Long => (DataType::Int64, false),
        AvroSchema::Float => (DataType::Float32, false),
        AvroSchema::Double => (DataType::Float64, false),
        AvroSchema::String | AvroSchema::Enum(_) | AvroSchema::Uuid(_) => (DataType::Utf8, false),
        AvroSchema::Bytes | AvroSchema::Fixed(_) => {
            bail!("bytes and fixed have no JSON form; use string for Parquet output")
        }
        AvroSchema::Decimal(decimal) => {
            if decimal.precision > 38 {
                bail!("decimal precision {} is above Parquet's 38", decimal.precision);
            }
            (DataType::Decimal128(decimal.precision as u8, decimal.scale as i8), false)
        }
        AvroSchema::BigDecimal => bail!("big-decimal is not supported for Parquet output"),
        AvroSchema::Date | AvroSchema::TimeMillis | AvroSchema::TimeMicros => {
            bail!("date and time-of-day logical types are not supported for Parquet output; use timestamp-micros")
        }
        AvroSchema::TimestampMillis | AvroSchema::LocalTimestampMillis => {
            (DataType::Timestamp(TimeUnit::Millisecond, None), false)
        }
        AvroSchema::TimestampMicros | AvroSchema::LocalTimestampMicros => {
            (DataType::Timestamp(TimeUnit::Microsecond, None), false)
        }
        AvroSchema::TimestampNanos | AvroSchema::LocalTimestampNanos => {
            (DataType::Timestamp(TimeUnit::Nanosecond, None), false)
        }
        AvroSchema::Duration(_) => bail!("duration is not supported for Parquet output"),
        AvroSchema::Array(array) => {
            let (items, nullable) = avro_type(&array.items, names)?;
            (DataType::List(Arc::new(Field::new("item", items, nullable))), false)
        }
        AvroSchema::Map(map) => {
            let (values, _) = avro_type(&map.types, names)?;
            (map_type(values), false)
        }
        AvroSchema::Record(record) => {
            let fields = record
                .fields
                .iter()
                .map(|field| {
                    let (data_type, nullable) = avro_type(&field.schema, names)
                        .with_context(|| format!("field `{}`", field.name))?;
                    Ok(Field::new(&field.name, data_type, nullable || field.default.is_some()))
                })
                .collect::<Result<Vec<_>>>()?;
            (DataType::Struct(fields.into()), false)
        }
        AvroSchema::Union(union) => {
            let branches: Vec<&AvroSchema> = union
                .variants()
                .iter()
                .filter(|variant| !matches!(variant, AvroSchema::Null))
                .collect();
            match branches.as_slice() {
                [only] => (avro_type(only, names)?.0, true),
                [] => (DataType::Null, true),
                _ => bail!("a union with several non-null branches has no single Parquet type"),
            }
        }
        AvroSchema::Ref { .. } => unreachable!("resolved above"),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::compile_toml;

    #[test]
    fn infers_types_from_generators() {
        let spec = compile_toml(
            r#"
version = 1
[fields.id]
type = "uuid"
[fields."customer.name"]
type = "name"
[fields.age]
type = "int_range"
min = 1
max = 9
[fields.big]
type = "int_range"
min = "170141183460469231731687303715884105000"
max = "170141183460469231731687303715884105727"
[fields.amount]
type = "decimal_range"
min = "1.00"
max = "9.99"
scale = 2
[fields.at]
type = "datetime_range"
start = "2024-01-01T00:00:00Z"
end = "2024-01-02T00:00:00Z"
[fields.tags]
type = "array"
element = { type = "choice", values = ["a"] }
[fields.attrs]
type = "map"
key = { type = "choice", values = ["k"] }
value = { type = "float_range", min = 0.0, max = 1.0 }
[fields.label]
type = "template"
value = "{{id}}"
"#,
        )
        .unwrap();
        let schema = infer_schema(&spec).unwrap();
        let field = |name: &str| schema.field_with_name(name).unwrap().data_type().clone();
        assert_eq!(field("id"), DataType::Utf8);
        assert!(matches!(field("customer"), DataType::Struct(fields) if fields[0].name() == "name"));
        assert_eq!(field("age"), DataType::Int64);
        assert_eq!(field("big"), DataType::Utf8, "39-digit bounds go as text");
        let wide = compile_toml("version = 1\n[fields.w]\ntype = \"int_range\"\nmin = 0\nmax = \"99999999999999999999999999999999999999\"\n").unwrap();
        assert_eq!(infer_schema(&wide).unwrap().field(0).data_type(), &DataType::Decimal128(38, 0), "38 digits fit a decimal");
        assert_eq!(field("amount"), DataType::Decimal128(38, 2));
        assert_eq!(field("at"), DataType::Timestamp(TimeUnit::Microsecond, None));
        assert!(matches!(field("tags"), DataType::List(item) if item.data_type() == &DataType::Utf8));
        assert!(matches!(field("attrs"), DataType::Map(_, _)));
        assert_eq!(field("label"), DataType::Utf8, "unpredictable output is text");
        assert!(schema.fields().iter().all(|f| f.is_nullable()));
    }

    #[test]
    fn maps_an_avro_record() {
        let schema = schema_from_avro(
            r#"{"type":"record","name":"E","fields":[
              {"name":"id","type":"string"},
              {"name":"n","type":["null","int"],"default":null},
              {"name":"amount","type":{"type":"bytes","logicalType":"decimal","precision":10,"scale":2}},
              {"name":"at","type":{"type":"long","logicalType":"timestamp-millis"}},
              {"name":"tags","type":{"type":"array","items":"string"}},
              {"name":"c","type":{"type":"record","name":"C","fields":[{"name":"x","type":"double"}]}}
            ]}"#,
        )
        .unwrap();
        let get = |name: &str| schema.field_with_name(name).unwrap().clone();
        assert!(!get("id").is_nullable());
        assert_eq!(get("n").data_type(), &DataType::Int32);
        assert!(get("n").is_nullable());
        assert_eq!(get("amount").data_type(), &DataType::Decimal128(10, 2));
        assert_eq!(get("at").data_type(), &DataType::Timestamp(TimeUnit::Millisecond, None));
        assert!(matches!(get("c").data_type(), DataType::Struct(_)));

        let error = schema_from_avro(r#"{"type":"record","name":"E","fields":[{"name":"b","type":"bytes"}]}"#)
            .expect_err("bytes");
        assert!(format!("{:#}", error).contains("bytes"), "{:#}", error);
    }
}
