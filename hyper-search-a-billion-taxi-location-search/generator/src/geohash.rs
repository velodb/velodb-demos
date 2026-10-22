//! Minimal geohash encoder (base32, standard alphabet). No external deps.

const BASE32: &[u8; 32] = b"0123456789bcdefghjkmnpqrstuvwxyz";

/// Encode `lat`/`lon` into `out.len()` geohash characters.
pub fn encode(lat: f64, lon: f64, out: &mut [u8]) {
    let (mut lat_lo, mut lat_hi) = (-90.0f64, 90.0f64);
    let (mut lon_lo, mut lon_hi) = (-180.0f64, 180.0f64);
    let mut even = true;
    let mut bit = 0u32;
    let mut ch = 0usize;
    let mut i = 0usize;
    while i < out.len() {
        if even {
            let mid = (lon_lo + lon_hi) / 2.0;
            if lon >= mid {
                ch |= 1 << (4 - bit);
                lon_lo = mid;
            } else {
                lon_hi = mid;
            }
        } else {
            let mid = (lat_lo + lat_hi) / 2.0;
            if lat >= mid {
                ch |= 1 << (4 - bit);
                lat_lo = mid;
            } else {
                lat_hi = mid;
            }
        }
        even = !even;
        if bit < 4 {
            bit += 1;
        } else {
            out[i] = BASE32[ch];
            i += 1;
            bit = 0;
            ch = 0;
        }
    }
}

#[allow(dead_code)]
pub fn encode_string(lat: f64, lon: f64, precision: usize) -> String {
    let mut buf = vec![0u8; precision];
    encode(lat, lon, &mut buf);
    String::from_utf8(buf).expect("base32 is ascii")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wikipedia_example() {
        // https://en.wikipedia.org/wiki/Geohash : (42.605, -5.603) -> ezs42
        assert_eq!(encode_string(42.605, -5.603, 5), "ezs42");
    }

    #[test]
    fn singapore_prefix() {
        // Central Singapore geohashes start with "w21z".
        let g = encode_string(1.2840, 103.8515, 6);
        assert!(g.starts_with("w21z"), "got {g}");
        assert_eq!(g.len(), 6);
    }
}
