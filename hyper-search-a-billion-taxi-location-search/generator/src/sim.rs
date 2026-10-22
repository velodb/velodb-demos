//! Taxi fleet simulation.
//!
//! Rows are clustered into *shifts*: a taxi comes online somewhere near an MRT station, drives
//! between stations (AVAILABLE -> ON_CALL -> HIRED -> AVAILABLE ...), pings every `ping_secs`,
//! and goes OFFLINE at the end of the shift.
//!
//! Work is split into *tasks* (chunks): each day has a deterministic shift schedule (seeded by
//! (seed, day)); the day is divided into `parts` equal time windows of ~`rows_per_file` rows,
//! and each task replays the shifts overlapping its window and emits only the pings inside it.
//! Chunks are therefore independent (parallel, resumable, regenerable) while trajectories stay
//! contiguous across chunk boundaries; shifts are only clipped at the day boundary.
//!
//! Timestamps are relative: `offset_ms <= 0` is milliseconds before the (future) load time.
//! Day 0 covers [-1 day, 0); shifts still running at offset 0 are exported to state.json so a
//! realtime feeder can continue them seamlessly.

use crate::config::Config;
use crate::stations;
use rand::seq::SliceRandom;
use std::sync::atomic::{AtomicU64, Ordering};
use rand::{Rng, SeedableRng};
use rand_xoshiro::Xoshiro256PlusPlus;
use serde::{Deserialize, Serialize};

pub const DAY_MS: i64 = 86_400_000;

// Singapore bounding box (taxis are clamped inside it).
const LAT_MIN: f64 = 1.23;
const LAT_MAX: f64 = 1.47;
const LON_MIN: f64 = 103.60;
const LON_MAX: f64 = 104.04;
const KM_PER_DEG_LAT: f64 = 111.32;
const KM_PER_DEG_LON: f64 = 111.29; // 111.32 * cos(1.35 deg)

#[derive(Clone, Copy, PartialEq, Eq, Debug, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Status {
    Available,
    OnCall,
    Hired,
    Offline,
}

impl Status {
    pub fn as_str(self) -> &'static str {
        match self {
            Status::Available => "AVAILABLE",
            Status::OnCall => "ON_CALL",
            Status::Hired => "HIRED",
            Status::Offline => "OFFLINE",
        }
    }
}

/// One row of output (strings are derived later from taxi_id / lat / lon).
#[derive(Clone, Copy, Debug)]
pub struct Ping {
    pub offset_ms: i64,
    pub taxi_id: u32,
    pub lat: f64,
    pub lon: f64,
    pub speed_kmh: u16,
    pub heading: u16,
    pub status: Status,
}

/// Per-taxi static attributes, derived deterministically from the config.
pub struct Fleet {
    pub plates: Vec<String>,
    pub company_idx: Vec<u8>,
    pub company_names: Vec<String>,
}

impl Fleet {
    pub fn new(cfg: &Config) -> Fleet {
        let mut rng = Xoshiro256PlusPlus::seed_from_u64(cfg.seed ^ 0xF1EE7);
        let total: f64 = cfg.companies.iter().map(|c| c.weight).sum();
        let mut plates = Vec::with_capacity(cfg.taxis as usize);
        let mut company_idx = Vec::with_capacity(cfg.taxis as usize);
        for id in 0..cfg.taxis {
            plates.push(plate_for(&cfg.plate_prefix, id));
            let r = rng.gen::<f64>() * total;
            let mut acc = 0.0;
            let mut idx = cfg.companies.len() - 1;
            for (i, c) in cfg.companies.iter().enumerate() {
                acc += c.weight;
                if r < acc {
                    idx = i;
                    break;
                }
            }
            company_idx.push(idx as u8);
        }
        Fleet {
            plates,
            company_idx,
            company_names: cfg.companies.iter().map(|c| c.name.clone()).collect(),
        }
    }

    pub fn company(&self, taxi_id: u32) -> &str {
        &self.company_names[self.company_idx[taxi_id as usize] as usize]
    }
}

