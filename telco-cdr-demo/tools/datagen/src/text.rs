//! CSV and newline-delimited JSON output.
//!
//! Text rows are rendered from the generated values, not from the Arrow
//! batch, so timestamps keep the format the spec asked for, decimals keep
//! their exact digits, and ARRAY, MAP and STRUCT values render as JSON, which
//! is what Doris reads in both formats.
//!
//! Note what this does *not* do: the Parquet path converts every value into
//! the column's Arrow type, which checks lengths, scales and ranges on every
//! row. Text output has no such step, so those limits are only enforced by
//! the startup validation, which cannot see through a template or a script.

use std::collections::HashSet;

use anyhow::{anyhow, Result};

use crate::compile::{CompiledSpec, CsvSpecRuntime, RowContext};
use crate::schema::{Node, RowTree};
use crate::value::Value;

/// One output column: its name, and whether its JSON value must be quoted
/// to survive parsers that hold numbers as doubles (LARGEINT, wide DECIMAL).
#[derive(Debug, Clone)]
pub struct OutputColumn {
    pub name: String,
    pub quote_in_json: bool,
}

impl OutputColumn {
    pub fn new(name: impl Into<String>) -> Self {
        Self { name: name.into(), quote_in_json: false }
    }

    /// The spec's visible fields, in output order.
    pub fn from_spec(spec: &CompiledSpec) -> Vec<Self> {
        spec.output_order
            .iter()
            .map(|&index| Self::new(spec.fields[index].name.clone()))
            .collect()
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TextFormat {
    Csv,
    /// One JSON object per line, which Doris loads with `read_json_by_line`.
    Json,
}

pub struct TextEncoder {
    format: TextFormat,
    names: Vec<String>,
    /// Columns whose JSON value is quoted. A JSON number is a double to most
    /// parsers, so LARGEINT and wide DECIMAL values would be rounded on the
    /// way in. As text they cast exactly.
    quote_in_json: HashSet<String>,
    /// Dotted column names nest in JSON: `customer.name` is written as
    /// `{"customer":{"name":...}}`. CSV keeps the dotted header.
    tree: RowTree,
    csv: CsvSpecRuntime,
    /// Whether CSV output starts with a header line.
    header: bool,
    /// JSON for Arrow's reader (the Parquet path): timestamps carry full
    /// precision in ISO form instead of the spec's display format.
    arrow: bool,
}

impl TextEncoder {
    pub fn new(format: TextFormat, columns: &[OutputColumn], csv: CsvSpecRuntime) -> Self {
        let names: Vec<String> = columns.iter().map(|column| column.name.clone()).collect();
        // A path and its prefix both present is caught at spec validation;
        // fall back to flat names rather than fail here.
        let tree = RowTree::from_names(names.iter().cloned())
            .unwrap_or_else(|_| RowTree::flat(names.iter().cloned()));
        Self {
            format,
            names,
            quote_in_json: columns
                .iter()
                .filter(|column| column.quote_in_json)
                .map(|column| column.name.clone())
                .collect(),
            tree,
            csv,
            header: true,
            arrow: false,
        }
    }

    /// Render JSON for Arrow's reader; see [`Value::write_json_arrow`].
    pub fn set_arrow_mode(&mut self, arrow: bool) {
        self.arrow = arrow;
    }

    /// Turn the CSV header line off (it is on by default).
    pub fn set_header(&mut self, header: bool) {
        self.header = header;
    }

    /// The CSV header. JSON names every field on every line, so it has none.
    pub fn header(&self) -> Result<Option<Vec<u8>>> {
        if self.format != TextFormat::Csv || !self.header {
            return Ok(None);
        }
        let mut out = Vec::new();
        self.write_csv_record(&mut out, self.names.iter().map(|name| name.as_str()));
        Ok(Some(out))
    }

    pub fn encode_row(&self, ctx: &RowContext, out: &mut Vec<u8>) -> Result<()> {
        match self.format {
            TextFormat::Csv => {
                for (index, name) in self.names.iter().enumerate() {
                    if index > 0 {
                        out.push(self.csv.delimiter);
                    }
                    match self.value(ctx, name)? {
                        // The null marker goes in raw. Quoting it would make
                        // it an ordinary string to whatever reads the file.
                        Value::Null => out.extend_from_slice(self.csv.null.as_bytes()),
                        other => self.write_cell(&other.csv_string(&self.csv.null), out),
                    }
                }
                out.extend_from_slice(&self.csv.newline);
                Ok(())
            }
            TextFormat::Json => {
                let mut text = String::new();
                text.push('{');
                self.write_json_nodes(&self.tree.root, ctx, &mut text)?;
                text.push('}');
                out.extend_from_slice(text.as_bytes());
                out.extend_from_slice(&self.csv.newline);
                Ok(())
            }
        }
    }

    fn write_json_nodes(&self, nodes: &[(String, Node)], ctx: &RowContext, out: &mut String) -> Result<()> {
        for (index, (key, node)) in nodes.iter().enumerate() {
            if index > 0 {
                out.push(',');
            }
            Value::String(key.clone()).write_json(out);
            out.push(':');
            match node {
                Node::Leaf(name) => {
                    // Nulls are written rather than left out, so every line
                    // has the same shape.
                    let value = self.value(ctx, name)?;
                    if self.quote_in_json.contains(name) && !matches!(value, Value::Null) {
                        Value::String(value.csv_string("")).write_json(out);
                    } else if self.arrow {
                        value.write_json_arrow(out);
                    } else {
                        value.write_json(out);
                    }
                }
                Node::Record(children) => {
                    out.push('{');
                    self.write_json_nodes(children, ctx, out)?;
                    out.push('}');
                }
            }
        }
        Ok(())
    }

    fn value<'a>(&self, ctx: &'a RowContext, name: &str) -> Result<&'a Value> {
        ctx.get(name)
            .ok_or_else(|| anyhow!("no generated value for column `{}`", name))
    }

