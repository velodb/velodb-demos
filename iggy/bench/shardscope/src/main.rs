// shardscope — live visualization of Iggy's thread-per-core shards + io_uring.
//
// Pure /proc for per-shard CPU%, pinned core, ring count and disk throughput;
// optional bpftrace overlay for the live io_uring submit/complete *rate* (the
// meaningful "millions of SQEs/s flowing through the rings" number). `--snapshot`
// prints one text frame so it works over a plain SSH pipe.

use std::collections::{HashMap, VecDeque};
use std::fs;
use std::io::{BufRead, BufReader};
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use clap::Parser;
use crossterm::event::{self, Event, KeyCode};
use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Bar, BarChart, BarGroup, Block, Borders, Paragraph, Sparkline};

const USER_HZ: f64 = 100.0;
const HIST: usize = 120;

#[derive(Parser)]
#[command(name = "shardscope", about = "Visualize Iggy thread-per-core + io_uring")]
struct Args {
    /// PID of iggy-server (auto-detected via pgrep if omitted).
    #[arg(long)]
    pid: Option<i32>,
    /// Print one text frame and exit instead of the full-screen TUI.
    #[arg(long)]
    snapshot: bool,
    /// Refresh interval in milliseconds.
    #[arg(long, default_value_t = 500)]
    interval: u64,
    /// Overlay live io_uring submit/complete rate via `sudo bpftrace`.
    #[arg(long)]
    bpftrace: bool,
    /// Only show threads whose name starts with this (Iggy names them shard-N).
    #[arg(long, default_value = "shard")]
    filter: String,
}

struct Shard {
    name: String,
    core: i32,
    cpu_pct: u64,
}

struct Frame {
    pid: i32,
    rings: usize,
    shards: Vec<Shard>,
    agg_cpu: u64,
    write_mb_s: f64,
    sqe_s: u64,
    cqe_s: u64,
    bpf_on: bool,
}

fn detect_pid() -> Option<i32> {
    let out = Command::new("pgrep").args(["-x", "iggy-server"]).output().ok()?;
    String::from_utf8_lossy(&out.stdout)
        .split_whitespace()
        .next()
        .and_then(|s| s.parse().ok())
}

fn read_thread(pid: i32, tid: i32) -> Option<(String, i32, u64)> {
    let content = fs::read_to_string(format!("/proc/{pid}/task/{tid}/stat")).ok()?;
    let open = content.find('(')?;
    let close = content.rfind(')')?;
    let name = content[open + 1..close].to_string();
    let f: Vec<&str> = content[close + 2..].split_whitespace().collect();
    let utime: u64 = f.get(11)?.parse().ok()?;
    let stime: u64 = f.get(12)?.parse().ok()?;
    let core: i32 = f.get(36).and_then(|s| s.parse().ok()).unwrap_or(-1);
    Some((name, core, utime + stime))
}

fn count_iouring_rings(pid: i32) -> usize {
    fs::read_dir(format!("/proc/{pid}/fd"))
        .map(|rd| {
            rd.flatten()
                .filter(|e| {
                    fs::read_link(e.path())
                        .map(|t| t.to_string_lossy().contains("io_uring"))
                        .unwrap_or(false)
                })
                .count()
        })
        .unwrap_or(0)
}

fn read_write_bytes(pid: i32) -> u64 {
    fs::read_to_string(format!("/proc/{pid}/io"))
        .ok()
        .and_then(|c| {
            c.lines()
                .find_map(|l| l.strip_prefix("write_bytes:").and_then(|v| v.trim().parse().ok()))
        })
        .unwrap_or(0)
}

fn spawn_bpftrace() -> Option<(Arc<Mutex<(u64, u64)>>, Child)> {
    // Plain `@s++` counters (not count()) so printf %d accepts them.
    let prog = "tracepoint:io_uring:io_uring_submit_sqe { @s++; } \
                tracepoint:io_uring:io_uring_complete { @c++; } \
                interval:s:1 { printf(\"R %d %d\\n\", @s, @c); @s=0; @c=0; }";
    let mut child = Command::new("sudo")
        .args(["bpftrace", "-e", prog])
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    let out = child.stdout.take()?;
    let shared = Arc::new(Mutex::new((0u64, 0u64)));
    let sink = shared.clone();
    std::thread::spawn(move || {
        for line in BufReader::new(out).lines().map_while(Result::ok) {
            let p: Vec<&str> = line.split_whitespace().collect();
            if p.len() == 3 && p[0] == "R" {
                if let (Ok(s), Ok(c)) = (p[1].parse(), p[2].parse()) {
                    *sink.lock().unwrap() = (s, c);
                }
            }
        }
    });
    Some((shared, child))
}

