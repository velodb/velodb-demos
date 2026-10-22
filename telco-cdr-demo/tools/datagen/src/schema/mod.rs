//! Schema-driven encoders: the user supplies an Avro `.avsc` or a Protobuf
//! `.proto`, and the generated row is shaped and typed to match it.

pub mod arrow;
pub mod avro;
pub mod proto;
pub mod tree;

pub use avro::AvroEncoder;
pub use proto::ProtoEncoder;
pub use tree::{Node, RowTree};

/// Minimal big-endian two's complement bytes of an integer, as Avro's
/// decimal logical type wants them.
pub(crate) fn i128_to_signed_be_bytes(value: i128) -> Vec<u8> {
    let bytes = value.to_be_bytes();
    let mut start = 0;
    while start + 1 < bytes.len() {
        let redundant = (bytes[start] == 0x00 && bytes[start + 1] < 0x80)
            || (bytes[start] == 0xFF && bytes[start + 1] >= 0x80);
        if !redundant {
            break;
        }
        start += 1;
    }
    bytes[start..].to_vec()
}

/// Rescale fixed-point units from `from` to `to` fractional digits. Going
/// down loses digits, so that is refused rather than rounded.
pub(crate) fn rescale_units(units: i128, from: u32, to: u32) -> anyhow::Result<i128> {
    if to >= from {
        units
            .checked_mul(10i128.pow(to - from))
            .ok_or_else(|| anyhow::anyhow!("decimal overflows 128 bits at scale {}", to))
    } else {
        let divisor = 10i128.pow(from - to);
        if units % divisor != 0 {
            anyhow::bail!(
                "decimal with scale {} does not fit a schema scale of {} without losing digits",
                from,
                to
            );
        }
        Ok(units / divisor)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn signed_bytes_are_minimal() {
        assert_eq!(i128_to_signed_be_bytes(0), vec![0]);
        assert_eq!(i128_to_signed_be_bytes(127), vec![127]);
        assert_eq!(i128_to_signed_be_bytes(128), vec![0, 128]);
        assert_eq!(i128_to_signed_be_bytes(-1), vec![0xFF]);
        assert_eq!(i128_to_signed_be_bytes(-128), vec![0x80]);
        assert_eq!(i128_to_signed_be_bytes(-129), vec![0xFF, 0x7F]);
        assert_eq!(i128_to_signed_be_bytes(4512), vec![0x11, 0xA0]);
    }

    #[test]
    fn rescaling_keeps_digits_or_refuses() {
        assert_eq!(rescale_units(4512, 2, 4).unwrap(), 451200);
        assert_eq!(rescale_units(451200, 4, 2).unwrap(), 4512);
        assert!(rescale_units(4512, 2, 1).is_err());
    }
}