    /// Quote only when the text would otherwise break the record, and escape
    /// with the spec's escape character rather than by doubling quotes, which
    /// is what Doris's `escape` load property expects.
    fn write_cell(&self, cell: &str, out: &mut Vec<u8>) {
        let needs_quoting = cell.bytes().any(|byte| {
            byte == self.csv.delimiter || byte == self.csv.quote || byte == b'\n' || byte == b'\r'
        }) || cell.as_bytes().windows(self.csv.newline.len()).any(|window| window == self.csv.newline);
        if !needs_quoting {
            out.extend_from_slice(cell.as_bytes());
            return;
        }
        out.push(self.csv.quote);
        for byte in cell.bytes() {
            if byte == self.csv.quote || byte == self.csv.escape {
                out.push(self.csv.escape);
            }
            out.push(byte);
        }
        out.push(self.csv.quote);
    }

    fn write_csv_record<'a>(&self, out: &mut Vec<u8>, cells: impl Iterator<Item = &'a str>) {
        for (index, cell) in cells.enumerate() {
            if index > 0 {
                out.push(self.csv.delimiter);
            }
            self.write_cell(cell, out);
        }
        out.extend_from_slice(&self.csv.newline);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Columns by name; a trailing `!` marks one quoted in JSON, which is
    /// what the Doris side does for LARGEINT and wide DECIMAL.
    fn columns(names: &str) -> Vec<OutputColumn> {
        names
            .split(',')
            .map(|name| {
                let name = name.trim();
                match name.strip_suffix('!') {
                    Some(quoted) => OutputColumn { name: quoted.to_string(), quote_in_json: true },
                    None => OutputColumn::new(name),
                }
            })
            .collect()
    }

    fn csv_settings() -> CsvSpecRuntime {
        CsvSpecRuntime {
            delimiter: b',',
            quote: b'"',
            escape: b'\\',
            newline: b"\n".to_vec(),
            null: "\\N".to_string(),
        }
    }

    fn row(pairs: Vec<(&str, Value)>) -> RowContext {
        pairs.into_iter().map(|(name, value)| (name.to_string(), value)).collect()
    }

    #[test]
    fn csv_quotes_separators_and_marks_nulls() {
        let columns = columns("a, b, c");
        let encoder = TextEncoder::new(TextFormat::Csv, &columns, csv_settings());
        let mut out = Vec::new();
        encoder
            .encode_row(
                &row(vec![
                    ("a", Value::String("has,comma and \"quote\"".into())),
                    ("b", Value::Null),
                    ("c", Value::String("line\nbreak".into())),
                ]),
                &mut out,
            )
            .unwrap();
        let text = String::from_utf8(out).unwrap();
        assert!(text.starts_with("\"has,comma and \\\"quote\\\"\","), "{}", text);
        assert!(text.contains(",\\N,"), "the null marker must go in unquoted: {}", text);
        assert!(text.contains("\"line\nbreak\""), "{}", text);
        assert!(text.ends_with('\n'));

        let header = encoder.header().unwrap().unwrap();
        assert_eq!(String::from_utf8(header).unwrap(), "a,b,c\n");
    }

    #[test]
    fn json_writes_one_object_per_line_with_nested_values() {
        let columns = columns("id, tags, at, amount, big!");
        let encoder = TextEncoder::new(TextFormat::Json, &columns, csv_settings());
        let mut out = Vec::new();
        encoder
            .encode_row(
                &row(vec![
                    ("id", Value::I64(7)),
                    ("tags", Value::List(vec![Value::I64(1), Value::Null])),
                    (
                        "at",
                        Value::Timestamp { micros: 1_000_000, format: "%Y-%m-%d %H:%M:%S".into() },
                    ),
                    ("amount", Value::Decimal { units: 1234, scale: 2 }),
                    ("big", Value::I128(170141183460469231731687303715884105727)),
                ]),
                &mut out,
            )
            .unwrap();
        let line = String::from_utf8(out).unwrap();
        assert!(line.ends_with('\n'));
        let parsed: serde_json::Value = serde_json::from_str(line.trim_end()).expect("valid JSON");
        assert_eq!(parsed["id"], 7);
        assert_eq!(parsed["tags"][0], 1);
        assert!(parsed["tags"][1].is_null());
        // Timestamps keep the spec's format; decimals keep exact digits.
        assert_eq!(parsed["at"], "1970-01-01 00:00:01");
        assert_eq!(parsed["amount"].to_string(), "12.34", "a narrow decimal stays a number");
        // A LARGEINT is quoted: as a bare number a JSON parser using doubles
        // would round it, and this value has 39 digits.
        assert_eq!(parsed["big"], "170141183460469231731687303715884105727");
        assert!(
            line.contains("\"big\":\"170141183460469231731687303715884105727\""),
            "{}",
            line
        );
        assert_eq!(encoder.header().unwrap(), None, "JSON has no header");
    }

    #[test]
    fn json_nests_dotted_columns_and_csv_keeps_them_flat() {
        let columns = columns("id, customer.name, customer.email, age");
        let ctx = row(vec![
            ("id", Value::I64(1)),
            ("customer.name", Value::String("Ann".into())),
            ("customer.email", Value::Null),
            ("age", Value::I64(30)),
        ]);
        let encoder = TextEncoder::new(TextFormat::Json, &columns, csv_settings());
        let mut out = Vec::new();
        encoder.encode_row(&ctx, &mut out).unwrap();
        assert_eq!(
            String::from_utf8(out).unwrap().trim_end(),
            r#"{"id":1,"customer":{"name":"Ann","email":null},"age":30}"#
        );

        let encoder = TextEncoder::new(TextFormat::Csv, &columns, csv_settings());
        assert_eq!(
            String::from_utf8(encoder.header().unwrap().unwrap()).unwrap(),
            "id,customer.name,customer.email,age\n"
        );
        let mut out = Vec::new();
        encoder.encode_row(&ctx, &mut out).unwrap();
        assert_eq!(String::from_utf8(out).unwrap(), "1,Ann,\\N,30\n");
    }

    #[test]
    fn a_missing_column_is_an_error_in_both_formats() {
        let columns = columns("a, b");
        for format in [TextFormat::Csv, TextFormat::Json] {
            let encoder = TextEncoder::new(format, &columns, csv_settings());
            let error = encoder
                .encode_row(&row(vec![("a", Value::I64(1))]), &mut Vec::new())
                .expect_err("column b is missing");
            assert!(error.to_string().contains('b'), "{}", error);
        }
    }
}
