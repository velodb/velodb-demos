//! Total up what a run has actually put in the bucket, and clear the debris.
//!
//! The generator's progress line reports what it believes it sent. This reports
//! what the bucket says is there, which is the figure worth trusting after a
//! run that lasted days.
//!
//! It uses the AWS SDK rather than `object_store`, which the generator uses,
//! because `ListMultipartUploads` and `AbortMultipartUpload` are not part of
//! the `ObjectStore` trait. Only the config parsing is shared.

// The config struct describes the generator's whole S3 setup; this binary
// reads only the parts it needs to reach the bucket.
#[allow(dead_code)]
#[path = "../s3.rs"]
mod s3;
#[allow(dead_code)]

use std::collections::BTreeMap;
use std::path::PathBuf;

use anyhow::{Context, Result};
use aws_credential_types::Credentials;
use aws_sdk_s3::config::{BehaviorVersion, Region};
use aws_sdk_s3::Client;
use chrono::{DateTime, Duration as ChronoDuration, Utc};
use clap::Parser;

use datagen::units::{format_bytes, group_digits};

#[derive(Parser, Debug)]
#[command(
    name = "calcsize",
    version,
    about = "Total the size of Parquet objects under an S3 prefix, and clear stale multipart uploads"
)]
struct Args {
    /// S3 config TOML. The same file the generator takes with --s3-config.
    #[arg(short = 'c', long, default_value = "s3.local.toml", value_name = "FILE")]
    config: PathBuf,

    /// List this prefix instead of the one in the config.
    #[arg(short = 'p', long, value_name = "PREFIX")]
    prefix: Option<String>,

    /// Count every object, not just `.parquet`.
    #[arg(long)]
    all: bool,

    /// Break the total down by the run id in `part-<run>-w..`.
    #[arg(long)]
    by_run: bool,

    /// Abort multipart uploads started more than this many days ago. An
    /// interrupted run leaves parts that are billed but invisible to a normal
    /// listing; nothing legitimate stays in flight for days.
    #[arg(long, default_value_t = 10, value_name = "DAYS")]
    abort_older_than: i64,

    /// Report stale multipart uploads without aborting any.
    #[arg(long, conflicts_with = "force_abort")]
    no_abort: bool,

    /// Abort every multipart upload under the prefix regardless of age, for
    /// clearing up immediately after a run rather than waiting out the age
    /// cutoff. This will kill uploads a running generator still has in flight,
    /// so do not point it at a prefix something is still writing to.
    #[arg(long)]
    force_abort: bool,
}

#[derive(Default)]
struct Tally {
    count: u64,
    bytes: u64,
    smallest: Option<u64>,
    largest: Option<u64>,
}

impl Tally {
    fn add(&mut self, size: u64) {
        self.count += 1;
        self.bytes += size;
        self.smallest = Some(self.smallest.map_or(size, |current| current.min(size)));
        self.largest = Some(self.largest.map_or(size, |current| current.max(size)));
    }
}

/// Pull the run id out of `part-<run_id>-w<nn>-<index>.parquet`. Anything
/// written before run ids existed, or by something else, groups under "-".
fn run_id_of(key: &str) -> String {
    let name = key.rsplit('/').next().unwrap_or(key);
    let Some(rest) = name.strip_prefix("part-") else {
        return "-".to_string();
    };
    match rest.rfind("-w") {
        Some(at) if at > 0 => rest[..at].to_string(),
        _ => "-".to_string(),
    }
}

fn build_client(config: &s3::OutputConfig) -> Result<Client> {
    let s3 = config.s3()?;
    let region = s3
        .region
        .clone()
        .unwrap_or_else(|| "us-east-1".to_string());
    let mut builder = aws_sdk_s3::Config::builder()
        .behavior_version(BehaviorVersion::latest())
        .region(Region::new(region))
        // Unlike object_store, the SDK puts the bucket in the host itself, so
        // the plain regional endpoint is what belongs here.
        .force_path_style(s3.path_style);
    if let Some(endpoint) = &s3.endpoint {
        builder = builder.endpoint_url(endpoint.trim_end_matches('/'));
    }
    if let Some(credentials) = &s3.credentials {
        builder = builder.credentials_provider(Credentials::new(
            credentials.access_key_id.clone(),
            credentials.secret_access_key.clone(),
            credentials.session_token.clone().filter(|t| !t.is_empty()),
            None,
            "s3-config-file",
        ));
    }
    Ok(Client::from_conf(builder.build()))
}

async fn sum_objects(
    client: &Client,
    bucket: &str,
    prefix: &str,
    args: &Args,
) -> Result<(Tally, Tally, BTreeMap<String, Tally>)> {
    let mut total = Tally::default();
    let mut skipped = Tally::default();
    let mut by_run: BTreeMap<String, Tally> = BTreeMap::new();
    let mut token: Option<String> = None;

    loop {
        let mut request = client.list_objects_v2().bucket(bucket).max_keys(1000);
        if !prefix.is_empty() {
            request = request.prefix(prefix);
        }
        if let Some(marker) = &token {
            request = request.continuation_token(marker);
        }
        let page = request
            .send()
            .await
            .context("listing objects failed; check bucket, endpoint and credentials")?;

        for object in page.contents() {
            let key = object.key().unwrap_or_default();
            let size = object.size().unwrap_or(0).max(0) as u64;
            if !args.all && !key.ends_with(".parquet") {
                skipped.add(size);
                continue;
            }
            total.add(size);
            if args.by_run {
                by_run.entry(run_id_of(key)).or_default().add(size);
            }
        }
        if total.count > 0 && total.count % 100_000 < 1000 {
            eprintln!("  ... {} objects so far", group_digits(total.count));
        }

        if page.is_truncated().unwrap_or(false) {
            token = page.next_continuation_token().map(str::to_string);
            if token.is_none() {
                break;
            }
        } else {
            break;
        }
    }
    Ok((total, skipped, by_run))
}

