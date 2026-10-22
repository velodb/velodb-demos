//! What each generator can produce at its extremes, for validation.

use base64::{engine::general_purpose, Engine as _};
use chrono::Utc;

use crate::generators::*;
use crate::spec::{AddressPart, NamePart, RandomBytesEncoding};
use crate::value::Value;

fn longest<'a>(words: &[&'a str]) -> &'a str {
    words.iter().copied().max_by_key(|word| word.len()).unwrap_or("")
}

fn integer(value: i128) -> Value {
    match i64::try_from(value) {
        Ok(small) => Value::I64(small),
        Err(_) => Value::I128(value),
    }
}

impl Generator {
    /// The extreme values this generator can produce, plus null when it has a
    /// null rate. Empty where the output cannot be predicted.
    pub fn probe_values(&self) -> Vec<Value> {
        let (mut values, null_rate) = match self {
            Generator::Constant(g) => (vec![g.value.clone()], g.null_rate),
            // Only the start is known; the per-row checks cover the rest.
            Generator::Sequence(g) => (vec![Value::I64(g.next)], g.null_rate),
            Generator::SequenceString(g) => (
                vec![Value::String(format!(
                    "{}{:0width$}{}",
                    g.prefix,
                    g.max_counter,
                    g.suffix,
                    width = g.digits
                ))],
                g.null_rate,
            ),
            Generator::Name(g) => {
                let text = match g.part {
                    NamePart::First => longest(FIRST_NAMES).to_string(),
                    NamePart::Last => longest(LAST_NAMES).to_string(),
                    NamePart::Full => format!("{} {}", longest(FIRST_NAMES), longest(LAST_NAMES)),
                };
                (vec![Value::String(text)], g.null_rate)
            }
            Generator::Email(g) => (
                vec![Value::String(format!("{}@{}", "x".repeat(12), longest(EMAIL_DOMAINS)))],
                g.null_rate,
            ),
            Generator::Lorem(g) => (
                vec![Value::String(vec![longest(LOREM_WORDS); g.words_max].join(" "))],
                g.null_rate,
            ),
            Generator::Address(g) => {
                let street = format!("9999 {} {}", longest(STREET_NAMES), longest(STREET_SUFFIXES));
                let text = match g.part {
                    AddressPart::Street => street,
                    AddressPart::City => longest(CITIES).to_string(),
                    AddressPart::State => longest(STATES).to_string(),
                    AddressPart::Country => longest(COUNTRIES).to_string(),
                    AddressPart::PostalCode => "99999".to_string(),
                    AddressPart::Full => format!(
                        "{}, {}, {} 99999, {}",
                        street,
                        longest(CITIES),
                        longest(STATES),
                        longest(COUNTRIES)
                    ),
                };
                (vec![Value::String(text)], g.null_rate)
            }
            Generator::Template(g) => (Vec::new(), g.null_rate),
            Generator::JavaScript(g) => (Vec::new(), g.null_rate),
            Generator::IntRange(g) => (vec![integer(g.min), integer(g.max)], g.null_rate),
            Generator::FloatRange(g) => (vec![Value::F64(g.min), Value::F64(g.max)], g.null_rate),
            Generator::DecimalRange(g) => (
                vec![
                    Value::Decimal { units: g.min_units, scale: g.scale },
                    Value::Decimal { units: g.max_units, scale: g.scale },
                ],
                g.null_rate,
            ),
            Generator::Fluctuating(g) => (
                [g.min, g.max].into_iter().filter_map(|bound| g.emit(bound).ok()).collect(),
                g.null_rate,
            ),
            Generator::DateTimeAround(g) => {
                let base = g.base.unwrap_or_else(Utc::now).timestamp_micros();
                let at = |offset: i64| Value::Timestamp {
                    micros: base.saturating_add(offset),
                    format: g.format.clone(),
                };
                (vec![at(g.offset_micros_min), at(g.offset_micros_max)], g.null_rate)
            }
            Generator::DateTimeRange(g) => {
                let at = |seconds: i64| Value::Timestamp {
                    micros: seconds.saturating_mul(1_000_000),
                    format: g.format.clone(),
                };
                (vec![at(g.start_seconds), at(g.end_seconds)], g.null_rate)
            }
            Generator::Choice(g) => (g.values.clone(), g.null_rate),
            Generator::WeightedChoice(g) => {
                (g.values.iter().map(|weighted| weighted.value.clone()).collect(), g.null_rate)
            }
            Generator::Uuid(g) => (vec![Value::String(uuid::Uuid::nil().to_string())], g.null_rate),
            Generator::RandomBytes(g) => {
                let widest = vec![0u8; g.max_bytes];
                let text = match g.encoding {
                    RandomBytesEncoding::Hex => hex_encode(&widest),
                    RandomBytesEncoding::Base64 => general_purpose::STANDARD.encode(&widest),
                    RandomBytesEncoding::Base64url => general_purpose::URL_SAFE_NO_PAD.encode(&widest),
                };
                (vec![Value::String(text)], g.null_rate)
            }
            Generator::Array(g) => {
                let items = if g.max_len == 0 { Vec::new() } else { g.element.probe_values() };
                (vec![Value::List(items)], g.null_rate)
            }
            Generator::Map(g) => {
                // Null keys are skipped at generation time, so skip them here.
                let mut keys: Vec<Value> = Vec::new();
                for key in g.key.probe_values() {
                    let text = key.csv_string("");
                    if !matches!(key, Value::Null) && !keys.iter().any(|k| k.csv_string("") == text) {
                        keys.push(key);
                    }
                }
                let values = g.value.probe_values();
                let entries = if g.max_len == 0 {
                    Vec::new()
                } else {
                    keys.into_iter()
                        .enumerate()
                        .map(|(index, key)| {
                            let value = values.get(index % values.len().max(1)).cloned().unwrap_or(Value::Null);
                            (key, value)
                        })
                        .collect()
                };
                (vec![Value::Map(entries)], g.null_rate)
            }
            Generator::Struct(g) => {
                let per_field: Vec<Vec<Value>> =
                    g.fields.iter().map(|(_, generator)| generator.probe_values()).collect();
                let rounds = per_field.iter().map(Vec::len).max().unwrap_or(0).max(1);
                let values = (0..rounds)
                    .map(|round| {
                        Value::Struct(
                            g.fields
                                .iter()
                                .zip(&per_field)
                                .map(|((name, _), probes)| {
                                    let value = if probes.is_empty() {
                                        Value::Null
                                    } else {
                                        probes[round % probes.len()].clone()
                                    };
                                    (name.clone(), value)
                                })
                                .collect(),
                        )
                    })
                    .collect();
                (values, g.null_rate)
            }
            Generator::Ipv4(g) => {
                let top = if g.host_bits == 0 { 0 } else { u32::MAX >> (32 - g.host_bits) };
                (
                    vec![
                        Value::String(std::net::Ipv4Addr::from(g.network).to_string()),
                        Value::String(std::net::Ipv4Addr::from(g.network | top).to_string()),
                    ],
                    g.null_rate,
                )
            }
            Generator::Ipv6(g) => {
                let top = if g.host_bits == 0 { 0 } else { u128::MAX >> (128 - g.host_bits) };
                (
                    vec![
                        Value::String(std::net::Ipv6Addr::from(g.network).to_string()),
                        Value::String(std::net::Ipv6Addr::from(g.network | top).to_string()),
                    ],
                    g.null_rate,
                )
            }
        };
        if null_rate.is_some_and(|rate| rate > 0.0) {
            values.push(Value::Null);
        }
        values
    }
}
