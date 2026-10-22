//! Derive a datagen field spec from a parsed Doris table.
//!
//! The output is YAML text, matching how the built-in presets work, so the
//! derived spec flows through the same `RawSpec` -> `compile_spec` path and can
//! be printed with `--emit-spec` for hand editing.

use anyhow::{bail, Result};

use crate::schema::{Column, DorisType, Table};

/// Strings shorter than this get fixed-width filler rather than lorem text,
/// so generated values never exceed the declared column width.
const LOREM_MIN_WIDTH: u32 = 64;

pub fn spec_yaml_from_table(table: &Table) -> Result<String> {
    let ungeneratable: Vec<&str> = table
        .columns
        .iter()
        .filter(|column| !column.ty.is_generatable())
        .map(|column| column.name.as_str())
        .collect();
    if !ungeneratable.is_empty() {
        bail!(
            "table `{}` has columns whose types cannot be generated yet: {}. \
             Provide these fields in a --spec file, or drop them from the schema.",
            table.name,
            ungeneratable.join(", ")
        );
    }

    let mut out = String::new();
    out.push_str("version: 1\n\n");
    out.push_str(&format!("# Derived from Doris table `{}`.\n", table.name));
    out.push_str("# Edit freely; every field below is a normal datagen generator.\n\n");
    out.push_str("context:\n  reset: row\n\nbatch:\n  rows: 1000\n\nfields:\n");

    for (index, column) in table.columns.iter().enumerate() {
        if let Some(function) = column.ty.load_function() {
            out.push_str(&format!(
                "  # {} has no Parquet form Doris loads directly: this writes its\n  # source values; load the column with {}(`{}`).\n",
                column.ty.sql_name(),
                function,
                column.name
            ));
        }
        out.push_str(&format!("  - name: {}\n", yaml_scalar(&column.name)));
        out.push_str(&format!("    order: {}\n", index));
        out.push_str("    gen:\n");
        for line in generator_for(column).lines() {
            out.push_str("      ");
            out.push_str(line);
            out.push('\n');
        }
    }
    Ok(out)
}

/// Choose a generator, preferring a column-name heuristic when the declared
/// type has room for it, otherwise falling back to the type default.
fn generator_for(column: &Column) -> String {
    if let Some(by_name) = generator_by_name(column) {
        return by_name;
    }
    generator_by_type(&column.ty)
}

fn generator_by_name(column: &Column) -> Option<String> {
    let name = column.name.to_ascii_lowercase();
    let width = string_width(&column.ty);

    // Text-shaped heuristics need a wide enough string column.
    if let Some(width) = width {
        let roomy = width >= LOREM_MIN_WIDTH;
        if roomy && contains_any(&name, &["email", "mail_addr"]) {
            return Some("type: email".to_string());
        }
        if roomy && contains_any(&name, &["uuid", "guid"]) {
            return Some("type: uuid".to_string());
        }
        if roomy && contains_any(&name, &["first_name"]) {
            return Some("type: name\npart: first".to_string());
        }
        if roomy && contains_any(&name, &["last_name", "surname"]) {
            return Some("type: name\npart: last".to_string());
        }
        if roomy && contains_any(&name, &["full_name", "customer_name", "user_name"]) {
            return Some("type: name\npart: full".to_string());
        }
        if roomy && contains_any(&name, &["address", "street"]) {
            return Some("type: address\npart: full".to_string());
        }
        if roomy && contains_any(&name, &["city"]) {
            return Some("type: address\npart: city".to_string());
        }
        if roomy && contains_any(&name, &["country"]) {
            return Some("type: address\npart: country".to_string());
        }
        if roomy && contains_any(&name, &["status", "state"]) && !name.contains("estate") {
            return Some(
                "type: weighted_choice\nvalues:\n  - value: active\n    weight: 80\n  - value: inactive\n    weight: 15\n  - value: blocked\n    weight: 5"
                    .to_string(),
            );
        }
    }

    // An integer column literally named `id` is the natural surrogate key.
    if is_integer(&column.ty) && (name == "id" || name.ends_with("_id") && name.starts_with("row")) {
        return Some("type: sequence\nstart: 1\nstep: 1".to_string());
    }

    None
}