/// Singapore-style plate: PREFIX + series letter + 4 digits + check letter, unique per taxi_id.
pub fn plate_for(prefix: &str, id: u32) -> String {
    let series = (b'A' + ((id / 10_000) % 26) as u8) as char;
    let num = id % 10_000;
    let check = (b'A' + ((id.wrapping_mul(7) + 3) % 26) as u8) as char;
    format!("{prefix}{series}{num:04}{check}")
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Vehicle {
    pub lat: f64,
    pub lon: f64,
    pub heading: f64,
    pub speed_kmh: f64,
    pub status: Status,
    pub dest_lat: f64,
    pub dest_lon: f64,
    /// Remaining ticks to sit still (pickup / drop-off / waiting).
    pub dwell: u32,
}

fn gauss<R: Rng>(rng: &mut R, sigma: f64) -> f64 {
    let u1: f64 = rng.gen::<f64>().max(1e-12);
    let u2: f64 = rng.gen();
    (-2.0 * u1.ln()).sqrt() * (std::f64::consts::TAU * u2).cos() * sigma
}

fn clamp_pos(lat: &mut f64, lon: &mut f64) {
    *lat = lat.clamp(LAT_MIN, LAT_MAX);
    *lon = lon.clamp(LON_MIN, LON_MAX);
}

impl Vehicle {
    pub fn spawn<R: Rng>(rng: &mut R) -> Vehicle {
        let st = stations::all().sample(rng);
        let mut lat = st.lat + gauss(rng, 0.006);
        let mut lon = st.lon + gauss(rng, 0.006);
        clamp_pos(&mut lat, &mut lon);
        let (dest_lat, dest_lon) = station_dest(rng);
        Vehicle {
            lat,
            lon,
            heading: rng.gen_range(0.0..360.0),
            speed_kmh: rng.gen_range(20.0..50.0),
            status: Status::Available,
            dest_lat,
            dest_lon,
            dwell: 0,
        }
    }

    /// Advance the vehicle by `dt_s` seconds.
    pub fn step<R: Rng>(&mut self, rng: &mut R, dt_s: f64) {
        if self.dwell > 0 {
            self.dwell -= 1;
            self.speed_kmh = 0.0;
            return;
        }
        let dlat_km = (self.dest_lat - self.lat) * KM_PER_DEG_LAT;
        let dlon_km = (self.dest_lon - self.lon) * KM_PER_DEG_LON;
        let dist_km = dlat_km.hypot(dlon_km);
        let bearing = dlon_km.atan2(dlat_km).to_degrees().rem_euclid(360.0);
        let mut diff = (bearing - self.heading + 540.0).rem_euclid(360.0) - 180.0;
        diff = diff.clamp(-60.0, 60.0) + gauss(rng, 6.0);
        self.heading = (self.heading + diff).rem_euclid(360.0);
        self.speed_kmh = (self.speed_kmh + rng.gen_range(-10.0..10.0)).clamp(12.0, 75.0);
        let step_km = self.speed_kmh * dt_s / 3600.0;
        if step_km >= dist_km {
            self.lat = self.dest_lat;
            self.lon = self.dest_lon;
            self.arrive(rng);
        } else {
            let h = self.heading.to_radians();
            self.lat += step_km * h.cos() / KM_PER_DEG_LAT;
            self.lon += step_km * h.sin() / KM_PER_DEG_LON;
        }
        clamp_pos(&mut self.lat, &mut self.lon);
    }

    fn arrive<R: Rng>(&mut self, rng: &mut R) {
        match self.status {
            Status::Available => {
                if rng.gen_bool(0.7) {
                    // Got a booking nearby: head to the pickup point.
                    self.status = Status::OnCall;
                    let (a, b) = self.nearby(rng, 1.5);
                    self.dest_lat = a;
                    self.dest_lon = b;
                    self.dwell = rng.gen_range(0..=2);
                } else {
                    let (a, b) = station_dest(rng);
                    self.dest_lat = a;
                    self.dest_lon = b;
                }
            }
            Status::OnCall => {
                // Passenger boarded.
                self.status = Status::Hired;
                self.dwell = rng.gen_range(1..=3);
                let (a, b) = station_dest(rng);
                self.dest_lat = a;
                self.dest_lon = b;
            }
            Status::Hired => {
                // Drop-off.
                self.status = Status::Available;
                self.dwell = rng.gen_range(1..=4);
                let (a, b) = if rng.gen_bool(0.5) { self.nearby(rng, 3.0) } else { station_dest(rng) };
                self.dest_lat = a;
                self.dest_lon = b;
            }
            Status::Offline => {}
        }
    }

    fn nearby<R: Rng>(&self, rng: &mut R, radius_km: f64) -> (f64, f64) {
        let mut lat = self.lat + rng.gen_range(-radius_km..radius_km) / KM_PER_DEG_LAT;
        let mut lon = self.lon + rng.gen_range(-radius_km..radius_km) / KM_PER_DEG_LON;
        clamp_pos(&mut lat, &mut lon);
        (lat, lon)
    }
}

fn station_dest<R: Rng>(rng: &mut R) -> (f64, f64) {
    let st = stations::all().sample(rng);
    let mut lat = st.lat + gauss(rng, 0.004);
    let mut lon = st.lon + gauss(rng, 0.004);
    clamp_pos(&mut lat, &mut lon);
    (lat, lon)
}

/// A shift still running at offset 0 (end of day 0); exported for the realtime feeder.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ActiveTaxi {
    pub taxi_id: u32,
    pub plate: String,
    pub company: String,
    /// Offset of the last ping written to the parquet files (< 0).
    pub last_ping_offset_ms: i64,
    /// When this shift ends (> 0, i.e. in the future relative to load time).
    pub shift_end_offset_ms: i64,
    pub vehicle: Vehicle,
}

