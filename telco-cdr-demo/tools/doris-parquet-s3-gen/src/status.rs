//! Live status display.
//!
//! `render` is pure: it turns a `Snapshot` into lines that are guaranteed to
//! fit the given terminal width. The layout degrades in stages as the terminal
//! narrows, and every line is truncated as a final safety net, so the display
//! cannot corrupt a small terminal.

use std::time::Duration;

/// A file currently being written and uploaded.
#[derive(Debug, Clone)]
pub struct ActiveFile {
    pub name: String,
    pub bytes: u64,
}

/// One sampled view of generation and upload progress.
#[derive(Debug, Clone, Default)]
pub struct Snapshot {
    pub elapsed: Duration,
    pub threads: usize,
    pub writers: usize,
    /// Batches waiting in the queue, and its capacity.
    pub queued: usize,
    pub queue_capacity: usize,
    pub rows: u64,
    pub target_rows: Option<u64>,
    pub bytes_generated: u64,
    pub bytes_uploaded: u64,
    pub target_bytes: Option<u64>,
    pub buffered_bytes: u64,
    pub files_completed: u64,
    pub active_files: Vec<ActiveFile>,
    pub rows_per_sec: f64,
    pub gen_bytes_per_sec: f64,
    pub upload_bytes_per_sec: f64,
    pub file_cap: Option<u64>,
    /// Generators have stopped; the workers are draining the queue.
    pub draining: bool,
    /// Set once no more rows will be produced, so the display can say so.
    pub finished: bool,
}

// Every varying field is padded to a reserved width. Numbers then grow into
// their own space instead of pushing the labels after them sideways, so the
// block stays still while it updates.
/// "9,999,999" rows per second.
const W_ROWS_RATE: usize = 9;
/// "1023 MiB".
const W_BYTES: usize = 8;
/// "1023 MiB/s".
const W_BYTE_RATE: usize = 10;
/// "upload-bound".
const W_STATE: usize = 12;
/// "done in 1h05m".
const W_TIMING: usize = 13;
/// Completed file count.
const W_FILES: usize = 3;
/// The leading label: "rows", "gen", "up", "file".
const W_LABEL: usize = 4;
/// The primary field on the gen and up lines, so their pairs share a column.
/// Sized by the wider of the two: rate + " rows/s " + state.
const W_PRIMARY: usize = W_ROWS_RATE + 8 + W_STATE;

/// Below this the progress bar is dropped.
const BAR_MIN_WIDTH: usize = 68;
/// Below this the layout switches to the compact two-line form.
const COMPACT_MAX_WIDTH: usize = 52;

pub fn render(snapshot: &Snapshot, width: usize) -> Vec<String> {
    let width = width.clamp(20, 200);
    let lines = if width < COMPACT_MAX_WIDTH {
        render_compact(snapshot, width)
    } else {
        render_full(snapshot, width)
    };
    lines
        .into_iter()
        .map(|line| truncate(&line, width))
        .collect()
}

