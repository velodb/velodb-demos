//! Avro encoding against a user-supplied `.avsc`.
//!
//! The schema decides the shape and the types; the spec decides the values.
//! At startup the field tree is checked against the schema and every
//! generator's extreme values are pushed through the conversion, so a `name`
//! generator aimed at a `long` fails before the run rather than on row one.

use std::collections::HashMap;

use anyhow::{anyhow, bail, Context, Result};
use apache_avro::schema::{
    DecimalSchema, EnumSchema, InnerDecimalSchema, Name, RecordSchema, ResolvedSchema, Schema,
    UnionSchema,
};
use apache_avro::types::Value as AvroValue;

use crate::compile::{CompiledSpec, RowContext};
use crate::generators::format_timestamp_micros;
use crate::schema::tree::{Node, RowTree};
use crate::schema::{i128_to_signed_be_bytes, rescale_units};
use crate::value::Value;

pub struct AvroEncoder {
    schema: Schema,
    /// Named types by full name, for `Ref` schemas.
    names: HashMap<String, Schema>,
    tree: RowTree,
    schema_text: String,
}

impl AvroEncoder {
    /// Parse the schema, match the spec's fields against it, and probe every
    /// generator's extremes through the conversion.
    pub fn new(schema_text: &str, spec: &CompiledSpec) -> Result<Self> {
        let schema = Schema::parse_str(schema_text).context("failed to parse the Avro schema")?;
        let names = ResolvedSchema::new(&schema)
            .context("failed to resolve named types in the Avro schema")?
            .get_names()
            .iter()
            .map(|(name, schema)| (name.fullname(None), (*schema).clone()))
            .collect();
        let tree = RowTree::from_spec(spec)?;
        let encoder = Self {
            schema_text: schema.canonical_form(),
            schema,
            names,
            tree,
        };
        let probes: HashMap<&str, Vec<Value>> = spec
            .fields
            .iter()
            .map(|field| (field.name.as_str(), field.generator.probe_values()))
            .collect();
        encoder.validate_record(&encoder.tree.root, &encoder.schema, "", &probes)?;
        Ok(encoder)
    }

    /// The schema in Parsing Canonical Form, which is what a registry and a
    /// stream prelude carry.
    pub fn schema_text(&self) -> &str {
        &self.schema_text
    }

    /// One row as a bare Avro datum: no container, no schema, no length.
    pub fn encode(&self, ctx: &RowContext) -> Result<Vec<u8>> {
        let value = self.encode_record(&self.tree.root, ctx, &self.schema)?;
        // The crate still exports this; its replacement is builder-only
        // and buys nothing for a single-schema writer.
        #[allow(deprecated)]
        apache_avro::to_avro_datum(&self.schema, value).context("failed to encode an Avro datum")
    }

    fn resolve<'a>(&'a self, schema: &'a Schema) -> Result<&'a Schema> {
        match schema {
            Schema::Ref { name } => self.lookup(name),
            other => Ok(other),
        }
    }

    fn lookup(&self, name: &Name) -> Result<&Schema> {
        let full = name.fullname(None);
        self.names
            .get(&full)
            .or_else(|| {
                self.names
                    .iter()
                    .find(|(key, _)| key.rsplit('.').next() == Some(full.as_str()))
                    .map(|(_, schema)| schema)
            })
            .ok_or_else(|| anyhow!("Avro schema references unknown type `{}`", full))
    }

