//! Protobuf encoding against a user-supplied `.proto`.
//!
//! The file is compiled in-process (no `protoc`), the message is filled
//! through reflection, and the `.proto` text itself is what a stream prelude
//! or a schema registry receives.

use std::collections::HashMap;
use std::path::Path;

use anyhow::{anyhow, bail, Context, Result};
use prost::Message as _;
use prost_reflect::{
    DescriptorPool, DynamicMessage, FieldDescriptor, Kind, MapKey, MessageDescriptor,
    Value as ProtoValue,
};

use crate::compile::{CompiledSpec, RowContext};
use crate::generators::format_timestamp_micros;
use crate::schema::tree::{Node, RowTree};
use crate::value::Value;

pub struct ProtoEncoder {
    message: MessageDescriptor,
    tree: RowTree,
    schema_text: String,
    message_indexes: Vec<i32>,
}

impl ProtoEncoder {
    /// Compile the file, pick the message, and check the spec against it.
    pub fn new(proto_path: &Path, message_name: Option<&str>, spec: &CompiledSpec) -> Result<Self> {
        let schema_text = std::fs::read_to_string(proto_path)
            .with_context(|| format!("failed to read {}", proto_path.display()))?;
        let file_name = proto_path
            .file_name()
            .ok_or_else(|| anyhow!("{} has no file name", proto_path.display()))?
            .to_string_lossy()
            .to_string();
        let include = proto_path.parent().filter(|p| !p.as_os_str().is_empty()).unwrap_or(Path::new("."));
        let set = protox::compile([&file_name], [include])
            .map_err(|error| anyhow!("failed to compile {}: {}", proto_path.display(), error))?;
        let pool = DescriptorPool::from_file_descriptor_set(set)
            .context("failed to load the compiled Protobuf descriptors")?;
        let file = pool
            .files()
            .find(|file| file.name() == file_name)
            .ok_or_else(|| anyhow!("compiled descriptors do not contain {}", file_name))?;
        let top_level: Vec<MessageDescriptor> = file.messages().collect();
        let message = match message_name {
            Some(name) => pool
                .all_messages()
                .find(|m| m.full_name() == name || m.name() == name)
                .ok_or_else(|| {
                    anyhow!(
                        "message `{}` is not defined in {} (it has: {})",
                        name,
                        file_name,
                        top_level.iter().map(|m| m.full_name()).collect::<Vec<_>>().join(", ")
                    )
                })?,
            None => match top_level.as_slice() {
                [only] => only.clone(),
                [] => bail!("{} defines no message", file_name),
                many => bail!(
                    "{} defines {} messages; set generation.proto_message to one of: {}",
                    file_name,
                    many.len(),
                    many.iter().map(|m| m.full_name()).collect::<Vec<_>>().join(", ")
                ),
            },
        };
        let message_indexes = message_indexes(&message);
        let tree = RowTree::from_spec(spec)?;
        let encoder = Self { message, tree, schema_text, message_indexes };
        let probes: HashMap<&str, Vec<Value>> = spec
            .fields
            .iter()
            .map(|field| (field.name.as_str(), field.generator.probe_values()))
            .collect();
        encoder.validate(&encoder.tree.root, &encoder.message, "", &probes)?;
        Ok(encoder)
    }

    /// The `.proto` source as given.
    pub fn schema_text(&self) -> &str {
        &self.schema_text
    }

    /// The message's position in the file, as the Confluent wire format
    /// carries it: `[0]` for the first top-level message, `[1, 0]` for the
    /// first message nested in the second.
    pub fn message_indexes(&self) -> &[i32] {
        &self.message_indexes
    }

    pub fn message_name(&self) -> &str {
        self.message.full_name()
    }

    /// One row as protobuf binary encoding of the message.
    pub fn encode(&self, ctx: &RowContext) -> Result<Vec<u8>> {
        let message = self.encode_message(&self.tree.root, ctx, &self.message)?;
        Ok(message.encode_to_vec())
    }