fn render_full(snapshot: &Snapshot, width: usize) -> Vec<String> {
    let mut lines = Vec::with_capacity(4);
    let short = width < 64;

    // Line 1: rows against target, with a bar and ETA when there is room.
    lines.push(progress_line(snapshot, width, short));

    // Line 2: generation side. There is no byte rate here on purpose: a row's
    // size is not known until its row group is encoded, so the only honest
    // byte rate belongs to the upload line below.
    //
    // Generators blocked on a full queue are feeling upload backpressure, not
    // stalling. Say so, or a zero rate looks like a hang.
    let state = if snapshot.draining {
        "draining"
    } else if snapshot.queued >= snapshot.queue_capacity && snapshot.queue_capacity > 0 {
        "upload-bound"
    } else {
        ""
    };
    let generating = format!(
        "{} rows/s {}",
        rjust(&fmt_count(snapshot.rows_per_sec as u64), W_ROWS_RATE),
        ljust(state, W_STATE),
    );
    let queue_digits = digits(snapshot.queue_capacity as u64);
    lines.push(columns_line(
        "gen",
        &generating,
        &[
            ("threads", rjust(&snapshot.threads.to_string(), 2)),
            (
                "queue",
                format!(
                    "{}/{}",
                    rjust(&snapshot.queued.to_string(), queue_digits),
                    snapshot.queue_capacity
                ),
            ),
            ("buffered", rjust(&fmt_bytes(snapshot.buffered_bytes), W_BYTES)),
        ],
        width,
        short,
    ));

    // Line 3: upload side.
    let active = snapshot.active_files.len();
    let upload = format!(
        "{} done, {} {}",
        rjust(&snapshot.files_completed.to_string(), W_FILES),
        rjust(&if active > 0 { active } else { snapshot.writers }.to_string(), 2),
        ljust(if active > 0 { "active" } else { "writers" }, 7),
    );
    lines.push(columns_line(
        "up",
        &upload,
        &[
            ("sent", rjust(&fmt_bytes(snapshot.bytes_uploaded), W_BYTES)),
            (
                "rate",
                rjust(
                    &format!("{}/s", fmt_bytes(snapshot.upload_bytes_per_sec as u64)),
                    W_BYTE_RATE,
                ),
            ),
        ],
        width,
        short,
    ));

    // Line 4: the files in flight, as many as fit.
    if !snapshot.active_files.is_empty() {
        lines.push(active_files_line(snapshot, width));
    }
    lines
}

/// A label, a fixed-width primary field, then key/value pairs.
///
/// Both the label and the primary field are padded to constant widths, so the
/// pairs on every line begin at the same column and stay there as the numbers
/// underneath them change.
fn columns_line(
    label: &str,
    primary: &str,
    pairs: &[(&str, String)],
    width: usize,
    short: bool,
) -> String {
    let mut out = format!("{}{}", label_cell(label), ljust(primary, W_PRIMARY));
    for (key, value) in pairs {
        let key = if short { &key[..key.len().min(3)] } else { *key };
        let piece = format!("  {} {}", key, value);
        // Stop at the first pair that will not fit. Pairs are ordered by
        // importance, and skipping one to fit a later shorter one would make
        // fields appear and vanish between redraws.
        if out.chars().count() + piece.chars().count() > width {
            break;
        }
        out.push_str(&piece);
    }
    out
}

/// The leading label, padded so every line's content starts in one column.
fn label_cell(label: &str) -> String {
    format!(" {} ", ljust(label, W_LABEL))
}

fn render_compact(snapshot: &Snapshot, width: usize) -> Vec<String> {
    let mut lines = Vec::with_capacity(3);
    let rows = match snapshot.target_rows {
        Some(target) => format!(
            "{}/{} {}",
            fmt_count_short(snapshot.rows),
            fmt_count_short(target),
            fmt_percent(snapshot.rows, target)
        ),
        None => fmt_count_short(snapshot.rows),
    };
    lines.push(clip(&format!(" rows {}  {}", rows, tail_timing(snapshot)), width));
    lines.push(clip(
        &format!(
            " gen  {}/s  buf {}",
            fmt_count_short(snapshot.rows_per_sec as u64),
            fmt_bytes(snapshot.buffered_bytes)
        ),
        width,
    ));
    lines.push(clip(
        &format!(
            " up   {} f  {}  {}/s",
            snapshot.files_completed,
            fmt_bytes(snapshot.bytes_uploaded),
            fmt_bytes(snapshot.upload_bytes_per_sec as u64)
        ),
        width,
    ));
    lines
}