    /// The record schema behind `schema`, through refs and a nullable union.
    /// Returns the union branch index when it went through one.
    fn record_schema<'a>(&'a self, schema: &'a Schema) -> Result<(&'a RecordSchema, Option<u32>)> {
        match self.resolve(schema)? {
            Schema::Record(record) => Ok((record, None)),
            Schema::Union(union) => {
                for (index, variant) in union.variants().iter().enumerate() {
                    if let Schema::Record(record) = self.resolve(variant)? {
                        return Ok((record, Some(index as u32)));
                    }
                }
                bail!("union has no record branch")
            }
            other => bail!("expected a record, the schema has {:?}", kind_name(other)),
        }
    }

    // ---- startup validation ----

    fn validate_record(
        &self,
        nodes: &[(String, Node)],
        schema: &Schema,
        path: &str,
        probes: &HashMap<&str, Vec<Value>>,
    ) -> Result<()> {
        let (record, _) = self
            .record_schema(schema)
            .with_context(|| format!("field `{}` is a record in the spec", show_path(path)))?;
        for (name, node) in nodes {
            let child_path = join_path(path, name);
            let field = record.fields.iter().find(|field| &field.name == name).ok_or_else(|| {
                anyhow!(
                    "spec field `{}` is not in the Avro schema (record `{}` has: {})",
                    child_path,
                    record.name.fullname(None),
                    record.fields.iter().map(|f| f.name.as_str()).collect::<Vec<_>>().join(", ")
                )
            })?;
            match node {
                Node::Record(children) => {
                    self.validate_record(children, &field.schema, &child_path, probes)?
                }
                Node::Leaf(row_name) => {
                    for probe in probes.get(row_name.as_str()).map(Vec::as_slice).unwrap_or(&[]) {
                        self.convert(probe, &field.schema).with_context(|| {
                            format!(
                                "field `{}` can produce {} which the Avro schema does not accept",
                                child_path,
                                describe(probe)
                            )
                        })?;
                    }
                }
            }
        }
        for field in &record.fields {
            if nodes.iter().any(|(name, _)| name == &field.name) {
                continue;
            }
            let nullable = matches!(self.resolve(&field.schema)?, Schema::Union(u) if u.is_nullable())
                || matches!(self.resolve(&field.schema)?, Schema::Null);
            if !nullable && field.default.is_none() {
                bail!(
                    "Avro field `{}` has no spec entry, is not nullable and has no default",
                    join_path(path, &field.name)
                );
            }
        }
        Ok(())
    }

    // ---- per-row encoding ----

    fn encode_record(&self, nodes: &[(String, Node)], ctx: &RowContext, schema: &Schema) -> Result<AvroValue> {
        let (record, branch) = self.record_schema(schema)?;
        let mut fields = Vec::with_capacity(record.fields.len());
        for field in &record.fields {
            let value = match nodes.iter().find(|(name, _)| name == &field.name) {
                Some((_, Node::Leaf(row_name))) => {
                    let value = ctx
                        .get(row_name)
                        .ok_or_else(|| anyhow!("no generated value for field `{}`", row_name))?;
                    self.convert(value, &field.schema)
                        .with_context(|| format!("field `{}`", row_name))?
                }
                Some((_, Node::Record(children))) => self.encode_record(children, ctx, &field.schema)?,
                None => self.missing_field(field)?,
            };
            fields.push((field.name.clone(), value));
        }
        let record = AvroValue::Record(fields);
        Ok(match branch {
            Some(index) => AvroValue::Union(index, Box::new(record)),
            None => record,
        })
    }

    fn missing_field(&self, field: &apache_avro::schema::RecordField) -> Result<AvroValue> {
        if let Some(default) = &field.default {
            return self.convert(&Value::from_json(default.clone()), &field.schema);
        }
        self.convert(&Value::Null, &field.schema)
            .with_context(|| format!("Avro field `{}` has no spec entry", field.name))
    }

    /// A generated value as the Avro value `schema` wants.
    pub fn convert(&self, value: &Value, schema: &Schema) -> Result<AvroValue> {
        let schema = self.resolve(schema)?;
        if let Schema::Union(union) = schema {
            return self.convert_union(value, union);
        }
        let mismatch = || anyhow!("{} does not fit Avro type {}", describe(value), kind_name(schema));
        Ok(match (schema, value) {
            (Schema::Null, Value::Null) => AvroValue::Null,
            (_, Value::Null) => bail!("null is not allowed: Avro type {} is not nullable", kind_name(schema)),

            (Schema::Boolean, Value::Bool(flag)) => AvroValue::Boolean(*flag),

            (Schema::Int, Value::I64(number)) => AvroValue::Int(i32::try_from(*number).map_err(|_| mismatch())?),
            (Schema::Int, Value::I128(number)) => AvroValue::Int(i32::try_from(*number).map_err(|_| mismatch())?),
            (Schema::Int, Value::Decimal { units, scale: 0 }) => AvroValue::Int(i32::try_from(*units).map_err(|_| mismatch())?),

            (Schema::Long, Value::I64(number)) => AvroValue::Long(*number),
            (Schema::Long, Value::I128(number)) => AvroValue::Long(i64::try_from(*number).map_err(|_| mismatch())?),
            (Schema::Long, Value::Timestamp { micros, .. }) => AvroValue::Long(*micros),
            (Schema::Long, Value::Decimal { units, scale: 0 }) => AvroValue::Long(i64::try_from(*units).map_err(|_| mismatch())?),

            (Schema::Float, Value::F64(number)) => AvroValue::Float(*number as f32),
            (Schema::Float, Value::I64(number)) => AvroValue::Float(*number as f32),
            (Schema::Float, Value::Decimal { units, scale }) => AvroValue::Float(decimal_f64(*units, *scale) as f32),
            (Schema::Double, Value::F64(number)) => AvroValue::Double(*number),
            (Schema::Double, Value::I64(number)) => AvroValue::Double(*number as f64),
            (Schema::Double, Value::Decimal { units, scale }) => AvroValue::Double(decimal_f64(*units, *scale)),

            (Schema::Bytes, Value::String(text)) => AvroValue::Bytes(text.as_bytes().to_vec()),
            (Schema::Fixed(fixed), Value::String(text)) => {
                if text.len() != fixed.size {
                    bail!("fixed `{}` needs exactly {} bytes, got {}", fixed.name.fullname(None), fixed.size, text.len());
                }
                AvroValue::Fixed(fixed.size, text.as_bytes().to_vec())
            }

            (Schema::String, Value::String(text)) => AvroValue::String(text.clone()),
            (Schema::String, Value::Timestamp { micros, format }) => AvroValue::String(format_timestamp_micros(*micros, format)),
            (Schema::String, Value::List(_) | Value::Struct(_) | Value::Map(_)) => AvroValue::String(value.to_json_string()),
            (Schema::String, other) => AvroValue::String(other.csv_string("")),

            (Schema::Enum(symbols), Value::String(symbol)) => enum_value(symbols, symbol)?,
            (Schema::Enum(symbols), Value::I64(index)) => {
                let symbol = usize::try_from(*index).ok().and_then(|i| symbols.symbols.get(i)).ok_or_else(mismatch)?;
                AvroValue::Enum(*index as u32, symbol.clone())
            }

            (Schema::Decimal(decimal), _) => self.convert_decimal(value, decimal)?,
            (Schema::BigDecimal, _) => bail!("the big-decimal logical type is not supported"),

            (Schema::Uuid(_), Value::String(text)) => AvroValue::Uuid(
                apache_avro::Uuid::parse_str(text).map_err(|_| anyhow!("`{}` is not a UUID", text))?,
            ),

            (Schema::Date, Value::I64(days)) => AvroValue::Date(i32::try_from(*days).map_err(|_| mismatch())?),
            (Schema::Date, Value::Timestamp { micros, .. }) => AvroValue::Date((micros.div_euclid(86_400_000_000)) as i32),
            (Schema::TimeMillis, Value::I64(n)) => AvroValue::TimeMillis(i32::try_from(*n).map_err(|_| mismatch())?),
            (Schema::TimeMicros, Value::I64(n)) => AvroValue::TimeMicros(*n),

            (Schema::TimestampMillis, Value::Timestamp { micros, .. }) => AvroValue::TimestampMillis(micros.div_euclid(1000)),
            (Schema::TimestampMillis, Value::I64(n)) => AvroValue::TimestampMillis(*n),
            (Schema::TimestampMicros, Value::Timestamp { micros, .. }) => AvroValue::TimestampMicros(*micros),
            (Schema::TimestampMicros, Value::I64(n)) => AvroValue::TimestampMicros(*n),
            (Schema::TimestampNanos, Value::Timestamp { micros, .. }) => {
                AvroValue::TimestampNanos(micros.checked_mul(1000).ok_or_else(mismatch)?)
            }
            (Schema::TimestampNanos, Value::I64(n)) => AvroValue::TimestampNanos(*n),
            (Schema::LocalTimestampMillis, Value::Timestamp { micros, .. }) => AvroValue::LocalTimestampMillis(micros.div_euclid(1000)),
            (Schema::LocalTimestampMillis, Value::I64(n)) => AvroValue::LocalTimestampMillis(*n),
            (Schema::LocalTimestampMicros, Value::Timestamp { micros, .. }) => AvroValue::LocalTimestampMicros(*micros),
            (Schema::LocalTimestampMicros, Value::I64(n)) => AvroValue::LocalTimestampMicros(*n),
            (Schema::LocalTimestampNanos, Value::Timestamp { micros, .. }) => {
                AvroValue::LocalTimestampNanos(micros.checked_mul(1000).ok_or_else(mismatch)?)
            }
            (Schema::LocalTimestampNanos, Value::I64(n)) => AvroValue::LocalTimestampNanos(*n),

            (Schema::Array(array), Value::List(items)) => AvroValue::Array(
                items.iter().map(|item| self.convert(item, &array.items)).collect::<Result<_>>()?,
            ),
            (Schema::Map(map), Value::Map(entries)) => AvroValue::Map(
                entries
                    .iter()
                    .map(|(key, item)| Ok((key.csv_string(""), self.convert(item, &map.types)?)))
                    .collect::<Result<_>>()?,
            ),
            (Schema::Map(map), Value::Struct(fields)) => AvroValue::Map(
                fields
                    .iter()
                    .map(|(key, item)| Ok((key.clone(), self.convert(item, &map.types)?)))
                    .collect::<Result<_>>()?,
            ),
            (Schema::Record(record), Value::Struct(fields)) => {
                let mut out = Vec::with_capacity(record.fields.len());
                for field in &record.fields {
                    let converted = match fields.iter().find(|(name, _)| name == &field.name) {
                        Some((_, item)) => self
                            .convert(item, &field.schema)
                            .with_context(|| format!("struct field `{}`", field.name))?,
                        None => self.missing_field(field)?,
                    };
                    out.push((field.name.clone(), converted));
                }
                AvroValue::Record(out)
            }
            _ => return Err(mismatch()),
        })
    }

    fn convert_union(&self, value: &Value, union: &UnionSchema) -> Result<AvroValue> {
        let variants = union.variants();
        if matches!(value, Value::Null) {
            let index = variants
                .iter()
                .position(|variant| matches!(variant, Schema::Null))
                .ok_or_else(|| anyhow!("null is not allowed: the union has no null branch"))?;
            return Ok(AvroValue::Union(index as u32, Box::new(AvroValue::Null)));
        }
        let mut errors = Vec::new();
        for (index, variant) in variants.iter().enumerate() {
            if matches!(variant, Schema::Null) {
                continue;
            }
            match self.convert(value, variant) {
                Ok(converted) => return Ok(AvroValue::Union(index as u32, Box::new(converted))),
                Err(error) => errors.push(format!("{:#}", error)),
            }
        }
        bail!("{} fits no branch of the union: {}", describe(value), errors.join("; "))
    }

    fn convert_decimal(&self, value: &Value, decimal: &DecimalSchema) -> Result<AvroValue> {
        let target_scale = decimal.scale as u32;
        let units = match value {
            Value::Decimal { units, scale } => rescale_units(*units, *scale, target_scale)?,
            Value::I64(number) => rescale_units(*number as i128, 0, target_scale)?,
            Value::I128(number) => rescale_units(*number, 0, target_scale)?,
            Value::String(text) => {
                let parsed: rust_decimal::Decimal =
                    text.parse().map_err(|_| anyhow!("`{}` is not a decimal number", text))?;
                crate::generators::decimal_to_units(parsed, target_scale, "decimal")?
            }
            other => bail!("{} does not fit an Avro decimal", describe(other)),
        };
        let digits = units.unsigned_abs().to_string().len();
        if digits > decimal.precision {
            bail!(
                "decimal {} has {} digits, more than the schema precision of {}",
                crate::generators::format_decimal_units(units, target_scale),
                digits,
                decimal.precision
            );
        }
        let mut bytes = i128_to_signed_be_bytes(units);
        if let InnerDecimalSchema::Fixed(fixed) = &decimal.inner {
            if bytes.len() > fixed.size {
                bail!("decimal needs {} bytes but the fixed type holds {}", bytes.len(), fixed.size);
            }
            let fill = if units < 0 { 0xFF } else { 0x00 };
            let mut padded = vec![fill; fixed.size - bytes.len()];
            padded.append(&mut bytes);
            bytes = padded;
        }
        Ok(AvroValue::Decimal(apache_avro::Decimal::from(bytes)))
    }
}

