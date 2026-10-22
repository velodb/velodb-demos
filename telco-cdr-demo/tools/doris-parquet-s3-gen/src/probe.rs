//! Startup validation: does the spec fit the schema?
//!
//! Each generator reports the most extreme values it can produce: range
//! bounds, the longest possible text, every choice, and null if it has a
//! null rate. Each of those goes through the real Parquet conversion for its
//! column, the same code every generated row goes through. A spec that can
//! produce something the column cannot hold therefore fails before the first
//! row, not an hour into a run when a rare value finally turns up.
//!
//! Templates and JavaScript produce text nothing can predict, so only their
//! null rate is checked; their values are still checked row by row.

use anyhow::{bail, Result};

use crate::parquet_out;
use crate::schema::Column;
use datagen::CompiledSpec;

/// Check every schema column's generator against the column's type.
pub fn validate_against_schema(spec: &CompiledSpec, columns: &[Column]) -> Result<()> {
    let mut problems = Vec::new();
    for column in columns {
        let Some(field) = spec.fields.iter().find(|field| field.name == column.name) else {
            continue;
        };
        for value in field.generator.probe_values() {
            // The writer's errors already quote the offending value.
            if let Err(error) = parquet_out::probe(column, &value) {
                problems.push(format!("  `{}` {}: {:#}", column.name, column.ty.sql_name(), error));
                // One problem per column says enough to fix it.
                break;
            }
        }
    }
    if !problems.is_empty() {
        bail!(
            "the spec can produce values these columns cannot hold:\n{}\n\
             Fix the generators above, or the columns they target.",
            problems.join("\n")
        );
    }
    Ok(())
}



