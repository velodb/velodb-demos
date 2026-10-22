//! Singapore MRT stations, embedded from ../../mrt_stations.csv (shared with the search CLI).
//! Coordinates are approximate (demo-grade); `weight` biases taxi destinations toward hubs.

use rand::Rng;
use std::sync::OnceLock;

const CSV: &str = include_str!("../../mrt_stations.csv");

#[derive(Debug, Clone)]
#[allow(dead_code)]
pub struct Station {
    pub name: String,
    pub lines: String,
    pub lat: f64,
    pub lon: f64,
    pub weight: f64,
}

pub struct Stations {
    pub list: Vec<Station>,
    cum: Vec<f64>,
    total: f64,
}

impl Stations {
    fn parse() -> Stations {
        let mut list = Vec::new();
        for (n, line) in CSV.lines().enumerate() {
            if n == 0 || line.trim().is_empty() {
                continue;
            }
            let f: Vec<&str> = line.split(',').collect();
            assert!(f.len() >= 5, "bad station line {}: {line}", n + 1);
            list.push(Station {
                name: f[0].to_string(),
                lines: f[1].to_string(),
                lat: f[2].parse().expect("lat"),
                lon: f[3].parse().expect("lon"),
                weight: f[4].parse().expect("weight"),
            });
        }
        let mut cum = Vec::with_capacity(list.len());
        let mut total = 0.0;
        for s in &list {
            total += s.weight;
            cum.push(total);
        }
        Stations { list, cum, total }
    }

    /// Weighted random station.
    pub fn sample<R: Rng>(&self, rng: &mut R) -> &Station {
        let r = rng.gen::<f64>() * self.total;
        let idx = self.cum.partition_point(|c| *c < r).min(self.list.len() - 1);
        &self.list[idx]
    }
}

pub fn all() -> &'static Stations {
    static S: OnceLock<Stations> = OnceLock::new();
    S.get_or_init(Stations::parse)
}
