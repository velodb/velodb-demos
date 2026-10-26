//! Size and duration parsing, shared by every binary in the crate.
//!
//! `src/bin/calcsize.rs` pulls this in by path rather than through a library
//! target, so `s3.rs` can reach it as `crate::units::...` from either crate
//! root.

use std::time::Duration;

pub fn parse_byte_size(value: &str) -> std::result::Result<u64, String> {
    let value = value.trim();
    let split = value
        .find(|c: char| c.is_ascii_alphabetic())
        .unwrap_or(value.len());
    let (num_str, unit_str) = value.split_at(split);
    let num: f64 = num_str
        .trim()
        .parse()
        .map_err(|_| format!("invalid byte size '{value}'"))?;
    if num < 0.0 {
        return Err(format!("byte size must be non-negative: '{value}'"));
    }
    let multiplier: f64 = match unit_str.trim().to_ascii_lowercase().as_str() {
        "" => 1.0,
        "k" | "kb" | "kib" => 1024.0,
        "m" | "mb" | "mib" => 1024.0 * 1024.0,
        "g" | "gb" | "gib" => 1024.0 * 1024.0 * 1024.0,
        // A tool whose job is multi-terabyte datasets should be able to say
        // so without counting gibibytes.
        "t" | "tb" | "tib" => 1024.0 * 1024.0 * 1024.0 * 1024.0,
        "p" | "pb" | "pib" => 1024.0 * 1024.0 * 1024.0 * 1024.0 * 1024.0,
        other => return Err(format!("unknown byte unit '{other}' in '{value}'")),
    };
    let bytes = (num * multiplier).ceil();
    if bytes > u64::MAX as f64 {
        return Err(format!("byte size overflow in '{value}'"));
    }
    Ok(bytes as u64)
}

pub fn parse_duration(value: &str) -> std::result::Result<Duration, String> {
    let mut total = 0u64;
    let mut number = String::new();
    for ch in value.chars() {
        if ch.is_ascii_digit() {
            number.push(ch);
            continue;
        }
        if number.is_empty() {
            return Err(format!("invalid duration '{value}'"));
        }
        let parsed = number
            .parse::<u64>()
            .map_err(|_| format!("invalid duration '{value}'"))?;
        number.clear();
        match ch {
            'h' => total = total.saturating_add(parsed.saturating_mul(3600)),
            'm' => total = total.saturating_add(parsed.saturating_mul(60)),
            's' => total = total.saturating_add(parsed),
            _ => return Err(format!("invalid duration unit '{ch}' in '{value}'")),
        }
    }
    if !number.is_empty() {
        total = total.saturating_add(
            number
                .parse::<u64>()
                .map_err(|_| format!("invalid duration '{value}'"))?,
        );
    }
    if total == 0 {
        return Err("duration must be greater than zero".into());
    }
    Ok(Duration::from_secs(total))
}

/// Bytes as a binary-prefixed figure: 1.83 TiB, 512.0 MiB, 940 B.
///
/// Three significant figures, because a total that moves by a gigabyte should
/// visibly move; `1.83 TiB` alone hides ten gigabytes of change.
pub fn format_bytes(value: u64) -> String {
    const UNITS: [(u64, &str); 5] = [
        (1u64 << 50, "PiB"),
        (1u64 << 40, "TiB"),
        (1u64 << 30, "GiB"),
        (1u64 << 20, "MiB"),
        (1u64 << 10, "KiB"),
    ];
    for (scale, suffix) in UNITS {
        if value >= scale {
            return format!("{:.2} {}", value as f64 / scale as f64, suffix);
        }
    }
    format!("{} B", value)
}

/// Digit grouping, so a count of objects is readable at a glance.
pub fn group_digits(value: u64) -> String {
    let digits = value.to_string();
    let mut out = String::with_capacity(digits.len() + digits.len() / 3);
    for (index, ch) in digits.chars().enumerate() {
        if index > 0 && (digits.len() - index) % 3 == 0 {
            out.push(',');
        }
        out.push(ch);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn formats_binary_sizes() {
        assert_eq!(format_bytes(0), "0 B");
        assert_eq!(format_bytes(940), "940 B");
        assert_eq!(format_bytes(1 << 20), "1.00 MiB");
        assert_eq!(format_bytes(3 << 40), "3.00 TiB");
        assert_eq!(format_bytes((1.83 * (1u64 << 40) as f64) as u64), "1.83 TiB");
    }

    #[test]
    fn groups_digits() {
        assert_eq!(group_digits(0), "0");
        assert_eq!(group_digits(999), "999");
        assert_eq!(group_digits(1000), "1,000");
        assert_eq!(group_digits(38520), "38,520");
        assert_eq!(group_digits(1234567890), "1,234,567,890");
    }
}