fn collect(
    pid: i32,
    prev: &mut HashMap<i32, u64>,
    dt: Duration,
    filter: &str,
    bpf: &Option<Arc<Mutex<(u64, u64)>>>,
) -> Frame {
    let dt_s = dt.as_secs_f64().max(1e-3);
    let mut shards = Vec::new();
    let mut agg = 0u64;
    if let Ok(rd) = fs::read_dir(format!("/proc/{pid}/task")) {
        for entry in rd.flatten() {
            let tid: i32 = match entry.file_name().to_string_lossy().parse() {
                Ok(t) => t,
                Err(_) => continue,
            };
            if let Some((name, core, ticks)) = read_thread(pid, tid) {
                let last = prev.insert(tid, ticks).unwrap_or(ticks);
                let cpu = ((ticks.saturating_sub(last) as f64 / USER_HZ / dt_s) * 100.0).round() as u64;
                agg += cpu;
                if name.starts_with(filter) {
                    shards.push(Shard { name, core, cpu_pct: cpu });
                }
            }
        }
    }
    shards.sort_by_key(|s| s.core);
    let (sqe, cqe) = bpf.as_ref().map(|m| *m.lock().unwrap()).unwrap_or((0, 0));
    Frame {
        pid,
        rings: count_iouring_rings(pid),
        shards,
        agg_cpu: agg,
        write_mb_s: 0.0,
        sqe_s: sqe,
        cqe_s: cqe,
        bpf_on: bpf.is_some(),
    }
}

fn human(n: u64) -> String {
    if n >= 1_000_000 {
        format!("{:.2}M", n as f64 / 1e6)
    } else if n >= 1_000 {
        format!("{:.1}K", n as f64 / 1e3)
    } else {
        n.to_string()
    }
}

fn cpu_color(p: u64) -> Color {
    if p >= 85 {
        Color::Red
    } else if p >= 55 {
        Color::Yellow
    } else {
        Color::Green
    }
}

fn print_snapshot(f: &Frame) {
    println!(
        "iggy-server pid {}   io_uring rings {}   shards {}   agg CPU {}%   write {:.0} MB/s",
        f.pid, f.rings, f.shards.len(), f.agg_cpu, f.write_mb_s
    );
    if f.bpf_on {
        println!("io_uring: submit {}/s   complete {}/s", human(f.sqe_s), human(f.cqe_s));
    } else {
        println!("io_uring: submit n/a (run with --bpftrace)");
    }
    for s in &f.shards {
        let bar = "█".repeat((s.cpu_pct.min(100) / 4) as usize);
        println!("  {:<9} core {:>2}  {:>3}%  {}", s.name, s.core, s.cpu_pct, bar);
    }
}

fn sparkline_block<'a>(title: String, data: &'a [u64], color: Color) -> Sparkline<'a> {
    Sparkline::default()
        .block(Block::default().borders(Borders::ALL).title(Span::styled(
            title,
            Style::default().fg(color).add_modifier(Modifier::BOLD),
        )))
        .data(data)
        .style(Style::default().fg(color))
}

fn ui(frame: &mut ratatui::Frame, f: &Frame, sqe_hist: &[u64], write_hist: &[u64]) {
    // Header, then a 1/3 (io_uring + NVMe sparklines) : 2/3 (per-shard bars) split.
    let rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(3), Constraint::Percentage(32), Constraint::Min(0)])
        .split(frame.area());

    // Header banner.
    let header = Paragraph::new(Line::from(vec![
        Span::styled(" shardscope ", Style::default().fg(Color::Black).bg(Color::Cyan).add_modifier(Modifier::BOLD)),
        Span::raw("  Iggy thread-per-core + io_uring   "),
        Span::styled(format!("pid {}", f.pid), Style::default().fg(Color::Gray)),
        Span::raw("   "),
        Span::styled(format!("io_uring rings: {}", f.rings), Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD)),
        Span::raw("   "),
        Span::styled(format!("shards: {}", f.shards.len()), Style::default().fg(Color::Cyan)),
        Span::raw("   "),
        Span::styled(format!("agg CPU: {}%", f.agg_cpu), Style::default().fg(Color::Yellow)),
        Span::styled("        q to quit", Style::default().fg(Color::DarkGray)),
    ]))
    .block(Block::default().borders(Borders::ALL));
    frame.render_widget(header, rows[0]);

    // Middle: io_uring submit + complete sparklines | disk write sparkline.
    let mid = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(34), Constraint::Percentage(33), Constraint::Percentage(33)])
        .split(rows[1]);

    let submit_title = if f.bpf_on {
        format!(" io_uring submit  {}/s ", human(f.sqe_s))
    } else {
        " io_uring submit  (--bpftrace) ".to_string()
    };
    let complete_title = if f.bpf_on {
        format!(" io_uring complete  {}/s ", human(f.cqe_s))
    } else {
        " io_uring complete  (--bpftrace) ".to_string()
    };
    frame.render_widget(sparkline_block(submit_title, sqe_hist, Color::Cyan), mid[0]);
    frame.render_widget(sparkline_block(complete_title, sqe_hist, Color::LightCyan), mid[1]);
    frame.render_widget(
        sparkline_block(format!(" NVMe write  {:.0} MB/s ", f.write_mb_s), write_hist, Color::Magenta),
        mid[2],
    );

    // Bottom: per-shard CPU bar chart, one bar per pinned core, color-graded.
    render_shards(frame, f, rows[2]);
}