fn generator_by_type(ty: &DorisType) -> String {
    match ty {
        DorisType::Boolean => {
            "type: weighted_choice\nvalues:\n  - value: \"true\"\n    weight: 50\n  - value: \"false\"\n    weight: 50".to_string()
        }
        DorisType::TinyInt => int_range(0, 127),
        DorisType::SmallInt => int_range(0, 32_767),
        DorisType::Int => int_range(0, 1_000_000),
        DorisType::BigInt => int_range(0, 1_000_000_000),
        // LARGEINT usually holds unsigned 64-bit ids, such as a ClickHouse
        // UInt64 converted to Doris, so span that range: it exercises the
        // values BIGINT cannot hold. Quoted, since it exceeds 64-bit signed.
        DorisType::LargeInt => "type: int_range\nmin: 0\nmax: \"18446744073709551615\"".to_string(),
        DorisType::Float => "type: float_range\nmin: 0.0\nmax: 1000.0\nprecision: 3".to_string(),
        DorisType::Double => {
            "type: float_range\nmin: 0.0\nmax: 1000000.0\nprecision: 6".to_string()
        }
        DorisType::Decimal { precision, scale } => decimal_range(*precision, *scale),
        DorisType::Date => {
            "type: datetime_around\noffset_seconds_min: -31536000\noffset_seconds_max: 0\nformat: \"%Y-%m-%d\""
                .to_string()
        }
        DorisType::DateTime { scale } => {
            let format = if *scale == 0 {
                "%Y-%m-%d %H:%M:%S".to_string()
            } else {
                format!("%Y-%m-%d %H:%M:%S%.{}f", scale)
            };
            format!(
                "type: datetime_around\noffset_seconds_min: -2592000\noffset_seconds_max: 0\nformat: \"{}\"",
                format
            )
        }
        DorisType::Char { len } => fixed_width_string(*len),
        DorisType::Varchar { len } => {
            if *len >= LOREM_MIN_WIDTH {
                lorem_within(*len)
            } else {
                fixed_width_string(*len)
            }
        }
        DorisType::String => "type: lorem\nwords_min: 3\nwords_max: 12".to_string(),
        // A small object; the writer renders it as JSON text.
        DorisType::Json | DorisType::Variant => [
            "type: struct",
            "fields:",
            "  - name: id",
            "    gen:",
            "      type: int_range",
            "      min: 1",
            "      max: 1000000",
            "  - name: tag",
            "    gen:",
            "      type: lorem",
            "      words_min: 1",
            "      words_max: 1",
            "  - name: score",
            "    gen:",
            "      type: float_range",
            "      min: 0.0",
            "      max: 100.0",
            "      precision: 2",
        ]
        .join("\n"),
        DorisType::Ipv4 => "type: ipv4\ncidr: \"10.0.0.0/8\"".to_string(),
        DorisType::Ipv6 => "type: ipv6\ncidr: \"2001:db8::/32\"".to_string(),
        DorisType::Array(element) => format!(
            "type: array\nmin_len: 0\nmax_len: 3\nelement:\n{}",
            indent(&generator_by_type(element), 2)
        ),
        DorisType::Map(key, value) => format!(
            "type: map\nmin_len: 0\nmax_len: 3\nkey:\n{}\nvalue:\n{}",
            indent(&map_key_generator(key), 2),
            indent(&generator_by_type(value), 2)
        ),
        DorisType::Struct(fields) => {
            let mut out = String::from("type: struct\nfields:");
            for (name, field_type) in fields {
                out.push_str(&format!(
                    "\n  - name: {}\n    gen:\n{}",
                    yaml_scalar(name),
                    indent(&generator_by_type(field_type), 6)
                ));
            }
            out
        }
        // Sketch sources: member ids, distinct-count keys, and measurements.
        DorisType::Bitmap => int_range(1, 10_000_000),
        DorisType::Hll => "type: uuid".to_string(),
        DorisType::QuantileState => {
            "type: float_range\nmin: 0.0\nmax: 1000.0\nprecision: 3".to_string()
        }
        // Refused by spec_yaml_from_table before any generator is chosen.
        DorisType::AggState(_) => unreachable!("AGG_STATE is not generatable"),
    }
}

/// Map keys want a small domain of short values, so draws collide and
/// dedupe naturally rather than every map holding unique noise.
fn map_key_generator(key: &DorisType) -> String {
    match string_width(key) {
        Some(width) if width >= longest_lorem_word() => {
            "type: lorem\nwords_min: 1\nwords_max: 1".to_string()
        }
        Some(width) => fixed_width_string(width),
        None => generator_by_type(key),
    }
}

fn longest_lorem_word() -> u32 {
    crate::LOREM_WORDS.iter().map(|word| word.len()).max().unwrap_or(1) as u32
}