    fn validate(
        &self,
        nodes: &[(String, Node)],
        message: &MessageDescriptor,
        path: &str,
        probes: &HashMap<&str, Vec<Value>>,
    ) -> Result<()> {
        for (name, node) in nodes {
            let child_path = join_path(path, name);
            let field = message.get_field_by_name(name).ok_or_else(|| {
                anyhow!(
                    "spec field `{}` is not in message `{}` (it has: {})",
                    child_path,
                    message.full_name(),
                    message.fields().map(|f| f.name().to_string()).collect::<Vec<_>>().join(", ")
                )
            })?;
            match node {
                Node::Record(children) => {
                    let Kind::Message(nested) = field.kind() else {
                        bail!("spec field `{}` is a record but `{}` is not a message field", child_path, field.full_name());
                    };
                    if field.is_list() || field.is_map() {
                        bail!("spec field `{}` is a record but `{}` is repeated or a map", child_path, field.full_name());
                    }
                    self.validate(children, &nested, &child_path, probes)?;
                }
                Node::Leaf(row_name) => {
                    for probe in probes.get(row_name.as_str()).map(Vec::as_slice).unwrap_or(&[]) {
                        convert_field(probe, &field).with_context(|| {
                            format!(
                                "field `{}` can produce {} which the Protobuf schema does not accept",
                                child_path,
                                describe(probe)
                            )
                        })?;
                    }
                }
            }
        }
        Ok(())
    }

    fn encode_message(&self, nodes: &[(String, Node)], ctx: &RowContext, desc: &MessageDescriptor) -> Result<DynamicMessage> {
        let mut message = DynamicMessage::new(desc.clone());
        for (name, node) in nodes {
            let field = desc
                .get_field_by_name(name)
                .ok_or_else(|| anyhow!("message `{}` has no field `{}`", desc.full_name(), name))?;
            let value = match node {
                Node::Leaf(row_name) => {
                    let value = ctx
                        .get(row_name)
                        .ok_or_else(|| anyhow!("no generated value for field `{}`", row_name))?;
                    convert_field(value, &field).with_context(|| format!("field `{}`", row_name))?
                }
                Node::Record(children) => {
                    let Kind::Message(nested) = field.kind() else {
                        bail!("`{}` is not a message field", field.full_name());
                    };
                    Some(ProtoValue::Message(self.encode_message(children, ctx, &nested)?))
                }
            };
            if let Some(value) = value {
                message
                    .try_set_field(&field, value)
                    .map_err(|error| anyhow!("field `{}`: {}", field.full_name(), error))?;
            }
        }
        Ok(message)
    }
}

fn message_indexes(message: &MessageDescriptor) -> Vec<i32> {
    let mut chain = Vec::new();
    let mut current = message.clone();
    loop {
        match current.parent_message() {
            Some(parent) => {
                let index = parent
                    .child_messages()
                    .position(|child| child.full_name() == current.full_name())
                    .unwrap_or(0);
                chain.push(index as i32);
                current = parent;
            }
            None => {
                let index = current
                    .parent_file()
                    .messages()
                    .position(|top| top.full_name() == current.full_name())
                    .unwrap_or(0);
                chain.push(index as i32);
                break;
            }
        }
    }
    chain.reverse();
    chain
}

/// A generated value as the field wants it; `None` leaves the field unset.
pub fn convert_field(value: &Value, field: &FieldDescriptor) -> Result<Option<ProtoValue>> {
    if matches!(value, Value::Null) {
        return Ok(None);
    }
    if field.is_map() {
        let Kind::Message(entry) = field.kind() else { bail!("map field without an entry message") };
        let key_field = entry.map_entry_key_field();
        let value_field = entry.map_entry_value_field();
        let pairs: Vec<(String, &Value)> = match value {
            Value::Map(entries) => entries.iter().map(|(k, v)| (k.csv_string(""), v)).collect(),
            Value::Struct(fields) => fields.iter().map(|(k, v)| (k.clone(), v)).collect(),
            other => bail!("{} does not fit a map field", describe(other)),
        };
        let mut map = HashMap::with_capacity(pairs.len());
        for (key, item) in pairs {
            let key = map_key(&key, &key_field.kind())?;
            if let Some(item) = convert_kind(item, &value_field.kind())? {
                map.insert(key, item);
            }
        }
        return Ok(Some(ProtoValue::Map(map)));
    }
    if field.is_list() {
        let items = match value {
            Value::List(items) => items,
            other => bail!("{} does not fit a repeated field", describe(other)),
        };
        let mut out = Vec::with_capacity(items.len());
        for item in items {
            if let Some(item) = convert_kind(item, &field.kind())? {
                out.push(item);
            }
        }
        return Ok(Some(ProtoValue::List(out)));
    }
    convert_kind(value, &field.kind())
}