fn enum_value(schema: &EnumSchema, symbol: &str) -> Result<AvroValue> {
    let index = schema
        .symbols
        .iter()
        .position(|candidate| candidate == symbol)
        .or_else(|| schema.symbols.iter().position(|candidate| candidate.eq_ignore_ascii_case(symbol)))
        .ok_or_else(|| {
            anyhow!(
                "`{}` is not a symbol of enum `{}` ({})",
                symbol,
                schema.name.fullname(None),
                schema.symbols.join(", ")
            )
        })?;
    Ok(AvroValue::Enum(index as u32, schema.symbols[index].clone()))
}

fn decimal_f64(units: i128, scale: u32) -> f64 {
    units as f64 / 10f64.powi(scale as i32)
}

fn kind_name(schema: &Schema) -> String {
    match schema {
        Schema::Record(record) => format!("record `{}`", record.name.fullname(None)),
        Schema::Enum(e) => format!("enum `{}`", e.name.fullname(None)),
        Schema::Fixed(f) => format!("fixed `{}`", f.name.fullname(None)),
        Schema::Decimal(d) => format!("decimal({}, {})", d.precision, d.scale),
        Schema::Ref { name } => format!("`{}`", name.fullname(None)),
        other => format!("{:?}", other).split('(').next().unwrap_or("?").to_ascii_lowercase(),
    }
}

