//! Doris `CREATE TABLE` parsing, restricted to what data generation needs:
//! column name, type, and nullability. Everything after the column block
//! (ENGINE, key model, DISTRIBUTED BY, PROPERTIES) is ignored.

use anyhow::{anyhow, bail, Result};

#[derive(Debug, Clone, PartialEq)]
pub enum DorisType {
    Boolean,
    TinyInt,
    SmallInt,
    Int,
    BigInt,
    /// 128-bit integer, range ±(2^127 - 1).
    LargeInt,
    Float,
    Double,
    /// Precision up to 38, or up to 76 when the cluster has
    /// `enable_decimal256` turned on.
    Decimal { precision: u8, scale: u8 },
    Date,
    DateTime { scale: u8 },
    /// Length in UTF-8 bytes, 1..=255.
    Char { len: u32 },
    /// Length in UTF-8 bytes, 1..=65533.
    Varchar { len: u32 },
    String,
    Json,
    Variant,
    Ipv4,
    Ipv6,
    Array(Box<DorisType>),
    Map(Box<DorisType>, Box<DorisType>),
    Struct(Vec<(String, DorisType)>),
    /// Sketch types have no Parquet form Doris loads directly. They are
    /// written as their source values and built during the load with
    /// `to_bitmap`, `hll_hash` or `to_quantile_state`.
    Bitmap,
    Hll,
    QuantileState,
    /// Carries the aggregate's signature, e.g. `sum(int)`. Not generatable:
    /// its load expression depends on the function.
    AggState(String),
}

/// DECIMAL precision above this needs Doris's `enable_decimal256`.
pub const DECIMAL128_MAX_PRECISION: u8 = 38;
pub const DECIMAL256_MAX_PRECISION: u8 = 76;

impl DorisType {
    /// Whether the generator layer can produce values for this type.
    pub fn is_generatable(&self) -> bool {
        match self {
            DorisType::AggState(_) => false,
            DorisType::Array(element) => element.is_generatable(),
            DorisType::Map(key, value) => key.is_generatable() && value.is_generatable(),
            DorisType::Struct(fields) => fields.iter().all(|(_, ty)| ty.is_generatable()),
            _ => true,
        }
    }

    /// The type as it would be written in Doris DDL, for messages.
    pub fn sql_name(&self) -> String {
        match self {
            DorisType::Boolean => "BOOLEAN".into(),
            DorisType::TinyInt => "TINYINT".into(),
            DorisType::SmallInt => "SMALLINT".into(),
            DorisType::Int => "INT".into(),
            DorisType::BigInt => "BIGINT".into(),
            DorisType::LargeInt => "LARGEINT".into(),
            DorisType::Float => "FLOAT".into(),
            DorisType::Double => "DOUBLE".into(),
            DorisType::Decimal { precision, scale } => format!("DECIMAL({},{})", precision, scale),
            DorisType::Date => "DATE".into(),
            DorisType::DateTime { scale } => format!("DATETIME({})", scale),
            DorisType::Char { len } => format!("CHAR({})", len),
            DorisType::Varchar { len } => format!("VARCHAR({})", len),
            DorisType::String => "STRING".into(),
            DorisType::Json => "JSON".into(),
            DorisType::Variant => "VARIANT".into(),
            DorisType::Ipv4 => "IPV4".into(),
            DorisType::Ipv6 => "IPV6".into(),
            DorisType::Array(element) => format!("ARRAY<{}>", element.sql_name()),
            DorisType::Map(key, value) => format!("MAP<{},{}>", key.sql_name(), value.sql_name()),
            DorisType::Struct(fields) => format!(
                "STRUCT<{}>",
                fields
                    .iter()
                    .map(|(name, ty)| format!("{}:{}", name, ty.sql_name()))
                    .collect::<Vec<_>>()
                    .join(",")
            ),
            DorisType::Bitmap => "BITMAP".into(),
            DorisType::Hll => "HLL".into(),
            DorisType::QuantileState => "QUANTILE_STATE".into(),
            DorisType::AggState(signature) => format!("AGG_STATE<{}>", signature),
        }
    }