fn mix(mut z: u64) -> u64 {
    // splitmix64 finalizer
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    z ^ (z >> 31)
}

pub fn day_seed(seed: u64, day: u32) -> u64 {
    mix(seed ^ (day as u64 + 1).wrapping_mul(0x9E37_79B9_7F4A_7C15))
}

/// One shift in a day's schedule. Ping times and movement are regenerated from `seed`.
#[derive(Clone, Copy, Debug)]
pub struct Shift {
    pub taxi_id: u32,
    pub start_ms: i64,
    /// Natural end of the shift (may exceed the day window: clipped, or run past offset 0).
    pub natural_end_ms: i64,
    /// Number of pings actually emitted (after day-clipping and quota truncation).
    pub n_pings: u32,
    /// True when the shift ends naturally inside the day => last ping is OFFLINE.
    pub ends_naturally: bool,
    pub seed: u64,
}

/// Deterministic ping times of a shift; `limit_ms` is the day window end.
fn count_pings(seed: u64, start_ms: i64, natural_end_ms: i64, limit_ms: i64, cfg: &Config) -> u32 {
    let end = natural_end_ms.min(limit_ms);
    let mut rng = Xoshiro256PlusPlus::seed_from_u64(seed ^ 0x7111);
    let mut t = start_ms;
    let mut n = 1u32;
    loop {
        let next = t + cfg.ping_ms() + rng.gen_range(-cfg.jitter_ms()..=cfg.jitter_ms());
        if next >= end {
            return n;
        }
        t = next;
        n += 1;
    }
}

pub fn day_window(day: u32) -> (i64, i64) {
    let end = -(day as i64) * DAY_MS;
    (end - DAY_MS, end)
}

/// Build the shift schedule of `day` so that the pings sum to exactly `quota`.
pub fn day_schedule(day: u32, quota: u64, cfg: &Config) -> Vec<Shift> {
    let mut rng = Xoshiro256PlusPlus::seed_from_u64(day_seed(cfg.seed, day));
    let (window_start, window_end) = day_window(day);
    let mut perm: Vec<u32> = (0..cfg.taxis).collect();
    perm.shuffle(&mut rng);
    let mut pi = 0usize;
    let mut shifts = Vec::new();
    let mut total = 0u64;
    while total < quota {
        if pi >= perm.len() {
            perm.shuffle(&mut rng);
            pi = 0;
        }
        let taxi_id = perm[pi];
        pi += 1;
        let start_ms = rng.gen_range(window_start..window_end);
        let natural_end_ms = start_ms + rng.gen_range(cfg.shift_min_ms()..=cfg.shift_max_ms());
        let seed = rng.gen::<u64>();
        let mut n = count_pings(seed, start_ms, natural_end_ms, window_end, cfg) as u64;
        let mut ends_naturally = natural_end_ms <= window_end;
        if total + n > quota {
            n = quota - total;
            ends_naturally = false;
        }
        total += n;
        shifts.push(Shift { taxi_id, start_ms, natural_end_ms, n_pings: n as u32, ends_naturally, seed });
    }
    shifts
}