/// Every in-flight multipart upload under the prefix, following pagination.
async fn list_multipart(
    client: &Client,
    bucket: &str,
    prefix: &str,
) -> Result<Vec<(String, String, DateTime<Utc>)>> {
    let mut found = Vec::new();
    let mut key_marker: Option<String> = None;
    let mut id_marker: Option<String> = None;

    loop {
        let mut request = client.list_multipart_uploads().bucket(bucket);
        if !prefix.is_empty() {
            request = request.prefix(prefix);
        }
        if let Some(marker) = &key_marker {
            request = request.key_marker(marker);
        }
        if let Some(marker) = &id_marker {
            request = request.upload_id_marker(marker);
        }
        let page = request
            .send()
            .await
            .context("listing multipart uploads failed")?;

        for upload in page.uploads() {
            let (Some(key), Some(id), Some(initiated)) =
                (upload.key(), upload.upload_id(), upload.initiated())
            else {
                continue;
            };
            let Some(when) = DateTime::from_timestamp(initiated.secs(), 0) else {
                continue;
            };
            found.push((key.to_string(), id.to_string(), when));
        }

        if page.is_truncated().unwrap_or(false) {
            key_marker = page.next_key_marker().map(str::to_string);
            id_marker = page.next_upload_id_marker().map(str::to_string);
            if key_marker.is_none() {
                break;
            }
        } else {
            break;
        }
    }
    Ok(found)
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();

    let config = s3::load(&args.config).with_context(|| {
        format!(
            "failed to read config `{}` (pass -c to point at another)",
            args.config.display()
        )
    })?;
    let client = build_client(&config)?;
    let s3 = config.s3()?;
    let bucket = s3.bucket.clone();
    let prefix = match &args.prefix {
        Some(given) => {
            let trimmed = given.trim().trim_start_matches('/');
            if trimmed.is_empty() || trimmed.ends_with('/') {
                trimmed.to_string()
            } else {
                format!("{}/", trimmed)
            }
        }
        None => config.normalised_prefix(),
    };

    eprintln!("listing s3://{}/{}", bucket, prefix);
    let (total, skipped, by_run) = sum_objects(&client, &bucket, &prefix, &args).await?;

    if args.by_run && by_run.len() > 1 {
        println!("by run:");
        for (run, tally) in &by_run {
            println!(
                "  {:<28} {:>12}  {:>12} objects",
                run,
                format_bytes(tally.bytes),
                group_digits(tally.count)
            );
        }
        println!();
    }

    let label = if args.all { "objects" } else { "parquet files" };
    println!("{:<20}{}", label, group_digits(total.count));
    println!(
        "{:<20}{}  ({} bytes)",
        "total size",
        format_bytes(total.bytes),
        group_digits(total.bytes)
    );
    if total.count > 0 {
        println!(
            "{:<20}{}",
            "average",
            format_bytes(total.bytes / total.count)
        );
        println!(
            "{:<20}{} / {}",
            "smallest / largest",
            format_bytes(total.smallest.unwrap_or(0)),
            format_bytes(total.largest.unwrap_or(0))
        );
    }
    if skipped.count > 0 {
        println!(
            "{:<20}{} in {} non-parquet object(s), not counted (--all includes them)",
            "skipped",
            format_bytes(skipped.bytes),
            group_digits(skipped.count)
        );
    }

    // ---- multipart debris ---------------------------------------------------
    let uploads = list_multipart(&client, &bucket, &prefix).await?;
    if uploads.is_empty() {
        return Ok(());
    }
    let now = Utc::now();
    let cutoff = ChronoDuration::days(args.abort_older_than.max(0));
    let (stale, fresh): (Vec<_>, Vec<_>) = uploads
        .into_iter()
        .partition(|(_, _, initiated)| args.force_abort || now - *initiated > cutoff);

    let days = args.abort_older_than;
    if args.no_abort {
        println!(
            "\nfound {} multipart upload(s) older than {} days and {} within it; \
             --no-abort left all of them alone.",
            group_digits(stale.len() as u64),
            days,
            group_digits(fresh.len() as u64)
        );
        return Ok(());
    }

    let mut cleared = 0u64;
    let mut failed = 0u64;
    for (key, upload_id, _) in &stale {
        match client
            .abort_multipart_upload()
            .bucket(&bucket)
            .key(key)
            .upload_id(upload_id)
            .send()
            .await
        {
            Ok(_) => cleared += 1,
            Err(error) => {
                failed += 1;
                eprintln!("could not abort `{}`: {}", key, error);
            }
        }
    }

    if args.force_abort {
        println!(
            "\nalso cleared {} multipart uploads of any age (--force-abort).",
            group_digits(cleared)
        );
    } else {
        println!(
            "\nalso cleared {} multipart uploads older than {} days. \
             Left {} multipart uploads intact within {} days.",
            group_digits(cleared),
            days,
            group_digits(fresh.len() as u64),
            days
        );
    }
    if failed > 0 {
        println!(
            "{} could not be aborted; they are left for the bucket's lifecycle rule.",
            group_digits(failed)
        );
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::run_id_of;

    #[test]
    fn reads_the_run_id_out_of_an_object_name() {
        assert_eq!(
            run_id_of("datagen/x/part-20260910T143803Z-79ee76-w00-000001.parquet"),
            "20260910T143803Z-79ee76"
        );
        // Written before run ids existed.
        assert_eq!(run_id_of("datagen/x/part-w03-000042.parquet"), "-");
        // Not ours at all.
        assert_eq!(run_id_of("datagen/x/_SUCCESS"), "-");
    }
}
