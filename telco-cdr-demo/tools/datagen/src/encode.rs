//! One encoder for every output format, and the framing around each record.
//!
//! Text formats are line-delimited. `avro_stream` and `proto_stream` are a
//! u32 big-endian length-prefixed schema followed by u32 length-prefixed
//! records. Kafka messages carry the Confluent wire format: a zero magic
//! byte, the registry's schema id as a u32 big-endian, then the record
//! (for Protobuf, the message-index path comes before the record).

use anyhow::{bail, Result};

use crate::compile::RowContext;
use crate::schema::{AvroEncoder, ProtoEncoder};
use crate::text::TextEncoder;

pub enum RecordEncoder {
    Text(TextEncoder),
    Avro(AvroEncoder),
    Proto(ProtoEncoder),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Framing {
    /// CSV or JSON lines, with the CSV header as the prelude.
    Lines,
    /// `[u32 len][schema]` once, then `[u32 len][record]` per row.
    LengthPrefixed,
    /// One record per message, Confluent wire format for Avro and Protobuf,
    /// the bare JSON object for text.
    Confluent { schema_id: u32 },
}

impl RecordEncoder {
    /// The schema text this encoder carries, for a registry or a prelude.
    pub fn schema_text(&self) -> Option<&str> {
        match self {
            RecordEncoder::Text(_) => None,
            RecordEncoder::Avro(encoder) => Some(encoder.schema_text()),
            RecordEncoder::Proto(encoder) => Some(encoder.schema_text()),
        }
    }

    /// What goes at the top of every file or stream before the rows.
    pub fn prelude(&self, framing: &Framing) -> Result<Option<Vec<u8>>> {
        match (self, framing) {
            (RecordEncoder::Text(text), Framing::Lines) => text.header(),
            (RecordEncoder::Avro(_) | RecordEncoder::Proto(_), Framing::LengthPrefixed) => {
                let schema = self.schema_text().unwrap_or_default();
                let mut out = Vec::with_capacity(4 + schema.len());
                write_length_prefixed(&mut out, schema.as_bytes())?;
                Ok(Some(out))
            }
            (_, Framing::Confluent { .. }) => Ok(None),
            (RecordEncoder::Text(_), Framing::LengthPrefixed) => {
                bail!("csv and json are line formats; length-prefixed framing needs avro_stream or proto_stream")
            }
            (RecordEncoder::Avro(_) | RecordEncoder::Proto(_), Framing::Lines) => {
                bail!("avro and protobuf records are binary; they need length-prefixed framing")
            }
        }
    }