/// A unit of work: one parquet file covering `[window_start, window_end)` of `day`.
#[derive(Clone, Debug, Serialize)]
pub struct Task {
    pub index: usize,
    pub day: u32,
    pub part: u32,
    pub parts: u32,
    pub window_start_ms: i64,
    pub window_end_ms: i64,
    /// Expected rows (uniform estimate; the actual count is exact per day, approximate per part).
    pub rows_est: u64,
}

impl Task {
    pub fn file_name(&self) -> String {
        format!("day_{:03}_{:02}.parquet", self.day, self.part)
    }
}

/// Global task list: every day split into `ceil(rows_day / rows_per_file)` equal time windows.
pub fn tasks(cfg: &Config) -> Vec<Task> {
    let mut out = Vec::new();
    for day in 0..cfg.days {
        let rows_day = cfg.rows_for_day(day);
        let parts = rows_day.div_ceil(cfg.rows_per_file).max(1) as u32;
        let (ds, de) = day_window(day);
        for part in 0..parts {
            let ws = ds + (DAY_MS * part as i64) / parts as i64;
            let we = if part + 1 == parts { de } else { ds + (DAY_MS * (part as i64 + 1)) / parts as i64 };
            out.push(Task {
                index: out.len(),
                day,
                part,
                parts,
                window_start_ms: ws,
                window_end_ms: we,
                rows_est: rows_day / parts as u64,
            });
        }
    }
    out
}

pub struct TaskResult {
    pub pings: Vec<Ping>,
    pub active: Vec<ActiveTaxi>,
    /// Shifts overlapping this window.
    pub shifts: u32,
}

