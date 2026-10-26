//! A generated value, and how it renders as text.

use std::sync::Arc;

use serde::Deserialize;

use crate::generators::{format_decimal_units, format_timestamp_micros};

#[derive(Debug, Clone, Deserialize)]
#[serde(untagged)]
pub enum Value {
    Null,
    Bool(bool),
    I64(i64),
    F64(f64),
    String(String),
    /// Microseconds since the Unix epoch, carrying the format the text paths
    /// render it with. Keeping the number means the Parquet writer takes it
    /// as-is; formatting a timestamp only to parse it straight back was the
    /// single largest item in the generator profile.
    ///
    /// `skip` because `Value` is an untagged `Deserialize` used for literals
    /// in a spec file: these two are produced by generators, never parsed.
    #[serde(skip)]
    Timestamp { micros: i64, format: Arc<str> },
    /// Fixed point: `units` scaled by ten to the minus `scale`.
    #[serde(skip)]
    Decimal { units: i128, scale: u32 },
    /// An integer beyond the 64-bit range, for LARGEINT columns.
    #[serde(skip)]
    I128(i128),
    /// An ARRAY value. A YAML sequence literal in a spec becomes one.
    List(Vec<Value>),
    /// A STRUCT value or JSON object: named fields, in order. A YAML mapping
    /// literal in a spec becomes one.
    Struct(#[serde(deserialize_with = "crate::value_json::deserialize_fields")] Vec<(String, Value)>),
    /// A MAP value. Keys may be any scalar, so they are values too.
    #[serde(skip)]
    Map(Vec<(Value, Value)>),
}

impl Value {
    pub fn csv_string(&self, null: &str) -> String {
        match self {
            Value::Null => null.to_string(),
            Value::Bool(value) => value.to_string(),
            Value::I64(value) => value.to_string(),
            Value::F64(value) => value.to_string(),
            Value::String(value) => value.clone(),
            Value::Timestamp { micros, format } => format_timestamp_micros(*micros, format),
            Value::Decimal { units, scale } => format_decimal_units(*units, *scale),
            Value::I128(value) => value.to_string(),
            // Nested values render as JSON, which is also what Doris accepts
            // for ARRAY, MAP and STRUCT in text formats.
            Value::List(_) | Value::Map(_) | Value::Struct(_) => self.to_json_string(),
        }
    }

    pub fn template_value(&self) -> serde_json::Value {
        match self {
            Value::Null => serde_json::Value::Null,
            Value::Bool(value) => serde_json::Value::Bool(*value),
            Value::I64(value) => serde_json::Value::Number((*value).into()),
            Value::F64(value) => serde_json::Number::from_f64(*value)
                .map(serde_json::Value::Number)
                .unwrap_or(serde_json::Value::Null),
            Value::String(value) => serde_json::Value::String(value.clone()),
            Value::Timestamp { micros, format } => {
                serde_json::Value::String(format_timestamp_micros(*micros, format))
            }
            Value::Decimal { units, scale } => {
                serde_json::Value::String(format_decimal_units(*units, *scale))
            }
            Value::I128(value) => serde_json::Number::from_i128(*value)
                .map(serde_json::Value::Number)
                .unwrap_or_else(|| serde_json::Value::String(value.to_string())),
            Value::List(items) => {
                serde_json::Value::Array(items.iter().map(Value::template_value).collect())
            }
            Value::Struct(fields) => serde_json::Value::Object(
                fields
                    .iter()
                    .map(|(name, value)| (name.clone(), value.template_value()))
                    .collect(),
            ),
            Value::Map(entries) => serde_json::Value::Object(
                entries
                    .iter()
                    .map(|(key, value)| (key.csv_string(""), value.template_value()))
                    .collect(),
            ),
        }
    }
}