    /// One row, framed, appended to `out`.
    pub fn encode(&self, ctx: &RowContext, framing: &Framing, out: &mut Vec<u8>) -> Result<()> {
        match (self, framing) {
            (RecordEncoder::Text(text), Framing::Lines) => text.encode_row(ctx, out),
            (RecordEncoder::Text(text), Framing::Confluent { .. }) => {
                text.encode_row(ctx, out)?;
                while matches!(out.last(), Some(b'\n' | b'\r')) {
                    out.pop();
                }
                Ok(())
            }
            (RecordEncoder::Avro(avro), Framing::LengthPrefixed) => {
                write_length_prefixed(out, &avro.encode(ctx)?)
            }
            (RecordEncoder::Avro(avro), Framing::Confluent { schema_id }) => {
                write_confluent_header(out, *schema_id, &[]);
                out.extend_from_slice(&avro.encode(ctx)?);
                Ok(())
            }
            (RecordEncoder::Proto(proto), Framing::LengthPrefixed) => {
                write_length_prefixed(out, &proto.encode(ctx)?)
            }
            (RecordEncoder::Proto(proto), Framing::Confluent { schema_id }) => {
                write_confluent_header(out, *schema_id, proto.message_indexes());
                out.extend_from_slice(&proto.encode(ctx)?);
                Ok(())
            }
            (RecordEncoder::Text(_), Framing::LengthPrefixed)
            | (RecordEncoder::Avro(_) | RecordEncoder::Proto(_), Framing::Lines) => {
                self.prelude(framing).map(|_| ())
            }
        }
    }
}

/// `[u32 big-endian length][bytes]`.
pub fn write_length_prefixed(out: &mut Vec<u8>, bytes: &[u8]) -> Result<()> {
    let length = u32::try_from(bytes.len())
        .map_err(|_| anyhow::anyhow!("a record of {} bytes does not fit a u32 length prefix", bytes.len()))?;
    out.extend_from_slice(&length.to_be_bytes());
    out.extend_from_slice(bytes);
    Ok(())
}

/// The Confluent Schema Registry wire header: magic `0`, schema id, and for
/// Protobuf the message-index path as zigzag varints, where the common
/// `[0]` collapses to a single zero byte.
pub fn write_confluent_header(out: &mut Vec<u8>, schema_id: u32, message_indexes: &[i32]) {
    out.push(0);
    out.extend_from_slice(&schema_id.to_be_bytes());
    if message_indexes.is_empty() {
        return;
    }
    if message_indexes == [0] {
        out.push(0);
        return;
    }
    write_zigzag_varint(out, message_indexes.len() as i64);
    for index in message_indexes {
        write_zigzag_varint(out, *index as i64);
    }
}

fn write_zigzag_varint(out: &mut Vec<u8>, value: i64) {
    let mut zigzag = ((value << 1) ^ (value >> 63)) as u64;
    loop {
        let byte = (zigzag & 0x7F) as u8;
        zigzag >>= 7;
        if zigzag == 0 {
            out.push(byte);
            return;
        }
        out.push(byte | 0x80);
    }
}

/// Read back a length-prefixed stream: the schema, then every record.
/// Used by tests and handy for consumers written in Rust.
pub fn read_length_prefixed(mut bytes: &[u8]) -> Result<Vec<Vec<u8>>> {
    let mut frames = Vec::new();
    while !bytes.is_empty() {
        if bytes.len() < 4 {
            bail!("truncated length prefix");
        }
        let length = u32::from_be_bytes([bytes[0], bytes[1], bytes[2], bytes[3]]) as usize;
        bytes = &bytes[4..];
        if bytes.len() < length {
            bail!("truncated frame: {} bytes announced, {} left", length, bytes.len());
        }
        frames.push(bytes[..length].to_vec());
        bytes = &bytes[length..];
    }
    Ok(frames)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn confluent_header_for_avro_is_magic_and_id() {
        let mut out = Vec::new();
        write_confluent_header(&mut out, 0x0102, &[]);
        assert_eq!(out, [0, 0, 0, 1, 2]);
    }

    #[test]
    fn confluent_header_for_protobuf_carries_message_indexes() {
        let mut out = Vec::new();
        write_confluent_header(&mut out, 7, &[0]);
        assert_eq!(out, [0, 0, 0, 0, 7, 0], "first message collapses to one zero byte");

        let mut out = Vec::new();
        write_confluent_header(&mut out, 7, &[2]);
        assert_eq!(out, [0, 0, 0, 0, 7, 2, 4], "length 1 then index 2, zigzag encoded");

        let mut out = Vec::new();
        write_confluent_header(&mut out, 7, &[1, 0]);
        assert_eq!(out, [0, 0, 0, 0, 7, 4, 2, 0]);
    }

    #[test]
    fn length_prefixed_frames_round_trip() {
        let mut out = Vec::new();
        write_length_prefixed(&mut out, b"schema").unwrap();
        write_length_prefixed(&mut out, b"").unwrap();
        write_length_prefixed(&mut out, &[1, 2, 3]).unwrap();
        let frames = read_length_prefixed(&out).unwrap();
        assert_eq!(frames, vec![b"schema".to_vec(), Vec::new(), vec![1, 2, 3]]);
        assert!(read_length_prefixed(&out[..out.len() - 1]).is_err());
    }
}
