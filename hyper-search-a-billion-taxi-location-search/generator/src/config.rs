//! Generator configuration: generator.toml + CLI overrides. Every field has a default,
//! so the tool runs with no config file at all.

use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Config {
    /// Total rows across all files (exact).
    pub rows: u64,
    /// Date span in days. File N covers offsets [-(N+1) days, -N days).
    pub days: u32,
    /// Fleet size; taxi_id in [0, taxis). Plate is derived deterministically from taxi_id.
    pub taxis: u32,
    /// Plate prefix (Singapore taxis: "SH"). Plate = prefix + letter + 4 digits + check letter.
    pub plate_prefix: String,
    /// Seconds between pings while a taxi is on shift.
    pub ping_secs: u32,
    /// +/- jitter applied to each ping interval, in seconds.
    pub ping_jitter_secs: u32,
    /// Shift length range, in minutes. Rows are clustered into shifts so trajectories are contiguous.
    pub shift_min_minutes: u32,
    pub shift_max_minutes: u32,
    /// RNG seed; same seed + same config => byte-identical output.
    pub seed: u64,
    /// Output directory for day_NNN.parquet, state.json, manifest.json.
    pub out: PathBuf,
    /// Target rows per parquet file. Each day is split into ceil(rows_per_day / rows_per_file)
    /// chunks; this bounds per-worker memory (~60 B/row) independent of `days`.
    pub rows_per_file: u64,
    /// Parquet row group size.
    pub row_group: usize,
    /// ZSTD level (1-22). 3 is a good speed/size tradeoff.
    pub zstd_level: i32,
    /// Worker threads (0 = all cores).
    pub threads: usize,
    /// Company name -> weight, assigned per taxi.
    pub companies: Vec<Company>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Company {
    pub name: String,
    pub weight: f64,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            rows: 1_000_000_000,
            days: 500,
            taxis: 100_000,
            plate_prefix: "SH".to_string(),
            ping_secs: 30,
            ping_jitter_secs: 3,
            shift_min_minutes: 60,
            shift_max_minutes: 240,
            seed: 42,
            out: PathBuf::from("data"),
            rows_per_file: 2_000_000,
            row_group: 1_000_000,
            zstd_level: 3,
            threads: 0,
            companies: vec![
                Company { name: "ComfortDelGro".into(), weight: 45.0 },
                Company { name: "SMRT".into(), weight: 15.0 },
                Company { name: "Trans-Cab".into(), weight: 15.0 },
                Company { name: "Premier".into(), weight: 10.0 },
                Company { name: "Prime".into(), weight: 8.0 },
                Company { name: "Strides".into(), weight: 7.0 },
            ],
        }
    }
}

impl Config {
    pub fn validate(&self) -> anyhow::Result<()> {
        anyhow::ensure!(self.rows > 0, "rows must be > 0");
        anyhow::ensure!(self.days > 0, "days must be > 0");
        anyhow::ensure!(self.taxis > 0 && self.taxis <= 260_000, "taxis must be in 1..=260000 (plate space)");
        anyhow::ensure!(self.ping_secs > 0, "ping_secs must be > 0");
        anyhow::ensure!(self.ping_jitter_secs < self.ping_secs, "ping_jitter_secs must be < ping_secs");
        anyhow::ensure!(
            self.shift_min_minutes > 0 && self.shift_min_minutes <= self.shift_max_minutes,
            "shift_min_minutes must be in 1..=shift_max_minutes"
        );
        anyhow::ensure!(self.rows_per_file >= 100_000, "rows_per_file must be >= 100000");
        anyhow::ensure!(self.row_group >= 10_000, "row_group must be >= 10000");
        anyhow::ensure!((1..=22).contains(&self.zstd_level), "zstd_level must be 1..=22");
        anyhow::ensure!(!self.companies.is_empty(), "companies must not be empty");
        anyhow::ensure!(self.plate_prefix.is_ascii(), "plate_prefix must be ascii");
        Ok(())
    }

    pub fn rows_for_day(&self, day: u32) -> u64 {
        let base = self.rows / self.days as u64;
        let rem = self.rows % self.days as u64;
        base + if (day as u64) < rem { 1 } else { 0 }
    }

    pub fn ping_ms(&self) -> i64 {
        self.ping_secs as i64 * 1000
    }
    pub fn jitter_ms(&self) -> i64 {
        self.ping_jitter_secs as i64 * 1000
    }
    pub fn shift_min_ms(&self) -> i64 {
        self.shift_min_minutes as i64 * 60_000
    }
    pub fn shift_max_ms(&self) -> i64 {
        self.shift_max_minutes as i64 * 60_000
    }
}