fn map_key(key: &str, kind: &Kind) -> Result<MapKey> {
    let parse = |what: &str| anyhow!("map key `{}` is not {}", key, what);
    Ok(match kind {
        Kind::String => MapKey::String(key.to_string()),
        Kind::Bool => MapKey::Bool(key.parse().map_err(|_| parse("a boolean"))?),
        Kind::Int32 | Kind::Sint32 | Kind::Sfixed32 => MapKey::I32(key.parse().map_err(|_| parse("a 32-bit integer"))?),
        Kind::Int64 | Kind::Sint64 | Kind::Sfixed64 => MapKey::I64(key.parse().map_err(|_| parse("a 64-bit integer"))?),
        Kind::Uint32 | Kind::Fixed32 => MapKey::U32(key.parse().map_err(|_| parse("an unsigned 32-bit integer"))?),
        Kind::Uint64 | Kind::Fixed64 => MapKey::U64(key.parse().map_err(|_| parse("an unsigned 64-bit integer"))?),
        other => bail!("{:?} is not a valid map key type", other),
    })
}

fn convert_kind(value: &Value, kind: &Kind) -> Result<Option<ProtoValue>> {
    let mismatch = || anyhow!("{} does not fit Protobuf type {}", describe(value), kind_name(kind));
    let as_i128 = || -> Option<i128> {
        match value {
            Value::I64(n) => Some(*n as i128),
            Value::I128(n) => Some(*n),
            Value::Decimal { units, scale: 0 } => Some(*units),
            Value::Timestamp { micros, .. } => Some(*micros as i128),
            _ => None,
        }
    };
    let as_f64 = || -> Option<f64> {
        match value {
            Value::F64(n) => Some(*n),
            Value::I64(n) => Some(*n as f64),
            Value::I128(n) => Some(*n as f64),
            Value::Decimal { units, scale } => Some(*units as f64 / 10f64.powi(*scale as i32)),
            _ => None,
        }
    };
    Ok(Some(match kind {
        Kind::Bool => match value {
            Value::Bool(flag) => ProtoValue::Bool(*flag),
            _ => return Err(mismatch()),
        },
        Kind::Int32 | Kind::Sint32 | Kind::Sfixed32 => {
            ProtoValue::I32(as_i128().and_then(|n| i32::try_from(n).ok()).ok_or_else(mismatch)?)
        }
        Kind::Int64 | Kind::Sint64 | Kind::Sfixed64 => {
            ProtoValue::I64(as_i128().and_then(|n| i64::try_from(n).ok()).ok_or_else(mismatch)?)
        }
        Kind::Uint32 | Kind::Fixed32 => {
            ProtoValue::U32(as_i128().and_then(|n| u32::try_from(n).ok()).ok_or_else(mismatch)?)
        }
        Kind::Uint64 | Kind::Fixed64 => {
            ProtoValue::U64(as_i128().and_then(|n| u64::try_from(n).ok()).ok_or_else(mismatch)?)
        }
        Kind::Float => ProtoValue::F32(as_f64().ok_or_else(mismatch)? as f32),
        Kind::Double => ProtoValue::F64(as_f64().ok_or_else(mismatch)?),
        Kind::String => ProtoValue::String(match value {
            Value::String(text) => text.clone(),
            Value::Timestamp { micros, format } => format_timestamp_micros(*micros, format),
            Value::List(_) | Value::Struct(_) | Value::Map(_) => value.to_json_string(),
            other => other.csv_string(""),
        }),
        Kind::Bytes => match value {
            Value::String(text) => ProtoValue::Bytes(bytes::Bytes::copy_from_slice(text.as_bytes())),
            _ => return Err(mismatch()),
        },
        Kind::Enum(desc) => match value {
            Value::String(name) => {
                let found = desc
                    .get_value_by_name(name)
                    .or_else(|| desc.values().find(|v| v.name().eq_ignore_ascii_case(name)))
                    .ok_or_else(|| {
                        anyhow!(
                            "`{}` is not a value of enum `{}` ({})",
                            name,
                            desc.full_name(),
                            desc.values().map(|v| v.name().to_string()).collect::<Vec<_>>().join(", ")
                        )
                    })?;
                ProtoValue::EnumNumber(found.number())
            }
            Value::I64(number) => {
                let number = i32::try_from(*number).map_err(|_| mismatch())?;
                if desc.get_value(number).is_none() {
                    bail!("{} is not a value of enum `{}`", number, desc.full_name());
                }
                ProtoValue::EnumNumber(number)
            }
            _ => return Err(mismatch()),
        },
        Kind::Message(desc) => {
            if desc.full_name() == "google.protobuf.Timestamp" {
                let micros = match value {
                    Value::Timestamp { micros, .. } | Value::I64(micros) => *micros,
                    _ => return Err(mismatch()),
                };
                let mut message = DynamicMessage::new(desc.clone());
                message.set_field_by_name("seconds", ProtoValue::I64(micros.div_euclid(1_000_000)));
                message.set_field_by_name("nanos", ProtoValue::I32((micros.rem_euclid(1_000_000) * 1000) as i32));
                return Ok(Some(ProtoValue::Message(message)));
            }
            let Value::Struct(fields) = value else { return Err(mismatch()) };
            let mut message = DynamicMessage::new(desc.clone());
            for (name, item) in fields {
                let field = desc
                    .get_field_by_name(name)
                    .ok_or_else(|| anyhow!("message `{}` has no field `{}`", desc.full_name(), name))?;
                if let Some(item) = convert_field(item, &field).with_context(|| format!("struct field `{}`", name))? {
                    message
                        .try_set_field(&field, item)
                        .map_err(|error| anyhow!("field `{}`: {}", field.full_name(), error))?;
                }
            }
            ProtoValue::Message(message)
        }
    }))
}