fn render_shards(frame: &mut ratatui::Frame, f: &Frame, area: Rect) {
    let n = f.shards.len().max(1) as u16;
    let gap = 1u16;
    // Widen bars to fill the panel width instead of a fixed 6 cols.
    let inner = area.width.saturating_sub(2);
    let bar_w = inner.saturating_sub(gap * n).checked_div(n).unwrap_or(6).clamp(3, 30);
    // Auto-scale the y-axis to the busiest shard (floored), so low CPU still
    // fills the panel height instead of leaving most of it empty.
    let peak = f.shards.iter().map(|s| s.cpu_pct).max().unwrap_or(10).max(8);

    let bars: Vec<Bar> = f
        .shards
        .iter()
        .map(|s| {
            let v = s.cpu_pct;
            Bar::default()
                .value(v)
                .label(Line::from(format!("c{}", s.core)))
                .text_value(format!("{v}%"))
                .style(Style::default().fg(cpu_color(v)))
                .value_style(Style::default().fg(Color::Black).bg(cpu_color(v)))
        })
        .collect();

    let chart = BarChart::default()
        .block(Block::default().borders(Borders::ALL).title(format!(
            " per-shard CPU%  (auto-scale 0–{peak}%)  —  each bar = one shard on one core "
        )))
        .data(BarGroup::default().bars(&bars))
        .bar_width(bar_w)
        .bar_gap(gap)
        .max(peak);
    frame.render_widget(chart, area);
}

fn main() -> std::io::Result<()> {
    let args = Args::parse();
    let pid = args.pid.or_else(detect_pid).expect("iggy-server PID not found; pass --pid");
    let interval = Duration::from_millis(args.interval);

    let mut prev: HashMap<i32, u64> = HashMap::new();
    let mut prev_write = read_write_bytes(pid);
    let bpf_pair = if args.bpftrace { spawn_bpftrace() } else { None };
    let bpf = bpf_pair.as_ref().map(|(m, _)| m.clone());

    if args.snapshot {
        collect(pid, &mut prev, interval, &args.filter, &bpf);
        std::thread::sleep(interval);
        // bpftrace needs a few seconds to compile + attach before it emits a
        // rate; wait (bounded) for the first non-zero reading in snapshot mode.
        if let Some(m) = &bpf {
            for _ in 0..16 {
                if m.lock().unwrap().0 > 0 {
                    break;
                }
                std::thread::sleep(Duration::from_millis(500));
            }
        }
        let w1 = read_write_bytes(pid);
        let mut f = collect(pid, &mut prev, interval, &args.filter, &bpf);
        f.write_mb_s = w1.saturating_sub(prev_write) as f64 / 1.048e6 / interval.as_secs_f64();
        print_snapshot(&f);
        return Ok(());
    }

    let mut sqe_hist: VecDeque<u64> = VecDeque::from(vec![0; HIST]);
    let mut write_hist: VecDeque<u64> = VecDeque::from(vec![0; HIST]);

    let mut terminal = ratatui::init();
    collect(pid, &mut prev, interval, &args.filter, &bpf);
    let mut last = Instant::now();
    loop {
        if event::poll(interval)? {
            if let Event::Key(k) = event::read()? {
                if matches!(k.code, KeyCode::Char('q') | KeyCode::Esc) {
                    break;
                }
            }
        }
        let dt = last.elapsed();
        last = Instant::now();
        let mut f = collect(pid, &mut prev, dt, &args.filter, &bpf);
        let w = read_write_bytes(pid);
        f.write_mb_s = w.saturating_sub(prev_write) as f64 / 1.048e6 / dt.as_secs_f64();
        prev_write = w;

        sqe_hist.pop_front();
        sqe_hist.push_back(f.sqe_s);
        write_hist.pop_front();
        write_hist.push_back(f.write_mb_s as u64);

        let sqe_slice: Vec<u64> = sqe_hist.iter().copied().collect();
        let write_slice: Vec<u64> = write_hist.iter().copied().collect();
        terminal.draw(|fr| ui(fr, &f, &sqe_slice, &write_slice))?;
    }
    ratatui::restore();
    Ok(())
}