fn describe(value: &Value) -> String {
    match value {
        Value::Null => "null".to_string(),
        Value::Bool(_) => "a boolean".to_string(),
        Value::I64(n) => format!("the integer {}", n),
        Value::I128(n) => format!("the wide integer {}", n),
        Value::F64(n) => format!("the float {}", n),
        Value::String(text) => format!("the string \"{}\"", text),
        Value::Timestamp { .. } => "a timestamp".to_string(),
        Value::Decimal { units, scale } => format!("the decimal {}", crate::generators::format_decimal_units(*units, *scale)),
        Value::List(_) => "an array".to_string(),
        Value::Struct(_) => "a struct".to_string(),
        Value::Map(_) => "a map".to_string(),
    }
}

fn join_path(path: &str, name: &str) -> String {
    if path.is_empty() {
        name.to_string()
    } else {
        format!("{}.{}", path, name)
    }
}

fn show_path(path: &str) -> &str {
    if path.is_empty() {
        "<root>"
    } else {
        path
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::compile_toml;

    const SCHEMA: &str = r#"{
      "type": "record", "name": "Event", "namespace": "demo",
      "fields": [
        {"name": "id", "type": "string"},
        {"name": "customer", "type": {"type": "record", "name": "Customer", "fields": [
          {"name": "name", "type": "string"},
          {"name": "email", "type": ["null", "string"], "default": null}
        ]}},
        {"name": "age", "type": "int"},
        {"name": "amount", "type": {"type": "bytes", "logicalType": "decimal", "precision": 10, "scale": 4}},
        {"name": "created_at", "type": {"type": "long", "logicalType": "timestamp-micros"}},
        {"name": "status", "type": {"type": "enum", "name": "Status", "symbols": ["active", "blocked"]}},
        {"name": "tags", "type": {"type": "array", "items": "string"}},
        {"name": "attrs", "type": {"type": "map", "values": "long"}},
        {"name": "note", "type": ["null", "string"], "default": null},
        {"name": "missing_with_default", "type": "int", "default": 7}
      ]
    }"#;

    const SPEC: &str = r#"
version = 1
[fields.id]
type = "uuid"
[fields."customer.name"]
type = "name"
[fields."customer.email"]
type = "email"
null_rate = 0.5
[fields.age]
type = "int_range"
min = 1
max = 99
[fields.amount]
type = "decimal_range"
min = "1.00"
max = "99.99"
scale = 2
[fields.created_at]
type = "datetime_range"
start = "2024-01-01T00:00:00Z"
end = "2024-12-31T00:00:00Z"
[fields.status]
type = "choice"
values = ["active", "blocked"]
[fields.tags]
type = "array"
min_len = 1
max_len = 3
element = { type = "choice", values = ["a", "b", "c"] }
[fields.attrs]
type = "map"
min_len = 1
max_len = 2
key = { type = "choice", values = ["x", "y"] }
value = { type = "int_range", min = 1, max = 5 }
[fields.hidden_helper]
hidden = true
type = "constant"
value = "unused"
"#;

    fn one_row(spec: &CompiledSpec) -> RowContext {
        use rand::SeedableRng;
        let mut rng = rand::rngs::StdRng::seed_from_u64(5);
        let mut fields = (*spec.fields).clone();
        let mut ctx = RowContext::new();
        for &index in spec.generation_order.iter() {
            let field = &mut fields[index];
            ctx.insert(field.name.clone(), field.generator.generate(&ctx, &mut rng).unwrap());
        }
        ctx
    }

    #[test]
    fn encodes_and_decodes_a_nested_row() {
        let spec = compile_toml(SPEC).unwrap();
        let encoder = AvroEncoder::new(SCHEMA, &spec).unwrap();
        let ctx = one_row(&spec);
        let datum = encoder.encode(&ctx).unwrap();
        let schema = Schema::parse_str(SCHEMA).unwrap();
        #[allow(deprecated)]
        let decoded = apache_avro::from_avro_datum(&schema, &mut datum.as_slice(), None).unwrap();
        let AvroValue::Record(fields) = decoded else { panic!("expected a record") };
        let get = |name: &str| fields.iter().find(|(n, _)| n == name).map(|(_, v)| v.clone()).unwrap();
        assert!(matches!(get("id"), AvroValue::String(_)));
        let AvroValue::Record(customer) = get("customer") else { panic!("customer is a record") };
        assert_eq!(customer[0].0, "name");
        assert!(matches!(get("age"), AvroValue::Int(_)));
        assert!(matches!(get("amount"), AvroValue::Decimal(_)));
        assert!(matches!(get("created_at"), AvroValue::TimestampMicros(_)));
        assert!(matches!(get("status"), AvroValue::Enum(_, _)));
        assert!(matches!(get("tags"), AvroValue::Array(_)));
        assert!(matches!(get("attrs"), AvroValue::Map(_)));
        assert!(matches!(get("note"), AvroValue::Union(0, _)), "absent nullable field is null");
        assert!(matches!(get("missing_with_default"), AvroValue::Int(7)), "absent field takes its default");
        assert!(encoder.schema_text().contains("\"name\":\"demo.Event\""));
    }

    #[test]
    fn decimal_rescales_to_the_schema_scale() {
        let spec = compile_toml(SPEC).unwrap();
        let encoder = AvroEncoder::new(SCHEMA, &spec).unwrap();
        let schema = Schema::parse_str(r#"{"type": "bytes", "logicalType": "decimal", "precision": 10, "scale": 4}"#).unwrap();
        let converted = encoder.convert(&Value::Decimal { units: 4512, scale: 2 }, &schema).unwrap();
        let AvroValue::Decimal(decimal) = converted else { panic!("expected decimal") };
        let bytes: Vec<u8> = decimal.try_into().unwrap();
        assert_eq!(bytes, i128_to_signed_be_bytes(451200));
    }

    #[test]
    fn a_wrong_type_fails_at_startup() {
        let spec = compile_toml(SPEC.replace("type = \"int_range\"\nmin = 1\nmax = 99", "type = \"name\"").as_str()).unwrap();
        let error = AvroEncoder::new(SCHEMA, &spec).err().expect("name into int");
        let text = format!("{:#}", error);
        assert!(text.contains("age") && text.contains("does not accept"), "{}", text);
    }

    #[test]
    fn null_into_a_non_nullable_field_fails_at_startup() {
        let spec = compile_toml(SPEC.replace("type = \"uuid\"", "type = \"uuid\"\nnull_rate = 0.1").as_str()).unwrap();
        let error = AvroEncoder::new(SCHEMA, &spec).err().expect("nullable id");
        assert!(format!("{:#}", error).contains("not nullable"), "{:#}", error);
    }

    #[test]
    fn an_unknown_spec_field_is_reported() {
        let spec = compile_toml(&format!("{}\n[fields.extra]\ntype = \"uuid\"\n", SPEC)).unwrap();
        let error = AvroEncoder::new(SCHEMA, &spec).err().expect("extra field");
        assert!(format!("{:#}", error).contains("`extra` is not in the Avro schema"), "{:#}", error);
    }
}