/// Generate the pings of `task`. `progress` is bumped as rows are produced.
pub fn generate_task(task: &Task, schedule: &[Shift], cfg: &Config, fleet: &Fleet, progress: &AtomicU64) -> TaskResult {
    let (ws, we) = (task.window_start_ms, task.window_end_ms);
    let day_end = day_window(task.day).1;
    let mut pings: Vec<Ping> = Vec::with_capacity(task.rows_est as usize + task.rows_est as usize / 8);
    let mut active = Vec::new();
    let mut shifts = 0u32;
    let mut since_report = 0u64;

    for sh in schedule {
        // Quick reject: shift entirely before/after the window.
        if sh.start_ms >= we {
            continue;
        }
        let shift_end = sh.natural_end_ms.min(day_end);
        if shift_end < ws {
            continue;
        }
        shifts += 1;

        let mut trng = Xoshiro256PlusPlus::seed_from_u64(sh.seed ^ 0x7111); // ping timing
        let mut mrng = Xoshiro256PlusPlus::seed_from_u64(sh.seed ^ 0x3A3A); // movement
        let mut v = Vehicle::spawn(&mut mrng);
        let mut t = sh.start_ms;
        let n = sh.n_pings;
        for i in 0..n {
            let last = i + 1 == n;
            if t >= we {
                break;
            }
            if t >= ws {
                let status = if last && sh.ends_naturally { Status::Offline } else { v.status };
                pings.push(Ping {
                    offset_ms: t,
                    taxi_id: sh.taxi_id,
                    lat: v.lat,
                    lon: v.lon,
                    speed_kmh: v.speed_kmh.round() as u16,
                    heading: v.heading.rem_euclid(360.0) as u16,
                    status,
                });
                since_report += 1;
                if since_report >= 65_536 {
                    progress.fetch_add(since_report, Ordering::Relaxed);
                    since_report = 0;
                }
                if last && task.day == 0 && sh.natural_end_ms > 0 {
                    active.push(ActiveTaxi {
                        taxi_id: sh.taxi_id,
                        plate: fleet.plates[sh.taxi_id as usize].clone(),
                        company: fleet.company(sh.taxi_id).to_string(),
                        last_ping_offset_ms: t,
                        shift_end_offset_ms: sh.natural_end_ms,
                        vehicle: v.clone(),
                    });
                }
            }
            if last {
                break;
            }
            let next = t + cfg.ping_ms() + trng.gen_range(-cfg.jitter_ms()..=cfg.jitter_ms());
            v.step(&mut mrng, (next - t) as f64 / 1000.0);
            t = next;
        }
    }
    progress.fetch_add(since_report, Ordering::Relaxed);
    pings.sort_unstable_by_key(|p| (p.offset_ms, p.taxi_id));
    TaskResult { pings, active, shifts }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn plates_unique() {
        let mut set = std::collections::HashSet::new();
        for id in 0..100_000u32 {
            assert!(set.insert(plate_for("SH", id)));
        }
        assert_eq!(plate_for("SH", 0), "SHA0000D");
    }

    fn run(cfg: &Config) -> Vec<(Task, TaskResult)> {
        let fleet = Fleet::new(cfg);
        let progress = AtomicU64::new(0);
        tasks(cfg)
            .into_iter()
            .map(|t| {
                let sched = day_schedule(t.day, cfg.rows_for_day(t.day), cfg);
                let r = generate_task(&t, &sched, cfg, &fleet, &progress);
                (t, r)
            })
            .collect()
    }

    #[test]
    fn exact_quota_sorted_and_split() {
        // 3 days x 40k rows, 15k rows per file => 3 parts per day, 9 tasks.
        let cfg = Config { rows: 120_000, days: 3, taxis: 500, rows_per_file: 15_000, ..Config::default() };
        let res = run(&cfg);
        assert_eq!(res.len(), 9);
        let total: usize = res.iter().map(|(_, r)| r.pings.len()).sum();
        assert_eq!(total, 120_000);
        for (t, r) in &res {
            assert!(r.pings.windows(2).all(|w| w[0].offset_ms <= w[1].offset_ms));
            assert!(r.pings.iter().all(|p| p.offset_ms >= t.window_start_ms && p.offset_ms < t.window_end_ms));
            assert!(r.pings.iter().all(|p| (LAT_MIN..=LAT_MAX).contains(&p.lat)));
        }
        // Per-day totals are exact.
        for day in 0..3 {
            let n: usize = res.iter().filter(|(t, _)| t.day == day).map(|(_, r)| r.pings.len()).sum();
            assert_eq!(n as u64, cfg.rows_for_day(day));
        }
        // A shift crossing a chunk boundary is continuous: consecutive pings of one taxi are < 1 km apart.
        let mut all: Vec<Ping> = res.iter().flat_map(|(_, r)| r.pings.iter().copied()).collect();
        all.sort_by_key(|p| (p.taxi_id, p.offset_ms));
        for w in all.windows(2) {
            if w[0].taxi_id == w[1].taxi_id && w[1].offset_ms - w[0].offset_ms < 60_000 {
                let d = ((w[1].lat - w[0].lat) * KM_PER_DEG_LAT).hypot((w[1].lon - w[0].lon) * KM_PER_DEG_LON);
                assert!(d < 1.0, "jump of {d} km");
            }
        }
        // day 0 exports active shifts.
        assert!(res.iter().filter(|(t, _)| t.day == 0).any(|(_, r)| !r.active.is_empty()));
    }

    #[test]
    fn deterministic() {
        let cfg = Config { rows: 30_000, days: 1, taxis: 200, rows_per_file: 10_000, ..Config::default() };
        let a = run(&cfg);
        let b = run(&cfg);
        for ((_, x), (_, y)) in a.iter().zip(b.iter()) {
            assert_eq!(x.pings.len(), y.pings.len());
            assert!(x.pings.iter().zip(y.pings.iter()).all(|(p, q)| p.offset_ms == q.offset_ms && p.lat == q.lat));
        }
    }
}
