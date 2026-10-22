//! A minimal Confluent Schema Registry client: register a schema under a
//! subject and get its id back.

use anyhow::{anyhow, bail, Context, Result};
use serde::Deserialize;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SchemaType {
    Avro,
    Protobuf,
}

impl SchemaType {
    fn registry_name(self) -> &'static str {
        match self {
            SchemaType::Avro => "AVRO",
            SchemaType::Protobuf => "PROTOBUF",
        }
    }
}

pub struct RegistryClient {
    base_url: String,
    username: Option<String>,
    password: Option<String>,
    client: reqwest::Client,
}

#[derive(Deserialize)]
struct RegisterResponse {
    id: u32,
}

impl RegistryClient {
    pub fn new(base_url: &str, username: Option<String>, password: Option<String>) -> Result<Self> {
        let client = reqwest::Client::builder()
            .build()
            .context("failed to build the HTTP client for the schema registry")?;
        Ok(Self {
            base_url: base_url.trim_end_matches('/').to_string(),
            username,
            password,
            client,
        })
    }

    /// Register `schema` under `subject`. The registry returns the existing
    /// id when the same schema is already there, so this is safe to repeat.
    pub async fn register(&self, subject: &str, schema: &str, kind: SchemaType) -> Result<u32> {
        let url = format!("{}/subjects/{}/versions", self.base_url, subject);
        let body = serde_json::json!({ "schema": schema, "schemaType": kind.registry_name() });
        let mut request = self
            .client
            .post(&url)
            .header("Content-Type", "application/vnd.schemaregistry.v1+json")
            .json(&body);
        if let Some(username) = &self.username {
            request = request.basic_auth(username, self.password.as_deref());
        }
        let response = request
            .send()
            .await
            .with_context(|| format!("schema registry request to {} failed", url))?;
        let status = response.status();
        let text = response.text().await.unwrap_or_default();
        if !status.is_success() {
            bail!(
                "schema registry refused to register `{}` ({}): {}",
                subject,
                status,
                text.trim()
            );
        }
        let parsed: RegisterResponse = serde_json::from_str(&text)
            .map_err(|_| anyhow!("unexpected schema registry response: {}", text.trim()))?;
        Ok(parsed.id)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    /// A one-shot HTTP server that records the request and answers with a
    /// canned body.
    async fn stub(reply: &'static str) -> (String, tokio::task::JoinHandle<String>) {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = format!("http://{}", listener.local_addr().unwrap());
        let handle = tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut buffer = vec![0u8; 65536];
            let mut request = Vec::new();
            loop {
                let read = socket.read(&mut buffer).await.unwrap();
                request.extend_from_slice(&buffer[..read]);
                let text = String::from_utf8_lossy(&request);
                if let Some(split) = text.find("\r\n\r\n") {
                    let length = text
                        .lines()
                        .find_map(|line| line.strip_prefix("content-length: ").or_else(|| line.strip_prefix("Content-Length: ")))
                        .and_then(|value| value.trim().parse::<usize>().ok())
                        .unwrap_or(0);
                    if request.len() >= split + 4 + length {
                        break;
                    }
                }
                if read == 0 {
                    break;
                }
            }
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                reply.len(),
                reply
            );
            socket.write_all(response.as_bytes()).await.unwrap();
            socket.shutdown().await.ok();
            String::from_utf8_lossy(&request).to_string()
        });
        (address, handle)
    }

    #[tokio::test]
    async fn registers_and_returns_the_id() {
        let (address, handle) = stub(r#"{"id": 42}"#).await;
        let client = RegistryClient::new(&address, Some("u".into()), Some("p".into())).unwrap();
        let id = client.register("events-value", "syntax = \"proto3\";", SchemaType::Protobuf).await.unwrap();
        assert_eq!(id, 42);
        let request = handle.await.unwrap();
        assert!(request.starts_with("POST /subjects/events-value/versions HTTP/1.1"), "{}", request);
        assert!(request.contains("\"schemaType\":\"PROTOBUF\""), "{}", request);
        assert!(request.contains("\"schema\":\"syntax = \\\"proto3\\\";\""), "{}", request);
        assert!(request.to_ascii_lowercase().contains("authorization: basic dtpw"), "{}", request);
    }
}