    /// The Doris function a load must apply to turn the written source values
    /// into this column's type, for sketch types only.
    pub fn load_function(&self) -> Option<&'static str> {
        match self {
            DorisType::Bitmap => Some("to_bitmap"),
            DorisType::Hll => Some("hll_hash"),
            DorisType::QuantileState => Some("to_quantile_state"),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct Column {
    pub name: String,
    pub ty: DorisType,
    pub nullable: bool,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Table {
    pub database: Option<String>,
    pub name: String,
    pub columns: Vec<Column>,
}

/// Parse every `CREATE TABLE` statement in `sql`.
pub fn parse_schema(sql: &str) -> Result<Vec<Table>> {
    let stripped = strip_comments(sql);
    let mut tables = Vec::new();
    let mut cursor = 0usize;

    while let Some(offset) = find_create_table(&stripped[cursor..]) {
        let start = cursor + offset;
        let after_keyword = start + "CREATE".len();
        let rest = &stripped[after_keyword..];

        let (ident, ident_end) = match read_table_identifier(rest) {
            Some(found) => found,
            None => {
                cursor = after_keyword;
                continue;
            }
        };

        let block_search = &stripped[after_keyword + ident_end..];
        let open = match block_search.find('(') {
            Some(index) => after_keyword + ident_end + index,
            None => bail!("CREATE TABLE `{}` has no column block", ident.1),
        };
        let close = match_paren(&stripped, open)
            .ok_or_else(|| anyhow!("unbalanced parentheses in CREATE TABLE `{}`", ident.1))?;

        let body = &stripped[open + 1..close];
        let columns = parse_column_block(body)
            .map_err(|err| anyhow!("in table `{}`: {}", ident.1, err))?;

        if columns.is_empty() {
            bail!("table `{}` declares no columns", ident.1);
        }

        tables.push(Table {
            database: ident.0,
            name: ident.1,
            columns,
        });
        cursor = close + 1;
    }

    if tables.is_empty() {
        bail!("no CREATE TABLE statement found");
    }
    Ok(tables)
}

/// Pick one table by name. `None` selects the only table, erroring if ambiguous.
pub fn select_table(tables: Vec<Table>, wanted: Option<&str>) -> Result<Table> {
    match wanted {
        Some(name) => {
            let target = name.rsplit('.').next().unwrap_or(name);
            tables
                .into_iter()
                .find(|table| table.name.eq_ignore_ascii_case(target))
                .ok_or_else(|| anyhow!("table `{}` not found in schema", name))
        }
        None => {
            let mut iter = tables.into_iter();
            let first = iter
                .next()
                .ok_or_else(|| anyhow!("no CREATE TABLE statement found"))?;
            if iter.next().is_some() {
                bail!("schema declares multiple tables; pass --table <name>");
            }
            Ok(first)
        }
    }
}

fn find_create_table(haystack: &str) -> Option<usize> {
    let upper = haystack.to_ascii_uppercase();
    let bytes = upper.as_bytes();
    let mut from = 0usize;
    while let Some(found) = upper[from..].find("CREATE") {
        let at = from + found;
        let before_ok = at == 0 || !is_ident_byte(bytes[at - 1]);
        if before_ok {
            let tail = upper[at + "CREATE".len()..].trim_start();
            // Allow CREATE [EXTERNAL] TABLE.
            let tail = tail.strip_prefix("EXTERNAL").map(str::trim_start).unwrap_or(tail);
            if tail.starts_with("TABLE") {
                let after = tail["TABLE".len()..].as_bytes().first().copied();
                if after.map_or(true, |byte| !is_ident_byte(byte)) {
                    return Some(at);
                }
            }
        }
        from = at + "CREATE".len();
    }
    None
}

/// Read `[EXTERNAL] TABLE [IF NOT EXISTS] [db.]name`, returning the identifier
/// and the offset just past it.
fn read_table_identifier(input: &str) -> Option<((Option<String>, String), usize)> {
    let mut cursor = 0usize;
    let expect = |word: &str, cursor: &mut usize, required: bool| -> bool {
        let rest = &input[*cursor..];
        let trimmed = rest.trim_start();
        let skipped = rest.len() - trimmed.len();
        if trimmed.len() >= word.len() && trimmed[..word.len()].eq_ignore_ascii_case(word) {
            let after = trimmed.as_bytes().get(word.len()).copied();
            if after.map_or(true, |byte| !is_ident_byte(byte)) {
                *cursor += skipped + word.len();
                return true;
            }
        }
        !required
    };

    expect("EXTERNAL", &mut cursor, false);
    if !expect("TABLE", &mut cursor, true) {
        return None;
    }
    if expect("IF", &mut cursor, false) {
        expect("NOT", &mut cursor, false);
        expect("EXISTS", &mut cursor, false);
    }

    let rest = &input[cursor..];
    let trimmed = rest.trim_start();
    cursor += rest.len() - trimmed.len();

    let (first, used) = read_ident(trimmed)?;
    cursor += used;

    let tail = &input[cursor..];
    let tail_trimmed = tail.trim_start();
    if tail_trimmed.starts_with('.') {
        let skipped = tail.len() - tail_trimmed.len();
        let after_dot = &tail_trimmed[1..];
        let after_trimmed = after_dot.trim_start();
        if let Some((second, used_second)) = read_ident(after_trimmed) {
            cursor += skipped + 1 + (after_dot.len() - after_trimmed.len()) + used_second;
            return Some(((Some(first), second), cursor));
        }
    }
    Some(((None, first), cursor))
}

/// Read a backtick-quoted or bare identifier, returning it and bytes consumed.
fn read_ident(input: &str) -> Option<(String, usize)> {
    let bytes = input.as_bytes();
    if bytes.first() == Some(&b'`') {
        let end = input[1..].find('`')? + 1;
        return Some((input[1..end].to_string(), end + 1));
    }
    let end = bytes.iter().position(|byte| !is_ident_byte(*byte)).unwrap_or(bytes.len());
    if end == 0 {
        return None;
    }
    Some((input[..end].to_string(), end))
}

fn is_ident_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'$'
}

/// Find the index of the `)` matching the `(` at `open`, respecting quoting.
fn match_paren(input: &str, open: usize) -> Option<usize> {
    let bytes = input.as_bytes();
    let mut depth = 0usize;
    let mut index = open;
    let mut quote: Option<u8> = None;

    while index < bytes.len() {
        let byte = bytes[index];
        match quote {
            Some(active) => {
                if byte == b'\\' && active != b'`' {
                    index += 2;
                    continue;
                }
                if byte == active {
                    quote = None;
                }
            }
            None => match byte {
                b'`' | b'\'' | b'"' => quote = Some(byte),
                b'(' => depth += 1,
                b')' => {
                    depth -= 1;
                    if depth == 0 {
                        return Some(index);
                    }
                }
                _ => {}
            },
        }
        index += 1;
    }
    None
}

/// Split on commas that sit at nesting depth zero and outside quotes.
/// Angle brackets nest too, so `MAP<STRING, BIGINT>` stays one entry.
fn split_top_level(input: &str) -> Vec<&str> {
    let bytes = input.as_bytes();
    let mut parts = Vec::new();
    let mut depth = 0usize;
    let mut start = 0usize;
    let mut quote: Option<u8> = None;

    for index in 0..bytes.len() {
        let byte = bytes[index];
        match quote {
            Some(active) => {
                if byte == active {
                    quote = None;
                }
            }
            None => match byte {
                b'`' | b'\'' | b'"' => quote = Some(byte),
                b'(' | b'<' => depth += 1,
                b')' | b'>' => depth = depth.saturating_sub(1),
                b',' if depth == 0 => {
                    parts.push(&input[start..index]);
                    start = index + 1;
                }
                _ => {}
            },
        }
    }
    parts.push(&input[start..]);
    parts
}

/// Entries that are table constraints rather than column definitions.
const NON_COLUMN_LEADS: &[&str] = &[
    "INDEX", "KEY", "UNIQUE", "PRIMARY", "DUPLICATE", "AGGREGATE", "CONSTRAINT", "FOREIGN",
    "CLUSTER",
];

fn parse_column_block(body: &str) -> Result<Vec<Column>> {
    let mut columns = Vec::new();
    for entry in split_top_level(body) {
        let trimmed = entry.trim();
        if trimmed.is_empty() {
            continue;
        }
        if is_non_column_entry(trimmed) {
            continue;
        }
        columns.push(parse_column(trimmed)?);
    }
    Ok(columns)
}

fn is_non_column_entry(entry: &str) -> bool {
    let first = entry.split_whitespace().next().unwrap_or("");
    // A backticked leading token is always a column name, even if it spells KEY.
    if first.starts_with('`') {
        return false;
    }
    NON_COLUMN_LEADS
        .iter()
        .any(|lead| first.eq_ignore_ascii_case(lead))
}

fn parse_column(entry: &str) -> Result<Column> {
    let (name, used) = read_ident(entry)
        .ok_or_else(|| anyhow!("cannot read column name from `{}`", entry))?;
    let rest = entry[used..].trim_start();
    if rest.is_empty() {
        bail!("column `{}` has no type", name);
    }

    let (type_text, after_type) = read_type_token(rest);
    let ty = parse_type(type_text)
        .map_err(|err| anyhow!("column `{}`: {}", name, err))?;

    let modifiers = strip_string_literals(&entry[used..][after_type..]);
    let upper = modifiers.to_ascii_uppercase();
    let nullable = !contains_word(&upper, "NOT NULL");

    Ok(Column { name, ty, nullable })
}

/// Read a type token such as `DECIMAL(20, 4)` or `VARCHAR(65533)`.
fn read_type_token(input: &str) -> (&str, usize) {
    let bytes = input.as_bytes();
    let mut index = 0usize;
    while index < bytes.len() && (is_ident_byte(bytes[index]) || bytes[index] == b'<') {
        // Angle brackets appear in ARRAY<INT>, MAP<..>, STRUCT<..>.
        if bytes[index] == b'<' {
            let mut depth = 0usize;
            while index < bytes.len() {
                match bytes[index] {
                    b'<' => depth += 1,
                    b'>' => {
                        depth -= 1;
                        if depth == 0 {
                            index += 1;
                            break;
                        }
                    }
                    _ => {}
                }
                index += 1;
            }
            break;
        }
        index += 1;
    }

    let after_name = input[index..].trim_start();
    if after_name.starts_with('(') {
        let skipped = input[index..].len() - after_name.len();
        if let Some(close) = match_paren(input, index + skipped) {
            return (input[..close + 1].trim_end(), close + 1);
        }
    }
    (input[..index].trim_end(), index)
}

fn parse_type(text: &str) -> Result<DorisType> {
    let text = strip_nullability(text.trim());
    if text.is_empty() {
        bail!("missing type");
    }

    let (base, args) = match text.find('(') {
        Some(open) if text.ends_with(')') => {
            (text[..open].trim(), Some(text[open + 1..text.len() - 1].trim()))
        }
        _ => (text, None),
    };

    if let Some(open) = base.find('<') {
        let name = base[..open].trim().to_ascii_uppercase();
        let inner = base[open + 1..base.len().saturating_sub(1)].trim();
        return parse_container(&name, inner);
    }

    let upper = base.to_ascii_uppercase();
    let arg_list: Vec<&str> = args
        .map(|text| split_top_level(text).into_iter().map(str::trim).collect())
        .unwrap_or_default();

    let parse_arg = |index: usize, fallback: u32| -> Result<u32> {
        match arg_list.get(index) {
            Some(text) => text
                .parse::<u32>()
                .map_err(|_| anyhow!("bad argument `{}` in type `{}`", text, base)),
            None => Ok(fallback),
        }
    };

    let ty = match upper.as_str() {
        "BOOLEAN" | "BOOL" => DorisType::Boolean,
        "TINYINT" => DorisType::TinyInt,
        "SMALLINT" => DorisType::SmallInt,
        "INT" | "INTEGER" => DorisType::Int,
        "BIGINT" => DorisType::BigInt,
        "LARGEINT" => DorisType::LargeInt,
        "FLOAT" | "REAL" => DorisType::Float,
        "DOUBLE" => DorisType::Double,
        "DECIMAL" | "DECIMALV3" | "DECIMALV2" | "NUMERIC" => {
            // DECIMALV2 is the legacy type; without arguments it is (27, 9).
            let (default_precision, default_scale) =
                if upper == "DECIMALV2" { (27, 9) } else { (9, 0) };
            let precision = parse_arg(0, default_precision)?;
            let scale = parse_arg(1, default_scale)?;
            if precision == 0 || precision > DECIMAL256_MAX_PRECISION as u32 {
                bail!(
                    "DECIMAL precision {} out of range 1..={} (above {} needs enable_decimal256)",
                    precision,
                    DECIMAL256_MAX_PRECISION,
                    DECIMAL128_MAX_PRECISION
                );
            }
            if scale > precision {
                bail!("DECIMAL scale {} exceeds precision {}", scale, precision);
            }
            DorisType::Decimal {
                precision: precision as u8,
                scale: scale as u8,
            }
        }
        "DATE" | "DATEV2" | "DATEV1" => DorisType::Date,
        "DATETIME" | "DATETIMEV2" | "DATETIMEV1" => {
            let scale = parse_arg(0, 0)?;
            if scale > 6 {
                bail!("DATETIME scale {} out of range 0..=6", scale);
            }
            DorisType::DateTime { scale: scale as u8 }
        }
        "CHAR" => DorisType::Char {
            len: parse_arg(0, 1)?,
        },
        "VARCHAR" => {
            let len = match arg_list.first() {
                Some(&"*") => 65533,
                _ => parse_arg(0, 65533)?,
            };
            DorisType::Varchar { len }
        }
        "STRING" | "TEXT" => DorisType::String,
        "JSON" | "JSONB" => DorisType::Json,
        "VARIANT" => DorisType::Variant,
        "IPV4" => DorisType::Ipv4,
        "IPV6" => DorisType::Ipv6,
        "BITMAP" => DorisType::Bitmap,
        "HLL" => DorisType::Hll,
        "QUANTILE_STATE" => DorisType::QuantileState,
        "AGG_STATE" => DorisType::AggState(String::new()),
        other => bail!("unsupported Doris type `{}`", other),
    };
    Ok(ty)
}

fn parse_container(name: &str, inner: &str) -> Result<DorisType> {
    match name {
        "ARRAY" => Ok(DorisType::Array(Box::new(parse_type(inner)?))),
        "MAP" => {
            let parts = split_top_level(inner);
            if parts.len() != 2 {
                bail!("MAP needs exactly two type arguments");
            }
            Ok(DorisType::Map(
                Box::new(parse_type(parts[0])?),
                Box::new(parse_type(parts[1])?),
            ))
        }
        "STRUCT" => {
            let mut fields = Vec::new();
            for part in split_top_level(inner) {
                let part = part.trim();
                if part.is_empty() {
                    continue;
                }
                let (field, used) = read_ident(part)
                    .ok_or_else(|| anyhow!("cannot read STRUCT field name in `{}`", part))?;
                let rest = part[used..].trim_start().trim_start_matches(':').trim_start();
                // A field may end in COMMENT '...'; the type is what precedes it.
                let rest = cut_at_top_level_keyword(rest, "COMMENT");
                if fields.iter().any(|(existing, _): &(String, DorisType)| existing == &field) {
                    bail!("STRUCT declares field `{}` twice", field);
                }
                fields.push((field, parse_type(rest)?));
            }
            if fields.is_empty() {
                bail!("STRUCT needs at least one field");
            }
            Ok(DorisType::Struct(fields))
        }
        "AGG_STATE" => Ok(DorisType::AggState(inner.trim().to_string())),
        other => bail!("unsupported Doris type `{}`", other),
    }
}

/// Drop a trailing `NULL` or `NOT NULL` from an element type such as the
/// `INT NOT NULL` in `ARRAY<INT NOT NULL>`. Elements are always written as
/// nullable Parquet fields, so the marker changes nothing about the output.
fn strip_nullability(text: &str) -> &str {
    let upper = text.to_ascii_uppercase();
    for suffix in [" NOT NULL", " NULL"] {
        if upper.ends_with(suffix) && !upper.ends_with('>') {
            return text[..text.len() - suffix.len()].trim_end();
        }
    }
    text
}

/// Everything before `keyword` when it appears outside brackets and quotes.
fn cut_at_top_level_keyword<'a>(text: &'a str, keyword: &str) -> &'a str {
    let blanked = strip_string_literals(text).to_ascii_uppercase();
    let bytes = blanked.as_bytes();
    let mut depth = 0usize;
    for index in 0..bytes.len() {
        match bytes[index] {
            b'(' | b'<' => depth += 1,
            b')' | b'>' => depth = depth.saturating_sub(1),
            _ if depth == 0 && blanked[index..].starts_with(keyword) => {
                let before_ok = index == 0 || !is_ident_byte(bytes[index - 1]);
                let after = bytes.get(index + keyword.len()).copied();
                if before_ok && after.is_none_or(|byte| !is_ident_byte(byte)) {
                    return text[..index].trim_end();
                }
            }
            _ => {}
        }
    }
    text
}

