//! Generators for Doris's nested and network types: ARRAY, MAP, STRUCT,
//! IPV4 and IPV6.
//!
//! The nested generators own child generators and run them against the same
//! row context, so an array element can be a template over other fields. A
//! nested generator's dependencies are the union of its children's, which is
//! what keeps the field graph ordering correct.

use std::collections::HashSet;
use std::net::{Ipv4Addr, Ipv6Addr};

use anyhow::{anyhow, bail, Result};
use rand::{rngs::StdRng, Rng};

use crate::compile::RowContext;
use crate::generators::{should_emit_null, FieldGenerator, Generator};
use crate::value::Value;

#[derive(Clone)]
pub struct ArrayGenerator {
    pub element: Box<Generator>,
    pub min_len: usize,
    pub max_len: usize,
    pub null_rate: Option<f64>,
    pub dependencies: Vec<String>,
}

impl FieldGenerator for ArrayGenerator {
    fn generate(&mut self, ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let len = rng.gen_range(self.min_len..=self.max_len);
        let mut items = Vec::with_capacity(len);
        for _ in 0..len {
            items.push(self.element.generate(ctx, rng)?);
        }
        Ok(Value::List(items))
    }
}

#[derive(Clone)]
pub struct MapGenerator {
    pub key: Box<Generator>,
    pub value: Box<Generator>,
    pub min_len: usize,
    pub max_len: usize,
    pub null_rate: Option<f64>,
    pub dependencies: Vec<String>,
}

/// Draws per wanted entry before settling for a shorter map. A key generator
/// with fewer distinct values than `max_len` cannot fill it, and should
/// produce a smaller map rather than spin.
const KEY_ATTEMPTS_PER_ENTRY: usize = 8;

impl FieldGenerator for MapGenerator {
    fn generate(&mut self, ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let len = rng.gen_range(self.min_len..=self.max_len);
        let mut entries = Vec::with_capacity(len);
        let mut seen = HashSet::with_capacity(len);
        let mut attempts = len * KEY_ATTEMPTS_PER_ENTRY;
        // Keys must be unique and never null, so draw until the map is full.
        while entries.len() < len && attempts > 0 {
            attempts -= 1;
            let key = self.key.generate(ctx, rng)?;
            if matches!(key, Value::Null) || !seen.insert(key.csv_string("")) {
                continue;
            }
            let value = self.value.generate(ctx, rng)?;
            entries.push((key, value));
        }
        Ok(Value::Map(entries))
    }
}

#[derive(Clone)]
pub struct StructGenerator {
    pub fields: Vec<(String, Generator)>,
    pub null_rate: Option<f64>,
    pub dependencies: Vec<String>,
}

impl FieldGenerator for StructGenerator {
    fn generate(&mut self, ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let mut fields = Vec::with_capacity(self.fields.len());
        for (name, generator) in self.fields.iter_mut() {
            fields.push((name.clone(), generator.generate(ctx, rng)?));
        }
        Ok(Value::Struct(fields))
    }
}

/// Addresses drawn uniformly from one network. Emitted as text, which is
/// what a template sees and what the Parquet writer validates.
#[derive(Clone)]
pub struct Ipv4Generator {
    pub network: u32,
    pub host_bits: u32,
    pub null_rate: Option<f64>,
}

impl Ipv4Generator {
    pub fn new(cidr: Option<&str>, null_rate: Option<f64>) -> Result<Self> {
        let (network, prefix) = match cidr {
            Some(cidr) => parse_cidr(cidr, 32, |text| {
                text.parse::<Ipv4Addr>().map(|address| u32::from(address) as u128)
            })?,
            None => (0, 0),
        };
        Ok(Self { network: network as u32, host_bits: 32 - prefix, null_rate })
    }
}

impl FieldGenerator for Ipv4Generator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let host = if self.host_bits == 0 {
            0
        } else {
            rng.gen::<u32>() >> (32 - self.host_bits)
        };
        Ok(Value::String(Ipv4Addr::from(self.network | host).to_string()))
    }
}

#[derive(Clone)]
pub struct Ipv6Generator {
    pub network: u128,
    pub host_bits: u32,
    pub null_rate: Option<f64>,
}