/// Lorem text that can never exceed `len` bytes: at most as many words as
/// fit when every word is the longest one, each followed by a space.
fn lorem_within(len: u32) -> String {
    let longest = longest_lorem_word();
    let words_max = ((len + 1) / (longest + 1)).clamp(1, 12);
    let words_min = words_max.min(3);
    format!("type: lorem\nwords_min: {}\nwords_max: {}", words_min, words_max)
}

fn indent(text: &str, spaces: usize) -> String {
    let pad = " ".repeat(spaces);
    text.lines().map(|line| format!("{}{}", pad, line)).collect::<Vec<_>>().join("\n")
}

/// Hex-encoded random bytes sized so the result never exceeds `len` characters.
/// Hex doubles the byte count, so `len / 2` bytes is the widest safe choice.
fn fixed_width_string(len: u32) -> String {
    let bytes = len / 2;
    if bytes == 0 {
        // CHAR(1) and CHAR(0): a single stable character is the only safe value.
        return "type: constant\nvalue: \"x\"".to_string();
    }
    format!(
        "type: random_bytes\nmin_bytes: {}\nmax_bytes: {}\nencoding: hex",
        bytes, bytes
    )
}

fn int_range(min: i64, max: i64) -> String {
    format!("type: int_range\nmin: {}\nmax: {}", min, max)
}

/// Keep the generated magnitude inside the declared precision.
fn decimal_range(precision: u8, column_scale: u8) -> String {
    let integer_digits = precision.saturating_sub(column_scale);
    // Generate at most 18 fractional digits. The writer pads to the column's
    // scale, and a DECIMAL(76, 60) would otherwise ask for 69-digit bounds,
    // past what the generator's 128-bit arithmetic holds.
    let scale = column_scale.min(18);
    // Cap the whole part so very wide DECIMALs stay readable.
    let digits = integer_digits.min(9);
    let max_whole = if digits == 0 {
        0u64
    } else {
        10u64.saturating_pow(digits as u32) - 1
    };
    let max = if scale == 0 {
        format!("{}", max_whole)
    } else {
        format!("{}.{}", max_whole, "9".repeat(scale as usize))
    };
    let min = if scale == 0 {
        "0".to_string()
    } else {
        format!("0.{}", "0".repeat(scale as usize))
    };
    format!(
        "type: decimal_range\nmin: \"{}\"\nmax: \"{}\"\nscale: {}",
        min, max, scale
    )
}

fn string_width(ty: &DorisType) -> Option<u32> {
    match ty {
        DorisType::Char { len } | DorisType::Varchar { len } => Some(*len),
        DorisType::String => Some(u32::MAX),
        _ => None,
    }
}

fn is_integer(ty: &DorisType) -> bool {
    matches!(
        ty,
        DorisType::TinyInt
            | DorisType::SmallInt
            | DorisType::Int
            | DorisType::BigInt
            | DorisType::LargeInt
    )
}

fn contains_any(haystack: &str, needles: &[&str]) -> bool {
    needles.iter().any(|needle| haystack.contains(needle))
}