fn kind_name(kind: &Kind) -> String {
    match kind {
        Kind::Message(m) => format!("message `{}`", m.full_name()),
        Kind::Enum(e) => format!("enum `{}`", e.full_name()),
        other => format!("{:?}", other).to_ascii_lowercase(),
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::compile_toml;

    const PROTO: &str = r#"syntax = "proto3";
package demo;
import "google/protobuf/timestamp.proto";

message Event {
  string id = 1;
  Customer customer = 2;
  int32 age = 3;
  string amount = 4;
  int64 created_at = 5;
  google.protobuf.Timestamp seen_at = 6;
  Status status = 7;
  repeated string tags = 8;
  map<string, int64> attrs = 9;
  optional string note = 10;
}

message Customer {
  string name = 1;
  string email = 2;
}

enum Status {
  ACTIVE = 0;
  BLOCKED = 1;
}
"#;

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
[fields.seen_at]
type = "datetime_range"
start = "2024-01-01T00:00:00Z"
end = "2024-12-31T00:00:00Z"
[fields.status]
type = "choice"
values = ["active", "BLOCKED"]
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
"#;

    fn write_proto(tag: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("datagen-proto-{}-{}", tag, std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("event.proto");
        std::fs::write(&path, PROTO).unwrap();
        path
    }

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
    fn encodes_and_decodes_a_nested_message() {
        let path = write_proto("roundtrip");
        let spec = compile_toml(SPEC).unwrap();
        let encoder = ProtoEncoder::new(&path, Some("Event"), &spec).unwrap();
        assert_eq!(encoder.message_name(), "demo.Event");
        assert_eq!(encoder.message_indexes(), &[0]);
        let ctx = one_row(&spec);
        let bytes = encoder.encode(&ctx).unwrap();
        let decoded = DynamicMessage::decode(encoder.message.clone(), bytes.as_slice()).unwrap();
        let customer = decoded.get_field_by_name("customer").unwrap();
        let customer = customer.as_message().unwrap();
        assert!(customer.get_field_by_name("name").unwrap().as_str().is_some_and(|s| !s.is_empty()));
        assert!(decoded.get_field_by_name("age").unwrap().as_i32().is_some_and(|a| (1..=99).contains(&a)));
        let amount = decoded.get_field_by_name("amount").unwrap();
        assert!(amount.as_str().unwrap().contains('.'));
        let seen = decoded.get_field_by_name("seen_at").unwrap();
        assert!(seen.as_message().unwrap().get_field_by_name("seconds").unwrap().as_i64().unwrap() > 1_700_000_000);
        assert!(decoded.get_field_by_name("tags").unwrap().as_list().is_some_and(|l| !l.is_empty()));
        assert!(decoded.get_field_by_name("attrs").unwrap().as_map().is_some_and(|m| !m.is_empty()));
        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }

    #[test]
    fn selects_a_message_by_name_and_reports_indexes() {
        let path = write_proto("select");
        let spec = compile_toml("version = 1\n[fields.name]\ntype = \"name\"\n").unwrap();
        let encoder = ProtoEncoder::new(&path, Some("Customer"), &spec).unwrap();
        assert_eq!(encoder.message_indexes(), &[1]);
        let error = ProtoEncoder::new(&path, None, &spec).err().expect("two messages, none chosen");
        assert!(error.to_string().contains("proto_message"), "{}", error);
        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }

    #[test]
    fn a_wrong_type_fails_at_startup() {
        let path = write_proto("wrong");
        let spec = compile_toml(SPEC.replace("type = \"int_range\"\nmin = 1\nmax = 99", "type = \"name\"").as_str()).unwrap();
        let error = ProtoEncoder::new(&path, Some("demo.Event"), &spec).err().expect("name into int32");
        let text = format!("{:#}", error);
        assert!(text.contains("age") && text.contains("does not accept"), "{}", text);
        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }
}