/// Blank out quoted literals so keyword scanning cannot match inside a COMMENT.
fn strip_string_literals(input: &str) -> String {
    let mut out = String::with_capacity(input.len());
    let mut quote: Option<char> = None;
    let mut escaped = false;

    for ch in input.chars() {
        match quote {
            Some(active) => {
                if escaped {
                    escaped = false;
                } else if ch == '\\' && active != '`' {
                    escaped = true;
                } else if ch == active {
                    quote = None;
                }
                out.push(' ');
            }
            None => {
                if ch == '\'' || ch == '"' || ch == '`' {
                    quote = Some(ch);
                    out.push(' ');
                } else {
                    out.push(ch);
                }
            }
        }
    }
    out
}

/// Match a multi-word keyword phrase on whitespace-normalised input.
fn contains_word(haystack_upper: &str, phrase: &str) -> bool {
    let normalised: Vec<&str> = haystack_upper.split_whitespace().collect();
    let wanted: Vec<&str> = phrase.split_whitespace().collect();
    normalised.windows(wanted.len()).any(|window| window == wanted.as_slice())
}

/// Remove `--` and `#` line comments and `/* */` block comments, preserving quoting.
fn strip_comments(input: &str) -> String {
    let mut out = String::with_capacity(input.len());
    let mut chars = input.char_indices().peekable();
    let mut quote: Option<char> = None;

    while let Some((_, ch)) = chars.next() {
        if let Some(active) = quote {
            out.push(ch);
            if ch == '\\' && active != '`' {
                if let Some((_, escaped)) = chars.next() {
                    out.push(escaped);
                }
                continue;
            }
            if ch == active {
                quote = None;
            }
            continue;
        }

        match ch {
            '`' | '\'' | '"' => {
                quote = Some(ch);
                out.push(ch);
            }
            '-' if chars.peek().map(|(_, next)| *next) == Some('-') => {
                skip_to_newline(&mut chars);
                out.push('\n');
            }
            '#' => {
                skip_to_newline(&mut chars);
                out.push('\n');
            }
            '/' if chars.peek().map(|(_, next)| *next) == Some('*') => {
                chars.next();
                let mut prev = '\0';
                for (_, inner) in chars.by_ref() {
                    if prev == '*' && inner == '/' {
                        break;
                    }
                    prev = inner;
                }
                out.push(' ');
            }
            _ => out.push(ch),
        }
    }
    out
}