/// Quote a column name if it is not a plain YAML-safe token.
fn yaml_scalar(name: &str) -> String {
    let safe = !name.is_empty()
        && name
            .chars()
            .all(|ch| ch.is_ascii_alphanumeric() || ch == '_' || ch == '-')
        && !name.chars().next().unwrap().is_ascii_digit();
    if safe {
        name.to_string()
    } else {
        format!("\"{}\"", name.replace('\\', "\\\\").replace('"', "\\\""))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::schema::parse_schema;

    fn derive(sql: &str) -> String {
        let tables = parse_schema(sql).expect("parse");
        spec_yaml_from_table(&tables[0]).expect("derive")
    }

    #[test]
    fn derives_generators_from_types() {
        let yaml = derive(
            "CREATE TABLE t (a INT, b DECIMAL(20,4), c DATE, d DATETIME(3), e STRING, f BOOLEAN) ENGINE=OLAP",
        );
        assert!(yaml.contains("type: int_range"));
        assert!(yaml.contains("type: decimal_range"));
        assert!(yaml.contains("scale: 4"));
        assert!(yaml.contains("\"%Y-%m-%d\""));
        assert!(yaml.contains("%Y-%m-%d %H:%M:%S%.3f"));
        assert!(yaml.contains("type: lorem"));
        assert!(yaml.contains("type: weighted_choice"));
    }

    #[test]
    fn narrow_strings_never_exceed_declared_width() {
        let yaml = derive("CREATE TABLE t (vin CHAR(17), tag VARCHAR(8), one CHAR(1)) ENGINE=OLAP");
        // CHAR(17) -> 8 bytes -> 16 hex chars, within 17.
        assert!(yaml.contains("min_bytes: 8"));
        assert!(yaml.contains("min_bytes: 4"));
        assert!(yaml.contains("value: \"x\""));
        assert!(!yaml.contains("type: lorem"), "narrow columns must not use lorem");
    }

    #[test]
    fn name_heuristics_apply_only_to_roomy_columns() {
        let roomy = derive("CREATE TABLE t (email VARCHAR(255), first_name VARCHAR(128)) ENGINE=OLAP");
        assert!(roomy.contains("type: email"));
        assert!(roomy.contains("part: first"));

        let narrow = derive("CREATE TABLE t (email CHAR(10)) ENGINE=OLAP");
        assert!(!narrow.contains("type: email"), "email must not overflow CHAR(10)");
        assert!(narrow.contains("type: random_bytes"));
    }

    #[test]
    fn integer_id_becomes_a_sequence() {
        let yaml = derive("CREATE TABLE t (id BIGINT, user_id BIGINT) ENGINE=OLAP");
        assert!(yaml.contains("type: sequence"));
        // user_id stays a range: it references another table's key space.
        assert!(yaml.contains("type: int_range"));
    }

    #[test]
    fn decimal_bounds_respect_precision() {
        let yaml = derive("CREATE TABLE t (a DECIMAL(5,2), b DECIMAL(3,0)) ENGINE=OLAP");
        assert!(yaml.contains("max: \"999.99\""), "5,2 leaves 3 integer digits");
        assert!(yaml.contains("max: \"999\""));
    }

    #[test]
    fn quotes_unusual_column_names() {
        let yaml = derive("CREATE TABLE t (`odd name` INT, `2fa` INT) ENGINE=OLAP");
        assert!(yaml.contains("name: \"odd name\""));
        assert!(yaml.contains("name: \"2fa\""));
    }

    #[test]
    fn refuses_ungeneratable_columns() {
        let tables =
            parse_schema("CREATE TABLE t (a INT, b AGG_STATE<sum(int)>) ENGINE=OLAP").unwrap();
        let err = spec_yaml_from_table(&tables[0]).unwrap_err().to_string();
        assert!(err.contains("b"), "error names the offending column: {}", err);
    }

    #[test]
    fn lorem_never_outgrows_its_varchar() {
        let longest = longest_lorem_word();
        for len in [64u32, 70, 94, 95, 200, 65533] {
            let yaml = lorem_within(len);
            let words: u32 = yaml
                .lines()
                .find_map(|line| line.strip_prefix("words_max: "))
                .unwrap()
                .parse()
                .unwrap();
            let worst_case = words * longest + words.saturating_sub(1);
            assert!(worst_case <= len, "VARCHAR({}) could receive {} bytes", len, worst_case);
        }
    }

    #[test]
    fn derives_every_type_including_nested_ones() {
        let yaml = derive(
            "CREATE TABLE t (
                a ARRAY<INT>, b MAP<VARCHAR(32), DECIMAL(10,2)>,
                c STRUCT<city:VARCHAR(80), zip:INT>, d JSON, e IPV4, f IPV6,
                g BITMAP, h HLL, i QUANTILE_STATE, j LARGEINT, k DECIMAL(76,60)
            ) ENGINE=OLAP",
        );
        for expected in [
            "type: array", "element:", "type: map", "key:", "type: struct",
            "type: ipv4", "type: ipv6", "to_bitmap(`g`)", "hll_hash(`h`)",
            "to_quantile_state(`i`)", "\"18446744073709551615\"",
        ] {
            assert!(yaml.contains(expected), "missing {}:\n{}", expected, yaml);
        }
        // Nested blocks must still be valid YAML.
        serde_yaml::from_str::<serde_yaml::Value>(&yaml).expect("derived YAML parses");
    }

    #[test]
    fn preserves_column_order() {
        let yaml = derive("CREATE TABLE t (z INT, a INT, m INT) ENGINE=OLAP");
        let z = yaml.find("name: z").unwrap();
        let a = yaml.find("name: a").unwrap();
        let m = yaml.find("name: m").unwrap();
        assert!(z < a && a < m, "DDL order must be preserved");
    }
}