fn progress_line(snapshot: &Snapshot, width: usize, short: bool) -> String {
    let label = label_cell("rows");
    let label = label.as_str();
    let timing = tail_timing(snapshot);

    let Some((done, target)) = progress_target(snapshot) else {
        // No target: show the count and elapsed time only.
        let counts = fmt_count(snapshot.rows);
        return pad_between(
            &format!("{}{}", label, counts),
            &rjust(&timing, W_TIMING),
            width,
        );
    };

    // Right-align the running count against the target so digits do not jitter.
    let target_text = if snapshot.target_rows.is_some() {
        fmt_count(target)
    } else {
        fmt_bytes(target)
    };
    let done_text = if snapshot.target_rows.is_some() {
        fmt_count(done)
    } else {
        fmt_bytes(done)
    };
    let counts = format!(
        "{:>width$} / {}",
        done_text,
        target_text,
        width = target_text.chars().count()
    );
    let percent = fmt_percent(done, target);

    let head = format!("{}{}  {:>4}", label, counts, percent);
    if short || width < BAR_MIN_WIDTH {
        return pad_between(&head, &rjust(&timing, W_TIMING), width);
    }

    // Size the bar against the reserved timing width, not the current text,
    // or the bar would grow and shrink as the estimate changes.
    let used = head.chars().count() + W_TIMING + 4;
    let bar_width = width.saturating_sub(used).clamp(0, 36);
    if bar_width < 8 {
        return pad_between(&head, &rjust(&timing, W_TIMING), width);
    }
    let bar = fmt_bar(done, target, bar_width);
    pad_between(
        &format!("{}  {}", head, bar),
        &rjust(&timing, W_TIMING),
        width,
    )
}

/// `label value` on the left, `key value` pairs packed to the right.
fn pair_line(label: &str, value: &str, pairs: &[(&str, String)], width: usize, short: bool) -> String {
    let head = format!(" {:<4} {}", label, value);
    let mut tail = String::new();
    for (key, value) in pairs {
        let piece = if short {
            format!("{} {}", &key[..key.len().min(3)], value)
        } else {
            format!("{} {}", key, value)
        };
        let candidate = if tail.is_empty() {
            piece
        } else {
            format!("{}  {}", tail, piece)
        };
        // Only keep a pair if the whole line still fits.
        if head.chars().count() + 2 + candidate.chars().count() <= width {
            tail = candidate;
        }
    }
    pad_between(&head, &tail, width)
}

fn active_files_line(snapshot: &Snapshot, width: usize) -> String {
    let mut out = label_cell("file");
    let mut first = true;
    for file in &snapshot.active_files {
        let piece = match snapshot.file_cap {
            Some(cap) => format!(
                "{} {}/{}",
                file.name,
                rjust(&fmt_bytes(file.bytes), W_BYTES),
                fmt_bytes(cap)
            ),
            None => format!("{} {}", file.name, rjust(&fmt_bytes(file.bytes), W_BYTES)),
        };
        let sep = if first { "" } else { "  " };
        if out.chars().count() + sep.chars().count() + piece.chars().count() > width {
            // Signal that more files are in flight than fit on the line.
            let more = format!("{}+{}", sep, snapshot.active_files.len() - count_shown(&out));
            if out.chars().count() + more.chars().count() <= width {
                out.push_str(&more);
            }
            break;
        }
        out.push_str(sep);
        out.push_str(&piece);
        first = false;
    }
    out
}

fn count_shown(line: &str) -> usize {
    line.matches(".parquet").count().max(line.split("  ").count().saturating_sub(1))
}

fn progress_target(snapshot: &Snapshot) -> Option<(u64, u64)> {
    if let Some(target) = snapshot.target_rows {
        return Some((snapshot.rows.min(target), target));
    }
    snapshot
        .target_bytes
        .map(|target| (snapshot.bytes_generated.min(target), target))
}

/// ETA when a target and a rate are known, otherwise elapsed time.
fn tail_timing(snapshot: &Snapshot) -> String {
    if snapshot.finished {
        return format!("done in {}", fmt_duration(snapshot.elapsed));
    }
    if let Some((done, target)) = progress_target(snapshot) {
        let rate = if snapshot.target_rows.is_some() {
            snapshot.rows_per_sec
        } else {
            snapshot.gen_bytes_per_sec
        };
        if rate > 0.0 && target > done {
            let seconds = (target - done) as f64 / rate;
            if seconds.is_finite() && seconds < 86_400.0 * 30.0 {
                return format!("ETA {}", fmt_duration(Duration::from_secs(seconds as u64)));
            }
        }
    }
    fmt_duration(snapshot.elapsed)
}