impl Ipv6Generator {
    pub fn new(cidr: Option<&str>, null_rate: Option<f64>) -> Result<Self> {
        let (network, prefix) = match cidr {
            Some(cidr) => parse_cidr(cidr, 128, |text| {
                text.parse::<Ipv6Addr>().map(u128::from)
            })?,
            None => (0, 0),
        };
        Ok(Self { network, host_bits: 128 - prefix, null_rate })
    }
}

impl FieldGenerator for Ipv6Generator {
    fn generate(&mut self, _ctx: &RowContext, rng: &mut StdRng) -> Result<Value> {
        if should_emit_null(self.null_rate, rng)? {
            return Ok(Value::Null);
        }
        let host = if self.host_bits == 0 {
            0
        } else {
            rng.gen::<u128>() >> (128 - self.host_bits)
        };
        Ok(Value::String(Ipv6Addr::from(self.network | host).to_string()))
    }
}

/// Parse `address/prefix`, returning the network with host bits cleared.
fn parse_cidr<E>(
    cidr: &str,
    width: u32,
    parse: impl Fn(&str) -> std::result::Result<u128, E>,
) -> Result<(u128, u32)> {
    let (address, prefix) = cidr
        .split_once('/')
        .ok_or_else(|| anyhow!("cidr `{}` needs a prefix length, e.g. 10.0.0.0/8", cidr))?;
    let prefix: u32 = prefix
        .trim()
        .parse()
        .map_err(|_| anyhow!("cidr `{}` has a bad prefix length", cidr))?;
    if prefix > width {
        bail!("cidr `{}` prefix length is above {}", cidr, width);
    }
    let address = parse(address.trim()).map_err(|_| anyhow!("cidr `{}` has a bad address", cidr))?;
    let host_mask = if prefix == 0 {
        u128::MAX >> (128 - width)
    } else {
        (1u128 << (width - prefix)).wrapping_sub(1)
    };
    Ok((address & !host_mask, prefix))
}

#[cfg(test)]
mod tests {
    use super::*;
    use rand::SeedableRng;

    #[test]
    fn ipv4_stays_inside_its_network() {
        let mut generator = Ipv4Generator::new(Some("10.20.0.0/16"), None).unwrap();
        let mut rng = StdRng::seed_from_u64(1);
        for _ in 0..1000 {
            let Value::String(text) = generator.generate(&RowContext::new(), &mut rng).unwrap() else {
                panic!("expected text");
            };
            let address: Ipv4Addr = text.parse().unwrap();
            assert_eq!(address.octets()[..2], [10, 20], "{}", text);
        }
    }

    #[test]
    fn ipv6_stays_inside_its_network_and_clears_host_bits() {
        let mut generator = Ipv6Generator::new(Some("2001:db8:ffff::1/32"), None).unwrap();
        assert_eq!(generator.network, u128::from("2001:db8::".parse::<Ipv6Addr>().unwrap()));
        let mut rng = StdRng::seed_from_u64(2);
        for _ in 0..1000 {
            let Value::String(text) = generator.generate(&RowContext::new(), &mut rng).unwrap() else {
                panic!("expected text");
            };
            let address: Ipv6Addr = text.parse().unwrap();
            assert_eq!(address.segments()[..2], [0x2001, 0x0db8], "{}", text);
        }
    }

    #[test]
    fn full_range_and_host_routes_work() {
        assert_eq!(Ipv4Generator::new(None, None).unwrap().host_bits, 32);
        let mut single = Ipv4Generator::new(Some("192.168.1.7/32"), None).unwrap();
        let value = single.generate(&RowContext::new(), &mut StdRng::seed_from_u64(3)).unwrap();
        assert!(matches!(value, Value::String(text) if text == "192.168.1.7"));
    }

    #[test]
    fn rejects_bad_cidrs() {
        assert!(Ipv4Generator::new(Some("10.0.0.0"), None).is_err());
        assert!(Ipv4Generator::new(Some("10.0.0.0/33"), None).is_err());
        assert!(Ipv6Generator::new(Some("zz::/8"), None).is_err());
    }
}
