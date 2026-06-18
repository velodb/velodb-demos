//! Clickstream event schema, ported from
//! `backend/internal/datagen/service/clickstream_service.go` (`ClickstreamEvent`).
//!
//! `event_timestamp` is stamped at generation time and doubles as the **origin
//! timestamp** for the event-to-queryable latency measurement: compare it to the
//! row's ingest time in VeloDB. Keep the JSON shape identical to the Go producer
//! and to the target VeloDB table so the Stream Load path is byte-for-byte
//! comparable on both brokers.

use chrono::Utc;
use rand::Rng;
use serde::Serialize;
use std::collections::BTreeMap;

#[derive(Debug, Serialize)]
pub struct ClickstreamEvent {
    pub event_id: String,
    pub session_id: String,
    pub partner_id: i32,
    pub user_id: i32,
    pub event_type: String,
    /// RFC3339, UTC. Origin stamp for latency. Set at generation, never reused.
    pub event_timestamp: String,
    pub page_url: String,
    pub device_info: BTreeMap<String, String>,
    pub event_properties: BTreeMap<String, serde_json::Value>,
    pub utm_params: BTreeMap<String, String>,
}

// Weighted event mix, roughly matching the funnel in the Go service
// (view-heavy, few purchases).
const EVENT_TYPES: &[(&str, u32)] = &[
    ("page_view", 70),
    ("add_to_cart", 20),
    ("purchase", 7),
    ("remove_from_cart", 3),
];
const PARTNER_IDS: &[i32] = &[101, 102, 103, 104];
const PAGES: &[&str] = &[
    "/", "/products", "/products/detail", "/cart", "/checkout", "/search",
];
const BROWSERS: &[&str] = &["Chrome", "Safari", "Firefox", "Edge"];
const OSES: &[&str] = &["macOS", "Windows", "iOS", "Android", "Linux"];
const DEVICES: &[&str] = &["desktop", "mobile", "tablet"];
const UTM_SOURCES: &[&str] = &["google", "meta", "newsletter", "direct"];

/// Generate one event with a freshly stamped `event_timestamp`.
pub fn generate() -> ClickstreamEvent {
    let mut rng = rand::thread_rng();
    let event_type = weighted_event_type(&mut rng);
    let partner_id = *PARTNER_IDS.choose(&mut rng);
    let user_id = rng.gen_range(1..=500_000);

    let mut device_info = BTreeMap::new();
    device_info.insert("browser".into(), pick(&mut rng, BROWSERS).into());
    device_info.insert("os".into(), pick(&mut rng, OSES).into());
    device_info.insert("device".into(), pick(&mut rng, DEVICES).into());

    let mut utm_params = BTreeMap::new();
    utm_params.insert("utm_source".into(), pick(&mut rng, UTM_SOURCES).into());
    utm_params.insert("utm_medium".into(), "cpc".into());
    utm_params.insert("utm_campaign".into(), format!("camp_{}", rng.gen_range(1..=20)));

    let mut event_properties = BTreeMap::new();
    event_properties.insert("product_id".into(), rng.gen_range(1..=10_000).into());
    event_properties.insert(
        "price".into(),
        ((rng.gen_range(199..=29999) as f64) / 100.0).into(),
    );
    event_properties.insert("quantity".into(), rng.gen_range(1..=5).into());

    ClickstreamEvent {
        event_id: hex_id(&mut rng),
        session_id: hex_id(&mut rng),
        partner_id,
        user_id,
        event_type: event_type.into(),
        event_timestamp: Utc::now().to_rfc3339(),
        page_url: format!("https://shop.example.com{}", pick(&mut rng, PAGES)),
        device_info,
        event_properties,
        utm_params,
    }
}

fn weighted_event_type(rng: &mut impl Rng) -> &'static str {
    let total: u32 = EVENT_TYPES.iter().map(|(_, w)| w).sum();
    let mut roll = rng.gen_range(0..total);
    for (name, weight) in EVENT_TYPES {
        if roll < *weight {
            return name;
        }
        roll -= *weight;
    }
    EVENT_TYPES[0].0
}

fn hex_id(rng: &mut impl Rng) -> String {
    format!("{:032x}", rng.gen::<u128>())
}

fn pick<'a>(rng: &mut impl Rng, items: &'a [&'a str]) -> &'a str {
    items[rng.gen_range(0..items.len())]
}

// Small local helper so we don't pull `rand::seq::SliceRandom` into scope just
// for one integer pick.
trait ChooseExt<T> {
    fn choose(&self, rng: &mut impl Rng) -> &T;
}
impl<T> ChooseExt<T> for [T] {
    fn choose(&self, rng: &mut impl Rng) -> &T {
        &self[rng.gen_range(0..self.len())]
    }
}