fn skip_to_newline(chars: &mut std::iter::Peekable<std::str::CharIndices<'_>>) {
    for (_, ch) in chars.by_ref() {
        if ch == '\n' {
            break;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn columns_of(sql: &str) -> Vec<Column> {
        let tables = parse_schema(sql).expect("parse");
        assert_eq!(tables.len(), 1);
        tables.into_iter().next().unwrap().columns
    }

    #[test]
    fn parses_scalar_types_and_nullability() {
        let columns = columns_of(
            r#"
            CREATE TABLE IF NOT EXISTS tracking_db.u347ug_data
            (
                `vin`        CHAR(17)        NOT NULL,
                `ts`         DATETIME(3)     NOT NULL,
                `imei`       LARGEINT        DEFAULT "18446744073709551615",
                `speed`      FLOAT,
                `payload`    VARCHAR(65533)  NULL,
                `amount`     DECIMAL(20, 4),
                `day`        DATE,
                `flag`       BOOLEAN
            )
            ENGINE=OLAP
            UNIQUE KEY(`vin`, `ts`)
            DISTRIBUTED BY HASH(`vin`) BUCKETS 32
            PROPERTIES ("replication_num" = "3");
            "#,
        );

        let expected = vec![
            ("vin", DorisType::Char { len: 17 }, false),
            ("ts", DorisType::DateTime { scale: 3 }, false),
            ("imei", DorisType::LargeInt, true),
            ("speed", DorisType::Float, true),
            ("payload", DorisType::Varchar { len: 65533 }, true),
            ("amount", DorisType::Decimal { precision: 20, scale: 4 }, true),
            ("day", DorisType::Date, true),
            ("flag", DorisType::Boolean, true),
        ];

        assert_eq!(columns.len(), expected.len());
        for (actual, (name, ty, nullable)) in columns.iter().zip(expected) {
            assert_eq!(actual.name, name);
            assert_eq!(actual.ty, ty, "type mismatch for {}", name);
            assert_eq!(actual.nullable, nullable, "nullability mismatch for {}", name);
        }
    }

    #[test]
    fn comment_text_does_not_affect_nullability() {
        let columns = columns_of(
            r#"CREATE TABLE t (
                `a` INT COMMENT "this column is NOT NULL in the source system",
                `b` INT NOT NULL COMMENT 'plain'
            ) ENGINE=OLAP"#,
        );
        assert!(columns[0].nullable, "COMMENT text must not imply NOT NULL");
        assert!(!columns[1].nullable);
    }

    #[test]
    fn strips_comments_including_non_ascii() {
        let columns = columns_of(
            r#"
            -- 车辆数据表 with a comma, and NOT NULL inside the comment
            /* block comment
               spanning lines */
            CREATE TABLE t (
                `a` INT,  -- 主键
                `b` STRING
            ) ENGINE=OLAP
            "#,
        );
        assert_eq!(columns.len(), 2);
        assert_eq!(columns[0].name, "a");
        assert_eq!(columns[1].name, "b");
        assert!(columns[0].nullable);
    }

    #[test]
    fn skips_index_and_key_entries() {
        let columns = columns_of(
            r#"CREATE TABLE t (
                `a` INT NOT NULL,
                `b` STRING,
                INDEX idx_b (`b`) USING INVERTED,
                INDEX idx_a (`a`) USING BITMAP
            ) ENGINE=OLAP
            DUPLICATE KEY(`a`)"#,
        );
        assert_eq!(columns.len(), 2);
        assert_eq!(columns[1].name, "b");
    }

    #[test]
    fn backticked_name_matching_a_keyword_is_a_column() {
        let columns = columns_of("CREATE TABLE t (`key` INT, `index` STRING) ENGINE=OLAP");
        assert_eq!(columns.len(), 2);
        assert_eq!(columns[0].name, "key");
        assert_eq!(columns[1].name, "index");
    }

    #[test]
    fn handles_aggregate_columns_and_bare_names() {
        let columns = columns_of(
            r#"CREATE TABLE t (
                user_id BIGINT NOT NULL,
                total DECIMAL(38, 9) SUM DEFAULT "0",
                last_seen DATETIME REPLACE_IF_NOT_NULL
            ) ENGINE=OLAP
            AGGREGATE KEY(user_id)"#,
        );
        assert_eq!(columns.len(), 3);
        assert_eq!(columns[0].ty, DorisType::BigInt);
        assert_eq!(columns[1].ty, DorisType::Decimal { precision: 38, scale: 9 });
        assert_eq!(columns[2].ty, DorisType::DateTime { scale: 0 });
        assert!(!columns[0].nullable);
        assert!(columns[1].nullable);
    }

    #[test]
    fn parses_varchar_star_and_type_aliases() {
        let columns = columns_of(
            "CREATE TABLE t (a VARCHAR(*), b TEXT, c INTEGER, d BOOL, e DECIMALV3(10,2), f DATETIMEV2(6)) ENGINE=OLAP",
        );
        assert_eq!(columns[0].ty, DorisType::Varchar { len: 65533 });
        assert_eq!(columns[1].ty, DorisType::String);
        assert_eq!(columns[2].ty, DorisType::Int);
        assert_eq!(columns[3].ty, DorisType::Boolean);
        assert_eq!(columns[4].ty, DorisType::Decimal { precision: 10, scale: 2 });
        assert_eq!(columns[5].ty, DorisType::DateTime { scale: 6 });
    }

    #[test]
    fn parses_container_and_sketch_types() {
        let columns = columns_of(
            "CREATE TABLE t (a ARRAY<INT>, b MAP<STRING, BIGINT>, c STRUCT<x:INT, y:STRING>, d HLL) ENGINE=OLAP",
        );
        assert_eq!(columns[0].ty, DorisType::Array(Box::new(DorisType::Int)));
        assert_eq!(
            columns[1].ty,
            DorisType::Map(Box::new(DorisType::String), Box::new(DorisType::BigInt))
        );
        assert_eq!(
            columns[2].ty,
            DorisType::Struct(vec![
                ("x".to_string(), DorisType::Int),
                ("y".to_string(), DorisType::String),
            ])
        );
        assert_eq!(columns[3].ty, DorisType::Hll);
        // Containers and sketches are generatable; only AGG_STATE is not.
        for column in &columns {
            assert!(column.ty.is_generatable(), "{} should be generatable", column.name);
        }
        assert!(!DorisType::AggState("sum(int)".into()).is_generatable());
    }

    #[test]
    fn selects_among_multiple_tables() {
        let sql = r#"
            CREATE TABLE db.first (a INT) ENGINE=OLAP;
            CREATE TABLE IF NOT EXISTS db.second (b STRING) ENGINE=OLAP;
        "#;
        let tables = parse_schema(sql).expect("parse");
        assert_eq!(tables.len(), 2);
        assert_eq!(tables[0].database.as_deref(), Some("db"));
        assert_eq!(tables[0].name, "first");

        let picked = select_table(tables.clone(), Some("second")).expect("select");
        assert_eq!(picked.columns[0].name, "b");

        let qualified = select_table(tables.clone(), Some("db.second")).expect("select qualified");
        assert_eq!(qualified.name, "second");

        assert!(select_table(tables.clone(), None).is_err(), "ambiguous without --table");
        assert!(select_table(tables, Some("missing")).is_err());
    }

    #[test]
    fn single_table_needs_no_selection() {
        let tables = parse_schema("CREATE TABLE only (a INT) ENGINE=OLAP").expect("parse");
        let picked = select_table(tables, None).expect("select");
        assert_eq!(picked.name, "only");
        assert_eq!(picked.database, None);
    }

    #[test]
    fn rejects_bad_input() {
        assert!(parse_schema("SELECT 1").is_err(), "no CREATE TABLE");
        assert!(parse_schema("CREATE TABLE t (a NOSUCHTYPE) ENGINE=OLAP").is_err());
        assert!(parse_schema("CREATE TABLE t (a DECIMAL(2, 5)) ENGINE=OLAP").is_err(), "scale > precision");
        assert!(parse_schema("CREATE TABLE t (a DECIMAL(99, 2)) ENGINE=OLAP").is_err(), "precision > 38");
        assert!(parse_schema("CREATE TABLE t (a DATETIME(9)) ENGINE=OLAP").is_err(), "scale > 6");
        assert!(parse_schema("CREATE TABLE t () ENGINE=OLAP").is_err(), "no columns");
    }

    #[test]
    fn handles_external_table_and_trailing_semicolons() {
        let tables = parse_schema("CREATE EXTERNAL TABLE ext (a INT);").expect("parse");
        assert_eq!(tables[0].name, "ext");
    }

    #[test]
    fn parses_every_doris_column_type() {
        let columns = columns_of(
            "CREATE TABLE t (
                a IPV4, b IPV6,
                c DECIMAL(76, 10), d DECIMALV2, e DATEV1, f DATETIMEV1,
                g BITMAP, h HLL, i QUANTILE_STATE, j AGG_STATE<sum(int)>,
                k STRUCT<city:VARCHAR(32) COMMENT 'where, exactly', zip:INT COMMENT \"code\">,
                l ARRAY<INT NOT NULL>,
                m ARRAY<MAP<STRING, ARRAY<DECIMAL(10,2)>>>,
                n MAP<INT, STRUCT<x:DATETIME(3), y:LARGEINT>>
            ) ENGINE=OLAP",
        );
        let ty = |index: usize| columns[index].ty.clone();
        assert_eq!(ty(0), DorisType::Ipv4);
        assert_eq!(ty(1), DorisType::Ipv6);
        assert_eq!(ty(2), DorisType::Decimal { precision: 76, scale: 10 });
        assert_eq!(ty(3), DorisType::Decimal { precision: 27, scale: 9 });
        assert_eq!(ty(4), DorisType::Date);
        assert_eq!(ty(5), DorisType::DateTime { scale: 0 });
        assert_eq!(ty(6), DorisType::Bitmap);
        assert_eq!(ty(7), DorisType::Hll);
        assert_eq!(ty(8), DorisType::QuantileState);
        assert_eq!(ty(9), DorisType::AggState("sum(int)".into()));
        assert_eq!(
            ty(10),
            DorisType::Struct(vec![
                ("city".into(), DorisType::Varchar { len: 32 }),
                ("zip".into(), DorisType::Int),
            ])
        );
        assert_eq!(ty(11), DorisType::Array(Box::new(DorisType::Int)));
        assert_eq!(
            ty(12).sql_name(),
            "ARRAY<MAP<STRING,ARRAY<DECIMAL(10,2)>>>"
        );
        assert_eq!(
            ty(13).sql_name(),
            "MAP<INT,STRUCT<x:DATETIME(3),y:LARGEINT>>"
        );
        assert_eq!(DorisType::Bitmap.load_function(), Some("to_bitmap"));
        assert_eq!(DorisType::Int.load_function(), None);
    }

    #[test]
    fn rejects_malformed_new_types() {
        assert!(parse_schema("CREATE TABLE t (a DECIMAL(77, 2)) ENGINE=OLAP").is_err());
        assert!(parse_schema("CREATE TABLE t (a STRUCT<x:INT, x:INT>) ENGINE=OLAP").is_err());
        assert!(parse_schema("CREATE TABLE t (a MAP<INT>) ENGINE=OLAP").is_err());
    }
}