fn fmt_bar(done: u64, target: u64, width: usize) -> String {
    if target == 0 || width == 0 {
        return String::new();
    }
    let filled = ((done as f64 / target as f64) * width as f64).round() as usize;
    let filled = filled.min(width);
    // Block characters are one column wide, so char counting stays accurate.
    let mut bar = String::with_capacity(width * 3);
    for _ in 0..filled {
        bar.push('\u{2588}');
    }
    for _ in filled..width {
        bar.push('\u{2591}');
    }
    bar
}

fn fmt_percent(done: u64, target: u64) -> String {
    if target == 0 {
        return "  0%".to_string();
    }
    let percent = ((done as f64 / target as f64) * 100.0).min(100.0);
    format!("{:.0}%", percent)
}

/// Group digits with commas: 12847392 -> "12,847,392".
fn fmt_count(value: u64) -> String {
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

/// Compact counts for narrow terminals: 12847392 -> "12.8M".
fn fmt_count_short(value: u64) -> String {
    const UNITS: [(u64, &str); 4] = [
        (1_000_000_000_000, "T"),
        (1_000_000_000, "G"),
        (1_000_000, "M"),
        (1_000, "K"),
    ];
    for (scale, suffix) in UNITS {
        if value >= scale {
            let scaled = value as f64 / scale as f64;
            return if scaled < 10.0 {
                format!("{:.1}{}", scaled, suffix)
            } else {
                format!("{:.0}{}", scaled, suffix)
            };
        }
    }
    value.to_string()
}

fn fmt_bytes(value: u64) -> String {
    const UNITS: [(u64, &str); 4] = [
        (1u64 << 40, "TiB"),
        (1u64 << 30, "GiB"),
        (1u64 << 20, "MiB"),
        (1u64 << 10, "KiB"),
    ];
    for (scale, suffix) in UNITS {
        if value >= scale {
            let scaled = value as f64 / scale as f64;
            return if scaled < 10.0 {
                format!("{:.1} {}", scaled, suffix)
            } else {
                format!("{:.0} {}", scaled, suffix)
            };
        }
    }
    format!("{} B", value)
}

fn fmt_duration(duration: Duration) -> String {
    let total = duration.as_secs();
    let (hours, minutes, seconds) = (total / 3600, (total % 3600) / 60, total % 60);
    if hours > 0 {
        format!("{}h{:02}m", hours, minutes)
    } else if minutes > 0 {
        format!("{}m{:02}s", minutes, seconds)
    } else {
        format!("{}s", seconds)
    }
}

/// Push `right` to the right edge, keeping at least one space of separation.
fn pad_between(left: &str, right: &str, width: usize) -> String {
    if right.is_empty() {
        return clip(left, width);
    }
    let left_len = left.chars().count();
    let right_len = right.chars().count();
    if left_len + right_len + 1 > width {
        // No room for both: the left side carries the more important numbers.
        return clip(left, width);
    }
    let gap = width - left_len - right_len - 1;
    format!("{}{}{} ", left, " ".repeat(gap), right)
}

/// Right-align inside a reserved field so digits grow leftwards into padding.
fn rjust(text: &str, width: usize) -> String {
    let len = text.chars().count();
    if len >= width {
        return text.to_string();
    }
    format!("{}{}", " ".repeat(width - len), text)
}

/// Left-align inside a reserved field, for words rather than numbers.
fn ljust(text: &str, width: usize) -> String {
    let len = text.chars().count();
    if len >= width {
        return text.to_string();
    }
    format!("{}{}", text, " ".repeat(width - len))
}

fn digits(value: u64) -> usize {
    value.to_string().len()
}

fn clip(text: &str, width: usize) -> String {
    truncate(text, width)
}

fn truncate(text: &str, width: usize) -> String {
    if text.chars().count() <= width {
        return text.to_string();
    }
    text.chars().take(width).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample() -> Snapshot {
        Snapshot {
            elapsed: Duration::from_secs(94),
            threads: 4,
            writers: 8,
            queued: 12,
            queue_capacity: 30,
            rows: 12_847_392,
            target_rows: Some(50_000_000),
            bytes_generated: 17_800_000_000,
            bytes_uploaded: 15_781_000_000,
            target_bytes: None,
            buffered_bytes: 192 << 20,
            files_completed: 6,
            active_files: vec![
                ActiveFile { name: "part-000007.parquet".into(), bytes: 1_288_490_188 },
                ActiveFile { name: "part-000008.parquet".into(), bytes: 429_496_729 },
            ],
            rows_per_sec: 421_003.0,
            gen_bytes_per_sec: 61_000_000.0,
            upload_bytes_per_sec: 54_600_000.0,
            file_cap: Some(2 << 30),
            draining: false,
            finished: false,
        }
    }

    #[test]
    fn never_exceeds_the_terminal_width() {
        let mut snapshot = sample();
        for width in 20..=160 {
            for finished in [false, true] {
                for target in [Some(50_000_000), None] {
                    snapshot.finished = finished;
                    snapshot.target_rows = target;
                    // The wordiest state: a full queue adds "upload-bound".
                    snapshot.queued = snapshot.queue_capacity;
                    for line in render(&snapshot, width) {
                        assert!(
                            line.chars().count() <= width,
                            "width {} overflowed with line {:?} ({} chars)",
                            width,
                            line,
                            line.chars().count()
                        );
                    }
                }
            }
        }
    }

    #[test]
    fn line_count_is_stable_so_redraw_does_not_drift() {
        let snapshot = sample();
        for width in [52, 60, 80, 100, 120] {
            let first = render(&snapshot, width).len();
            let mut later = snapshot.clone();
            later.rows = 49_999_999;
            later.bytes_uploaded = 40_000_000_000;
            assert_eq!(first, render(&later, width).len(), "line count moved at width {}", width);
        }
    }

    #[test]
    fn handles_zero_and_missing_values() {
        let snapshot = Snapshot { threads: 1, queue_capacity: 30, ..Default::default() };
        let lines = render(&snapshot, 80);
        assert!(!lines.is_empty());
        for line in &lines {
            assert!(line.chars().count() <= 80);
        }
        // A zero target must not divide by zero or claim progress.
        let zero_target = Snapshot { target_rows: Some(0), ..Default::default() };
        assert!(render(&zero_target, 80).iter().any(|line| line.contains("0%")));
    }

    #[test]
    fn formats_numbers_readably() {
        assert_eq!(fmt_count(12_847_392), "12,847,392");
        assert_eq!(fmt_count(0), "0");
        assert_eq!(fmt_count_short(12_847_392), "13M");
        assert_eq!(fmt_count_short(1_500), "1.5K");
        assert_eq!(fmt_bytes(1_288_490_188), "1.2 GiB");
        assert_eq!(fmt_bytes(512), "512 B");
        assert_eq!(fmt_duration(Duration::from_secs(94)), "1m34s");
        assert_eq!(fmt_duration(Duration::from_secs(3_930)), "1h05m");
    }

    /// Not an assertion: prints the layout so it can be eyeballed.
    #[test]
    fn demo() {
        let mut snapshot = sample();
        for width in [100usize, 80, 64, 48] {
            println!("\n--- {} columns {}", width, "-".repeat(width.saturating_sub(16)));
            for line in render(&snapshot, width) {
                println!("{}", line);
            }
        }

        println!("\n--- 80 columns, no row target (unbounded stream) ---");
        snapshot.target_rows = None;
        for line in render(&snapshot, 80) {
            println!("{}", line);
        }

        println!("\n--- 80 columns, size target instead of rows ---");
        snapshot.target_bytes = Some(50 << 30);
        for line in render(&snapshot, 80) {
            println!("{}", line);
        }

        println!("\n--- 80 columns, finished ---");
        snapshot.finished = true;
        snapshot.active_files.clear();
        snapshot.files_completed = 24;
        snapshot.buffered_bytes = 0;
        for line in render(&snapshot, 80) {
            println!("{}", line);
        }
    }

    #[test]
    fn a_full_queue_is_labelled_upload_bound_not_a_stall() {
        let mut snapshot = sample();
        snapshot.queued = snapshot.queue_capacity;
        snapshot.rows_per_sec = 0.0;
        let text = render(&snapshot, 120).join("\n");
        assert!(text.contains("upload-bound"), "{}", text);

        snapshot.draining = true;
        let draining = render(&snapshot, 120).join("\n");
        assert!(draining.contains("draining"), "{}", draining);
        assert!(!draining.contains("upload-bound"), "draining wins: {}", draining);

        snapshot.draining = false;
        snapshot.queued = 3;
        let running = render(&snapshot, 120).join("\n");
        assert!(!running.contains("upload-bound"), "{}", running);
    }

    /// The point of the reserved widths: labels must not move as numbers grow.
    #[test]
    fn field_positions_do_not_move_as_numbers_change() {
        let small = Snapshot {
            elapsed: Duration::from_secs(3),
            threads: 4,
            writers: 8,
            queued: 0,
            queue_capacity: 30,
            rows: 7,
            target_rows: Some(50_000_000),
            bytes_generated: 12,
            bytes_uploaded: 12,
            target_bytes: None,
            buffered_bytes: 0,
            files_completed: 0,
            active_files: vec![ActiveFile { name: "part-w00-000001.parquet".into(), bytes: 3 }],
            rows_per_sec: 4.0,
            gen_bytes_per_sec: 0.0,
            upload_bytes_per_sec: 9.0,
            file_cap: Some(2 << 30),
            draining: false,
            finished: false,
        };
        let large = Snapshot {
            elapsed: Duration::from_secs(9999),
            rows: 49_999_999,
            bytes_generated: 900_000_000_000,
            bytes_uploaded: 900_000_000_000,
            buffered_bytes: 999 << 20,
            files_completed: 987,
            queued: 30,
            rows_per_sec: 9_876_543.0,
            upload_bytes_per_sec: 987_654_321.0,
            active_files: vec![ActiveFile {
                name: "part-w00-000001.parquet".into(),
                bytes: 2_000_000_000,
            }],
            ..small.clone()
        };

        for width in [80usize, 100, 120] {
            let a = render(&small, width);
            let b = render(&large, width);
            assert_eq!(a.len(), b.len(), "line count moved at width {}", width);
            for (line_a, line_b) in a.iter().zip(&b) {
                for label in ["threads", "queue", "buffered", "sent", "rate", "done"] {
                    assert_eq!(
                        line_a.find(label),
                        line_b.find(label),
                        "`{}` moved at width {}:\n  {:?}\n  {:?}",
                        label,
                        width,
                        line_a,
                        line_b
                    );
                }
            }
        }
    }

    #[test]
    fn lines_share_one_left_column() {
        let snapshot = sample();
        let lines = render(&snapshot, 110);
        // Every label sits in the same fixed-width cell, so the content after
        // it starts at one column on every line.
        let starts: Vec<usize> = lines
            .iter()
            .map(|line| line.len() - line.trim_start().len())
            .collect();
        assert!(starts.iter().all(|start| *start == starts[0]), "{:?}", lines);
        for line in &lines {
            let cell: String = line.chars().take(W_LABEL + 2).collect();
            assert_eq!(cell.chars().count(), W_LABEL + 2);
            assert!(cell.starts_with(' ') && cell.ends_with(' '), "{:?}", cell);
        }
    }
}
