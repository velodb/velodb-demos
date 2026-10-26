//! JSON for generated values.
//!
//! Nested values need a text form in three places: CSV output, JSON and
//! VARIANT columns, and JavaScript. `serde_json::Value` would do, except its
//! objects sort their keys, and a STRUCT's field order is part of its type.
//! So values render themselves, in order.

use std::fmt::Write as _;

use serde::de::{Deserializer, MapAccess, Visitor};

use crate::generators::{format_decimal_units, format_timestamp_micros};
use crate::value::Value;

impl Value {
    /// Render as JSON, keeping STRUCT and object fields in their order.
    pub fn to_json_string(&self) -> String {
        let mut out = String::new();
        self.write_json(&mut out);
        out
    }

    pub fn write_json(&self, out: &mut String) {
        self.write_json_opts(out, false)
    }

    /// JSON for Arrow's reader: timestamps as `YYYY-MM-DDTHH:MM:SS.ffffff`
    /// whatever the spec's display format, so any timestamp unit and
    /// precision decodes exactly.
    pub fn write_json_arrow(&self, out: &mut String) {
        self.write_json_opts(out, true)
    }

    fn write_json_opts(&self, out: &mut String, arrow: bool) {
        match self {
            Value::Null => out.push_str("null"),
            Value::Bool(value) => out.push_str(if *value { "true" } else { "false" }),
            Value::I64(value) => {
                let _ = write!(out, "{}", value);
            }
            Value::I128(value) => {
                let _ = write!(out, "{}", value);
            }
            // JSON has no NaN or infinity; null is the conventional stand-in.
            Value::F64(value) if !value.is_finite() => out.push_str("null"),
            Value::F64(value) => {
                let _ = write!(out, "{}", value);
            }
            // Decimals stay exact: written as the number's own digits.
            Value::Decimal { units, scale } => out.push_str(&format_decimal_units(*units, *scale)),
            Value::String(text) => write_json_string(text, out),
            Value::Timestamp { micros, format } if arrow => {
                write_json_string(&format_timestamp_micros(*micros, "%Y-%m-%dT%H:%M:%S%.6f"), out);
                let _ = format;
            }
            Value::Timestamp { micros, format } => {
                write_json_string(&format_timestamp_micros(*micros, format), out)
            }
            Value::List(items) => {
                out.push('[');
                for (index, item) in items.iter().enumerate() {
                    if index > 0 {
                        out.push(',');
                    }
                    item.write_json_opts(out, arrow);
                }
                out.push(']');
            }
            Value::Struct(fields) => {
                out.push('{');
                for (index, (name, value)) in fields.iter().enumerate() {
                    if index > 0 {
                        out.push(',');
                    }
                    write_json_string(name, out);
                    out.push(':');
                    value.write_json_opts(out, arrow);
                }
                out.push('}');
            }
            // JSON object keys are strings, so non-string keys are rendered.
            Value::Map(entries) => {
                out.push('{');
                for (index, (key, value)) in entries.iter().enumerate() {
                    if index > 0 {
                        out.push(',');
                    }
                    write_json_string(&key.csv_string(""), out);
                    out.push(':');
                    value.write_json_opts(out, arrow);
                }
                out.push('}');
            }
        }
    }

    /// Turn parsed JSON into a value. Objects become STRUCT-shaped values;
    /// the Parquet writer reshapes them into MAPs where the column wants one.
    pub fn from_json(json: serde_json::Value) -> Value {
        match json {
            serde_json::Value::Null => Value::Null,
            serde_json::Value::Bool(flag) => Value::Bool(flag),
            serde_json::Value::Number(number) => {
                if let Some(value) = number.as_i64() {
                    Value::I64(value)
                } else if let Some(value) = number.as_u64() {
                    Value::I128(value as i128)
                } else {
                    Value::F64(number.as_f64().unwrap_or(f64::NAN))
                }
            }
            serde_json::Value::String(text) => Value::String(text),
            serde_json::Value::Array(items) => {
                Value::List(items.into_iter().map(Value::from_json).collect())
            }
            serde_json::Value::Object(fields) => Value::Struct(
                fields
                    .into_iter()
                    .map(|(name, value)| (name, Value::from_json(value)))
                    .collect(),
            ),
        }
    }
}

fn write_json_string(text: &str, out: &mut String) {
    out.push('"');
    for ch in text.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            ch if (ch as u32) < 0x20 => {
                let _ = write!(out, "\\u{:04x}", ch as u32);
            }
            ch => out.push(ch),
        }
    }
    out.push('"');
}

/// Deserialize a YAML mapping into ordered fields. A `HashMap` would lose the
/// order, and for a STRUCT literal the order is part of what was written.
pub(crate) fn deserialize_fields<'de, D>(deserializer: D) -> Result<Vec<(String, Value)>, D::Error>
where
    D: Deserializer<'de>,
{
    struct FieldsVisitor;

    impl<'de> Visitor<'de> for FieldsVisitor {
        type Value = Vec<(String, Value)>;

        fn expecting(&self, formatter: &mut std::fmt::Formatter) -> std::fmt::Result {
            formatter.write_str("a mapping of field names to values")
        }

        fn visit_map<A>(self, mut map: A) -> Result<Self::Value, A::Error>
        where
            A: MapAccess<'de>,
        {
            let mut fields = Vec::new();
            while let Some((name, value)) = map.next_entry::<String, Value>()? {
                fields.push((name, value));
            }
            Ok(fields)
        }
    }

    deserializer.deserialize_map(FieldsVisitor)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn renders_nested_values_in_order() {
        let value = Value::Struct(vec![
            ("zeta".into(), Value::I64(1)),
            ("alpha".into(), Value::List(vec![Value::String("a\"b".into()), Value::Null])),
            (
                "map".into(),
                Value::Map(vec![(Value::I64(7), Value::Decimal { units: 1234, scale: 2 })]),
            ),
            ("big".into(), Value::I128(170141183460469231731687303715884105727)),
        ]);
        assert_eq!(
            value.to_json_string(),
            r#"{"zeta":1,"alpha":["a\"b",null],"map":{"7":12.34},"big":170141183460469231731687303715884105727}"#
        );
    }

    #[test]
    fn rendered_json_is_valid_and_round_trips() {
        let value = Value::List(vec![
            Value::F64(1.5),
            Value::F64(f64::NAN),
            Value::String("tab\there\u{1}".into()),
        ]);
        let text = value.to_json_string();
        let parsed: serde_json::Value = serde_json::from_str(&text).expect("valid JSON");
        assert_eq!(parsed[0], 1.5);
        assert!(parsed[1].is_null());
        assert_eq!(parsed[2], "tab\there\u{1}");
    }

    #[test]
    fn yaml_literals_become_lists_and_structs() {
        let list: Value = serde_yaml::from_str("[1, two, 3.5]").unwrap();
        assert!(matches!(&list, Value::List(items) if items.len() == 3));

        let object: Value = serde_yaml::from_str("{b: 1, a: [x, y]}").unwrap();
        let Value::Struct(fields) = object else { panic!("expected struct") };
        let names: Vec<&str> = fields.iter().map(|(name, _)| name.as_str()).collect();
        assert_eq!(names, ["b", "a"], "field order must survive");

        // Scalars are unaffected by the new variants.
        assert!(matches!(serde_yaml::from_str::<Value>("42").unwrap(), Value::I64(42)));
        assert!(matches!(serde_yaml::from_str::<Value>("hi").unwrap(), Value::String(_)));
    }
}
