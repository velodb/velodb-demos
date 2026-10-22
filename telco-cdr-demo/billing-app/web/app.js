"use strict";

const $ = (id) => document.getElementById(id);
const state = { month: "", page: 1, total: 0, lastJobStatus: null, lastJobId: null };
const STEP_NAMES = { dedup: "Deduplicate CDRs", bill: "Bill run", inject: "Inject duplicate CDRs" };
const STEP_NO = { dedup: "Step 1", inject: "Step 1 · simulation", bill: "Step 2" };

// ---------------------------------------------------------------------------------------------
// formatting
// ---------------------------------------------------------------------------------------------
const nf = new Intl.NumberFormat("en-US");
const fmtInt = (n) => (n == null ? "–" : nf.format(Math.round(Number(n))));
const fmtMB = (n) => (n == null ? "–" : Number(n).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 }));
const fmtMoney = (n, d = 2) => (n == null ? "–" : "$" + Number(n).toLocaleString("en-US", { minimumFractionDigits: d, maximumFractionDigits: d }));
function compact(n, money = false) {
  if (n == null) return "–";
  n = Number(n);
  const abs = Math.abs(n);
  const p = money ? "$" : "";
  if (abs >= 1e9) return p + (n / 1e9).toFixed(2) + "B";
  if (abs >= 1e6) return p + (n / 1e6).toFixed(2) + "M";
  if (abs >= 1e4) return p + (n / 1e3).toFixed(1) + "K";
  return money ? fmtMoney(n, abs < 100 ? 2 : 0) : fmtInt(n);
}
function dur(sec) {
  if (sec == null || sec < 0) return "–";
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return h ? `${h}h ${m}m` : m ? `${m}m ${s}s` : `${s}s`;
}
const phone = (s) => { s = String(s); return s.length === 10 && s.startsWith("65") ? `+65 ${s.slice(2, 6)} ${s.slice(6)}` : s; };
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

async function api(path, opts = {}) {
  const r = await fetch(path, opts);
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(body.error || r.statusText);
  return body;
}

// ---------------------------------------------------------------------------------------------
// tooltip
// ---------------------------------------------------------------------------------------------
const tip = $("tip");
function showTip(ev, title, rows) {
  tip.innerHTML = `<div class="tt-title">${esc(title)}</div>` +
    rows.map(([color, k, v]) => `<div class="tt-row"><span>${color ? `<i style="background:${color}"></i>` : ""}${esc(k)}</span><b>${esc(v)}</b></div>`).join("");
  tip.hidden = false;
  const pad = 14, w = tip.offsetWidth, h = tip.offsetHeight;
  let x = ev.clientX + pad, y = ev.clientY + pad;
  if (x + w > innerWidth - 8) x = ev.clientX - w - pad;
  if (y + h > innerHeight - 8) y = ev.clientY - h - pad;
  tip.style.left = x + "px";
  tip.style.top = y + "px";
}
const hideTip = () => (tip.hidden = true);
function bindTips(root, getter) {
  root.querySelectorAll("[data-tip]").forEach((el) => {
    const i = Number(el.dataset.tip);
    el.addEventListener("mousemove", (ev) => { const [t, rows] = getter(i); showTip(ev, t, rows); });
    el.addEventListener("mouseleave", hideTip);
  });
}
const cssVar = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

// ---------------------------------------------------------------------------------------------
// charts (plain SVG)
// ---------------------------------------------------------------------------------------------
function niceMax(v) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  const f = v / p;
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * p;
}

// bar with a 4px rounded data end and a square baseline
function hbarPath(x, y, w, h, r = 4) {
  if (w <= 0) return "";
  r = Math.min(r, w, h / 2);
  return `M${x},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h - r}Q${x + w},${y + h} ${x + w - r},${y + h}H${x}Z`;
}
function vbarPath(x, y, w, h, r = 4) {
  if (h <= 0) return "";
  r = Math.min(r, h, w / 2);
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}

const PLAN_SERIES = [
  { key: "recurring", label: "Plan fees", color: "--series-1" },
  { key: "voice", label: "Voice usage", color: "--series-2" },
  { key: "sms", label: "SMS", color: "--series-3" },
  { key: "data", label: "Data overage", color: "--series-4" },
  { key: "roaming", label: "Roaming", color: "--series-5" },
];

function stackedPlanChart(el, plans) {
  const width = el.clientWidth || 600;
  const labelW = 118, valueW = 70, rowH = 34, barH = 20, top = 6, gap = 2;
  const height = top + plans.length * rowH + 24;
  const plotW = Math.max(width - labelW - valueW, 80);
  const totals = plans.map((p) => PLAN_SERIES.reduce((s, k) => s + Number(p[k.key] || 0), 0));
  const max = niceMax(Math.max(...totals, 1));
  const x = (v) => labelW + (v / max) * plotW;
  const colors = PLAN_SERIES.map((s) => cssVar(s.color));
  let svg = `<svg width="${width}" height="${height}" role="img" aria-label="Revenue by rate plan">`;
  for (let i = 0; i <= 4; i++) {
    const v = (max / 4) * i, gx = x(v);
    svg += `<line class="gridline" x1="${gx}" x2="${gx}" y1="${top}" y2="${height - 20}"/>`;
    svg += `<text x="${gx}" y="${height - 6}" text-anchor="middle">${compact(v, true)}</text>`;
  }
  plans.forEach((p, i) => {
    const y = top + i * rowH + (rowH - barH) / 2;
    svg += `<g class="row" data-tip="${i}"><rect class="hover-band" x="0" y="${top + i * rowH}" width="${width}" height="${rowH}"/>`;
    svg += `<text class="label" x="${labelW - 10}" y="${y + barH / 2 + 4}" text-anchor="end">${esc(p.rate_plan)}</text>`;
    let acc = 0;
    const segs = PLAN_SERIES.map((s, si) => ({ v: Number(p[s.key] || 0), si })).filter((s) => s.v > 0);
    segs.forEach((s, k) => {
      const x0 = x(acc) + (k > 0 ? gap / 2 : 0), x1 = x(acc + s.v) - (k < segs.length - 1 ? gap / 2 : 0);
      const w = Math.max(x1 - x0, 0);
      svg += k === segs.length - 1
        ? `<path d="${hbarPath(x0, y, w, barH)}" fill="${colors[s.si]}"/>`
        : `<rect x="${x0}" y="${y}" width="${w}" height="${barH}" fill="${colors[s.si]}"/>`;
      acc += s.v;
    });
    svg += `<text class="value" x="${x(totals[i]) + 6}" y="${y + barH / 2 + 4}">${compact(totals[i], true)}</text>`;
    svg += `<rect class="hit" x="0" y="${top + i * rowH}" width="${width}" height="${rowH}"/></g>`;
  });
  svg += `<line class="baseline" x1="${labelW}" x2="${labelW}" y1="${top}" y2="${height - 20}"/></svg>`;
  el.innerHTML = svg;
  bindTips(el, (i) => {
    const p = plans[i];
    return [`${p.rate_plan} · ${fmtInt(p.bills)} bills`, [
      ...PLAN_SERIES.map((s, si) => [colors[si], s.label, fmtMoney(p[s.key], 0)]),
      [null, "Total excl. GST", fmtMoney(totals[i], 0)],
      [null, "ARPU incl. GST", fmtMoney(p.arpu)],
    ]];
  });
  $("plan-legend").innerHTML = PLAN_SERIES.map((s, si) => `<span><i style="background:${colors[si]}"></i>${s.label}</span>`).join("");
}

function columnChart(el, points, { label, value, color = "--series-1", fmt = fmtInt, fmtTick = compact, tipTitle, tipRows, xEvery = 1, height = 200 }) {
  const width = el.clientWidth || 400;
  const left = 46, right = 8, top = 16, bottom = 24;
  const plotW = width - left - right, plotH = height - top - bottom;
  const max = niceMax(Math.max(...points.map(value), 0));
  const band = plotW / Math.max(points.length, 1);
  const barW = Math.min(24, Math.max(band - 2, 1));
  const y = (v) => top + plotH - (v / max) * plotH;
  const fill = cssVar(color);
  let svg = `<svg width="${width}" height="${height}" role="img">`;
  for (let i = 0; i <= 4; i++) {
    const v = (max / 4) * i, gy = y(v);
    svg += `<line class="${i ? "gridline" : "baseline"}" x1="${left}" x2="${width - right}" y1="${gy}" y2="${gy}"/>`;
    svg += `<text x="${left - 6}" y="${gy + 4}" text-anchor="end">${fmtTick(v)}</text>`;
  }
  let maxI = 0;
  points.forEach((p, i) => { if (value(p) > value(points[maxI])) maxI = i; });
  points.forEach((p, i) => {
    const cx = left + band * i + band / 2, v = value(p), by = y(v);
    svg += `<g class="col" data-tip="${i}"><rect class="hover-band" x="${left + band * i}" y="${top}" width="${band}" height="${plotH}"/>`;
    svg += `<path d="${vbarPath(cx - barW / 2, by, barW, top + plotH - by)}" fill="${fill}"/>`;
    if (i % xEvery === 0) svg += `<text x="${cx}" y="${height - 8}" text-anchor="middle">${esc(label(p))}</text>`;
    if (i === maxI && v > 0) svg += `<text class="value" x="${cx}" y="${by - 5}" text-anchor="middle">${fmt(v)}</text>`;
    svg += `<rect class="hit" x="${left + band * i}" y="${top}" width="${band}" height="${plotH}"/></g>`;
  });
  el.innerHTML = svg + "</svg>";
  bindTips(el, (i) => [tipTitle(points[i]), tipRows(points[i], fill)]);
}

function hbarChart(el, rows, { label, value, fmt, color = "--series-1" }) {
  const width = el.clientWidth || 400;
  const labelW = 124, valueW = 84, rowH = 24, barH = 14;
  const height = rows.length * rowH + 4;
  const max = Math.max(...rows.map(value), 1);
  const plotW = Math.max(width - labelW - valueW, 60);
  const fill = cssVar(color);
  let svg = `<svg width="${width}" height="${height}" role="img">`;
  rows.forEach((r, i) => {
    const y = 2 + i * rowH, w = (value(r) / max) * plotW;
    svg += `<text class="label" x="${labelW - 8}" y="${y + rowH / 2 + 4}" text-anchor="end">${esc(label(r))}</text>`;
    svg += `<path d="${hbarPath(labelW, y + (rowH - barH) / 2, Math.max(w, 1), barH)}" fill="${fill}"/>`;
    svg += `<text class="value" x="${labelW + Math.max(w, 1) + 6}" y="${y + rowH / 2 + 4}">${esc(fmt(value(r)))}</text>`;
  });
  el.innerHTML = svg + `<line class="baseline" x1="${labelW}" x2="${labelW}" y1="0" y2="${height}"/></svg>`;
}

// ---------------------------------------------------------------------------------------------
// overview + pipeline cards
// ---------------------------------------------------------------------------------------------
async function loadOverview(refresh = false) {
  try {
    const o = await api(`/api/overview?month=${encodeURIComponent(state.month)}${refresh ? "&refresh=1" : ""}`);
    $("conn").textContent = `${o.database} @ ${o.host.split(".")[0]}`;
    $("conn").title = `${o.host} / ${o.database} · ${o.parallel} parallel statements per step`;
    const sel = $("month");
    const months = o.months || [];
    sel.innerHTML = months.map((m) => `<option value="${m.month}">${m.month} (${m.days} days)</option>`).join("");
    if (!state.month) state.month = o.month;
    sel.value = state.month;
    const st = o.stats || {};
    for (const k of ["call_log", "dedup", "data_stream_log", "bills"]) {
      const s = st[k];
      $("st-" + k).innerHTML = s && s.rows != null ? `${compact(s.rows)}<small>${s.ms}ms</small>` : s && s.error ? "err" : "–";
      if (s && s.error) $("st-" + k).title = s.error;
    }
  } catch (e) {
    $("conn").textContent = "error: " + e.message;
  }
}

// ---------------------------------------------------------------------------------------------
// job panel
// ---------------------------------------------------------------------------------------------
async function startStep(step) {
  if (!state.month) return;
  if (step === "inject" && !confirm(`Insert duplicate rows into call_log for ${state.month}? This modifies the source table.`)) return;
  try {
    await api(`/api/steps/${step}`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ month: state.month, dup_rate: Number($("dup-rate").value) }),
    });
    $("job").hidden = false;
    $("job").scrollIntoView({ behavior: "smooth", block: "start" });
    pollJob();
  } catch (e) {
    alert(e.message);
  }
}

let pollTimer = null;
async function pollJob() {
  clearTimeout(pollTimer);
  let j;
  try { j = await api("/api/job"); } catch { pollTimer = setTimeout(pollJob, 3000); return; }
  const running = j.status === "running";
  if (j.status !== "idle") renderJob(j);
  document.querySelectorAll("[data-run]").forEach((b) => (b.disabled = running));
  document.querySelectorAll(".step").forEach((s) => s.classList.toggle("active", running && (s.dataset.step === j.step || (j.step === "inject" && s.dataset.step === "dedup"))));
  if (state.lastJobId === j.id && state.lastJobStatus === "running" && !running) {
    // a step just finished: refresh everything downstream
    if (j.month && j.month !== state.month) state.month = j.month;
    await loadOverview(true);
    loadDashboard();
    loadRuns();
  }
  if (state.lastJobId !== j.id && running) loadRuns();
  state.job = j;
  state.lastJobId = j.id;
  state.lastJobStatus = j.status;
  pollTimer = setTimeout(pollJob, running ? 1000 : 5000);
}

let renderedLogs = 0, renderedJob = null;
function renderJob(j) {
  $("job").hidden = false;
  $("job-eyebrow").textContent = `${STEP_NO[j.step] || ""} · ${j.month} · ${j.id}`;
  $("job-title").textContent = STEP_NAMES[j.step] || j.step;
  const pill = $("job-status");
  pill.className = "status-pill " + j.status;
  pill.textContent = j.status;
  $("job-cancel").hidden = j.status !== "running";
  const pctDone = j.total ? Math.round((j.done / j.total) * 100) : 0;
  $("job-bar").style.width = (j.status === "succeeded" ? 100 : pctDone) + "%";
  $("job-progress").setAttribute("aria-valuenow", pctDone);
  const rate = j.elapsed_sec > 0 && j.rows_in ? j.rows_in / j.elapsed_sec : 0;
  const metas = [
    ["Progress", `${j.done} / ${j.total} tasks · ${pctDone}%`],
    ["Elapsed", dur(j.elapsed_sec)],
  ];
  if (j.status === "running") metas.push(["ETA", j.eta_sec >= 0 ? dur(j.eta_sec) : "estimating…"]);
  if (j.step === "dedup") metas.push(["CDRs in", fmtInt(j.rows_in)], ["Unique", fmtInt(j.rows_out)], ["Duplicates", fmtInt(j.rows_in - j.rows_out)], ["Throughput", rate ? compact(rate) + " rows/s" : "–"]);
  if (j.step === "bill") metas.push(["Bills written", fmtInt(j.rows_out)]);
  if (j.step === "inject") metas.push(["Duplicates injected", fmtInt(j.rows_out)]);
  if (j.running && j.running.length) metas.push(["Running", j.running.join(", ")]);
  $("job-meta").innerHTML = metas.map(([k, v]) => `<span>${k} <b>${esc(v)}</b></span>`).join("");

  const sum = $("job-summary");
  if (j.status !== "running") {
    sum.hidden = false;
    sum.className = "job-summary " + j.status;
    sum.textContent = j.status === "failed" ? `Failed: ${j.error}` : j.summary?.headline ? `${j.summary.headline} · ${dur(j.elapsed_sec)}` : `${j.status} after ${dur(j.elapsed_sec)}`;
  } else sum.hidden = true;

  $("job-tasks").innerHTML = (j.tasks || []).map((t) => {
    let detail = t.status;
    if (t.status === "done") {
      if (j.step === "dedup") detail = `${compact(t.in)} → ${compact(t.out)}`;
      else detail = `${compact(t.out)} rows`;
      detail += ` · ${(t.ms / 1000).toFixed(1)}s`;
    }
    return `<div class="task ${t.status}" title="${esc(t.name)}: ${esc(detail)}"><div class="t-name">${esc(t.name)}</div><div class="t-state">${esc(detail)}</div></div>`;
  }).join("");

  if (renderedJob !== j.id) { $("job-log").innerHTML = ""; renderedLogs = 0; renderedJob = j.id; }
  const logs = j.logs || [];
  if (logs.length < renderedLogs) { $("job-log").innerHTML = ""; renderedLogs = 0; }
  const box = $("job-log");
  const atBottom = box.scrollTop + box.clientHeight >= box.scrollHeight - 20;
  for (const l of logs.slice(renderedLogs)) {
    const li = document.createElement("li");
    li.className = l.level;
    const meta = l.sql ? `<span class="l-meta">${l.ms != null ? (l.ms / 1000).toFixed(2) + "s" : ""}${l.rows ? " · " + fmtInt(l.rows) + " rows" : ""}</span>` : "";
    li.innerHTML = l.sql
      ? `<details><summary><span class="l-t">${l.t}</span>${esc(l.msg)}${meta}</summary><pre>${esc(l.sql)}</pre></details>`
      : `<span class="l-t">${l.t}</span>${esc(l.msg)}`;
    box.appendChild(li);
  }
  renderedLogs = logs.length;
  $("log-count").textContent = `(${logs.filter((l) => l.sql).length})`;
  if (atBottom) box.scrollTop = box.scrollHeight;
}

// ---------------------------------------------------------------------------------------------
// dashboard
// ---------------------------------------------------------------------------------------------
async function loadDashboard() {
  if (!state.month) return;
  $("dash-month").textContent = state.month;
  let s;
  try { s = await api(`/api/bills/summary?month=${state.month}`); } catch (e) { $("dash-ms").textContent = e.message; return; }
  const k = s.kpi || {};
  const has = Number(k.bills) > 0;
  if (!has) {
    const j = state.job;
    const busy = j && j.status === "running" && j.month === state.month;
    $("dash-empty").textContent = busy && j.step === "bill"
      ? `Bill run for ${state.month} is in progress (${j.done}/${j.total} slices): the month's bills were cleared and are being rewritten.`
      : busy
        ? `${STEP_NAMES[j.step] || j.step} for ${state.month} is in progress; run the bill run when it finishes.`
        : `No bills for this month yet. Run step 1 and step 2 for ${state.month}.`;
  }
  $("dash-empty").hidden = has;
  $("dash-body").hidden = !has;
  $("st-revenue").textContent = has ? compact(k.revenue, true) : "–";
  $("st-arpu").textContent = has ? fmtMoney(k.arpu) : "–";
  if (!has) return;
  $("dash-ms").textContent = `summary queries ${s.ms}ms · run ${k.bill_run_id} · generated ${k.generated_at}`;
  $("k-revenue").textContent = compact(k.revenue, true);
  $("k-revenue-sub").textContent = `${compact(k.recurring, true)} plan fees · ${compact(k.tax, true)} GST`;
  $("k-bills").textContent = compact(k.bills);
  $("k-bills-sub").textContent = `${compact(k.bills_with_usage)} with usage charges`;
  $("k-arpu").textContent = fmtMoney(k.arpu);
  $("k-overage").textContent = compact(k.overage, true);
  $("k-roaming").textContent = compact(k.roaming, true);
  $("k-roaming-sub").textContent = `${compact(k.roaming_bills)} bills roamed`;

  const plans = s.plans || [];
  const planSel = $("f-plan");
  const cur = planSel.value;
  planSel.innerHTML = `<option value="">All plans</option>` + plans.map((p) => `<option>${esc(p.rate_plan)}</option>`).slice().sort().join("");
  planSel.value = cur;
  stackedPlanChart($("plan-chart"), plans);
  $("plan-table").innerHTML = `<div class="table-wrap"><table class="grid"><thead><tr><th>Plan</th><th class="num">Bills</th>${PLAN_SERIES.map((x) => `<th class="num">${x.label}</th>`).join("")}<th class="num">GST</th><th class="num">Revenue</th><th class="num">ARPU</th></tr></thead><tbody>` +
    plans.map((p) => `<tr><td>${esc(p.rate_plan)}</td><td class="num">${fmtInt(p.bills)}</td>${PLAN_SERIES.map((x) => `<td class="num">${fmtMoney(p[x.key], 0)}</td>`).join("")}<td class="num">${fmtMoney(p.tax, 0)}</td><td class="num">${fmtMoney(p.revenue, 0)}</td><td class="num">${fmtMoney(p.arpu)}</td></tr>`).join("") +
    `</tbody></table></div>`;

  const counts = new Map((s.histogram || []).map((h) => [Number(h.bucket), Number(h.bills)]));
  const hist = Array.from({ length: 31 }, (_, i) => ({ bucket: i * 10, bills: counts.get(i * 10) || 0 }));
  columnChart($("hist-chart"), hist, {
    label: (p) => (p.bucket >= 300 ? "300+" : "$" + p.bucket),
    value: (p) => Number(p.bills),
    xEvery: 5,
    tipTitle: (p) => (p.bucket >= 300 ? "$300 and above" : `$${p.bucket} – $${p.bucket + 9.99}`),
    tipRows: (p, c) => [[c, "Bills", fmtInt(p.bills)]],
    height: 240,
  });
  state.page = 1;
  searchBills();
}

async function searchBills() {
  const params = new URLSearchParams({
    month: state.month, q: $("f-q").value, plan: $("f-plan").value, type: $("f-type").value,
    roaming: $("f-roaming").checked ? "1" : "", min_total: $("f-min").value, sort: $("f-sort").value, page: state.page,
  });
  const tbody = $("bills");
  try {
    const r = await api("/api/bills?" + params);
    state.total = r.total;
    tbody.innerHTML = r.rows.length ? r.rows.map((b) => `<tr class="clickable" data-msisdn="${b.msisdn}">
      <td>${esc(b.bill_no)}</td><td>${phone(b.msisdn)} ${b.roaming ? '<span class="tag roam">roamed</span>' : ""}</td>
      <td><span class="tag">${esc(b.rate_plan)}</span></td>
      <td class="num">${fmtInt(b.dom_voice_min)}</td><td class="num">${fmtInt(b.sms_sent)}</td><td class="num">${fmtInt(b.data_total_mb)}</td>
      <td class="num">${fmtMoney(b.monthly_fee)}</td><td class="num">${fmtMoney(b.usage_charges)}</td><td class="num strong">${fmtMoney(b.total)}</td>
      <td><a class="btn" href="/api/bills/${state.month}/${b.msisdn}/pdf" onclick="event.stopPropagation()">PDF</a></td></tr>`).join("")
      : `<tr><td colspan="10" class="muted">No bills match.</td></tr>`;
    const from = (r.page - 1) * r.size + 1, to = Math.min(r.page * r.size, r.total);
    $("bills-info").textContent = r.total ? `${fmtInt(from)}–${fmtInt(to)} of ${fmtInt(r.total)} bills · ${r.ms}ms` : `0 bills · ${r.ms}ms`;
    $("prev").disabled = r.page <= 1;
    $("next").disabled = to >= r.total;
    tbody.querySelectorAll("tr.clickable").forEach((tr) => tr.addEventListener("click", () => openBill(tr.dataset.msisdn)));
  } catch (e) {
    tbody.innerHTML = `<tr><td colspan="10">${esc(e.message)}</td></tr>`;
  }
}

// ---------------------------------------------------------------------------------------------
// bill detail drawer
// ---------------------------------------------------------------------------------------------
async function openBill(msisdn) {
  const month = state.month;
  $("drawer").hidden = false;
  $("scrim").hidden = false;
  $("d-eyebrow").textContent = `Bill · ${month}`;
  $("d-title").textContent = phone(msisdn);
  $("d-sub").textContent = "loading…";
  $("d-body").innerHTML = "";
  $("d-pdf").href = `/api/bills/${month}/${msisdn}/pdf`;
  let d;
  try { d = await api(`/api/bills/${month}/${msisdn}`); } catch (e) { $("d-sub").textContent = e.message; return; }
  const b = d.bill, p = d.plan || {};
  $("d-eyebrow").textContent = `${b.bill_no} · ${month}`;
  $("d-sub").textContent = `${b.rate_plan} · ${b.subscriber_type.toLowerCase()} · 5 detail queries in ${d.ms}ms`;
  const line = (label, used, incl, charged, amount) =>
    `<tr><td>${label}</td><td class="num">${used}</td><td class="num">${incl}</td><td class="num">${charged}</td><td class="num strong">${fmtMoney(amount)}</td></tr>`;
  const items = d.items || [];
  $("d-body").innerHTML = `
    <div class="bill-total">
      <div class="kpi"><div class="kpi-label">Total incl. GST</div><div class="kpi-value">${fmtMoney(b.total)}</div></div>
      <div class="kpi"><div class="kpi-label">Plan fee</div><div class="kpi-value">${fmtMoney(b.monthly_fee)}</div></div>
      <div class="kpi"><div class="kpi-label">Usage charges</div><div class="kpi-value">${fmtMoney(b.usage_charges)}</div></div>
    </div>
    <div class="d-section"><h3>Charges</h3><div class="table-wrap"><table class="grid">
      <thead><tr><th>Item</th><th class="num">Used</th><th class="num">Included</th><th class="num">Billable</th><th class="num">Amount</th></tr></thead><tbody>
      ${line(`Monthly fee · ${esc(b.rate_plan)}`, "", "", "", b.monthly_fee)}
      ${line("Domestic voice (min)", fmtInt(b.dom_voice_min), fmtInt(b.incl_voice_min), fmtInt(b.voice_overage_min), b.voice_overage_charge)}
      ${line("SMS", fmtInt(b.sms_sent), fmtInt(b.incl_sms), fmtInt(b.sms_overage), b.sms_charge)}
      ${line(`Data MB <span class="muted small">(${fmtInt(b.zero_rated_mb)} MB zero-rated)</span>`, fmtInt(b.chargeable_mb), fmtInt(b.incl_data_mb), fmtInt(b.data_overage_mb), b.data_overage_charge)}
      ${line(`International (min) <span class="muted small">@ ${fmtMoney(p.intl_per_min)}</span>`, fmtInt(b.intl_min), "", fmtInt(b.intl_min), b.intl_charge)}
      ${line(`Premium (min) <span class="muted small">@ ${fmtMoney(p.premium_per_min)}</span>`, fmtInt(b.premium_min), "", fmtInt(b.premium_min), b.premium_charge)}
      ${line(`Roaming voice (min) <span class="muted small">@ ${fmtMoney(p.roaming_voice_per_min)}</span>`, fmtInt(b.roam_voice_min), "", fmtInt(b.roam_voice_min), b.roam_voice_charge)}
      ${line("Roaming SMS", fmtInt(b.roam_sms), "", fmtInt(b.roam_sms), b.roam_sms_charge)}
      ${line(`Roaming data (MB) <span class="muted small">@ ${fmtMoney(p.roaming_data_per_mb)}/MB</span>`, fmtMB(b.roam_data_mb), "", fmtMB(b.roam_data_mb), b.roam_data_charge)}
      ${b.roam_data_mb * p.roaming_data_per_mb > b.roam_data_charge + 0.005 ? `<tr><td colspan="5" class="cap-note">${fmtMB(b.roam_data_mb)} MB × ${fmtMoney(p.roaming_data_per_mb)}/MB = ${fmtMoney(b.roam_data_mb * p.roaming_data_per_mb)}, capped at the plan's roaming data limit of ${fmtMoney(p.roaming_data_cap)} (saves ${fmtMoney(b.roam_data_mb * p.roaming_data_per_mb - b.roam_data_charge)})</td></tr>` : ""}
      <tr><td colspan="4" class="num">Subtotal</td><td class="num">${fmtMoney(b.subtotal)}</td></tr>
      <tr><td colspan="4" class="num">GST 9%</td><td class="num">${fmtMoney(b.tax)}</td></tr>
      <tr><td colspan="4" class="num strong">Total</td><td class="num strong">${fmtMoney(b.total)}</td></tr>
    </tbody></table></div></div>
    <div class="d-section"><h3>Daily usage</h3><div class="d-charts">
      <figure class="chart-card"><figcaption><h3>Call minutes</h3><p>all directions, per day</p></figcaption><div id="d-minutes"></div></figure>
      <figure class="chart-card"><figcaption><h3>Data MB</h3><p>uplink + downlink, per day</p></figcaption><div id="d-mb"></div></figure>
    </div></div>
    <div class="d-section"><h3>Data by application category</h3><div class="chart-card"><div id="d-apps"></div></div></div>
    <div class="d-section"><h3>Itemised international, premium and roaming usage <span class="muted small">(${items.length}${items.length === 300 ? "+" : ""})</span></h3>
      ${items.length ? `<div class="table-wrap"><table class="grid"><thead><tr><th>Time</th><th>Number</th><th>Type</th><th>Network</th><th class="num">Duration</th><th class="num">Charge</th></tr></thead><tbody>
      ${items.map((it) => `<tr><td>${esc(String(it.start_time).slice(0, 16))}</td><td>${esc(it.other_party_number)}</td>
        <td>${it.call_type === "SMS" ? "SMS" : esc(it.other_party_type.toLowerCase() + " " + it.direction)}</td>
        <td>${it.roaming ? `<span class="tag roam">roam ${esc(it.visited_plmn)}</span>` : "home"}</td>
        <td class="num">${it.call_type === "SMS" ? "–" : Math.floor(it.duration_sec / 60) + ":" + String(it.duration_sec % 60).padStart(2, "0")}</td>
        <td class="num">${fmtMoney(it.charge)}</td></tr>`).join("")}</tbody></table></div>` : `<div class="empty">No itemised usage this month.</div>`}
    </div>`;
  // one bar per calendar day, so bars sit on their dates even when most days have no usage
  const byDay = new Map((d.daily || []).map((x) => [String(x.day).slice(0, 10), x]));
  const [yy, mm] = month.split("-").map(Number);
  const daily = Array.from({ length: new Date(yy, mm, 0).getDate() }, (_, i) => {
    const day = `${month}-${String(i + 1).padStart(2, "0")}`;
    return byDay.get(day) || { day, minutes: 0, calls: 0, sms: 0, mb: 0 };
  });
  const dayLabel = (x) => String(Number(String(x.day).slice(8, 10)));
  columnChart($("d-minutes"), daily, {
    label: dayLabel, value: (x) => Number(x.minutes), xEvery: 5, fmt: (v) => fmtInt(v), height: 160,
    tipTitle: (x) => x.day, tipRows: (x, c) => [[c, "Minutes", fmtInt(x.minutes)], [null, "Calls", fmtInt(x.calls)], [null, "SMS", fmtInt(x.sms)]],
  });
  columnChart($("d-mb"), daily, {
    label: dayLabel, value: (x) => Number(x.mb), xEvery: 5, fmt: (v) => compact(v), height: 160,
    tipTitle: (x) => x.day, tipRows: (x, c) => [[c, "Data", fmtInt(x.mb) + " MB"]],
  });
  hbarChart($("d-apps"), d.apps || [], { label: (a) => a.app_category, value: (a) => Number(a.mb), fmt: (v) => fmtInt(v) + " MB" });
}

function closeDrawer() { $("drawer").hidden = true; $("scrim").hidden = true; hideTip(); }

async function loadRuns() {
  try {
    const runs = await api("/api/runs");
    $("runs").innerHTML = runs.length ? runs.map((r) => `<tr><td>${esc(r.run_id)}</td><td>${esc(r.month)}</td>
      <td><span class="run-status ${esc(r.status)}">${esc(r.status)}</span></td><td>${esc(r.started_at)}</td>
      <td class="num">${dur(r.secs)}</td><td class="num">${r.tasks_done}/${r.tasks_total}</td><td class="msg">${esc(r.message || "")}</td></tr>`).join("")
      : `<tr><td colspan="7" class="muted">No runs yet.</td></tr>`;
  } catch (e) {
    $("runs").innerHTML = `<tr><td colspan="7">${esc(e.message)}</td></tr>`;
  }
}

// ---------------------------------------------------------------------------------------------
// wiring
// ---------------------------------------------------------------------------------------------
document.querySelectorAll("[data-run]").forEach((b) => b.addEventListener("click", () => startStep(b.dataset.run)));
$("job-cancel").addEventListener("click", async () => { await api("/api/job/cancel", { method: "POST" }); pollJob(); });
$("month").addEventListener("change", async (e) => { state.month = e.target.value; await loadOverview(); loadDashboard(); });
$("refresh").addEventListener("click", async () => { await loadOverview(true); loadDashboard(); loadRuns(); });
$("go-dash").addEventListener("click", () => $("dash").scrollIntoView({ behavior: "smooth" }));
$("search-form").addEventListener("submit", (e) => { e.preventDefault(); state.page = 1; searchBills(); });
$("prev").addEventListener("click", () => { state.page--; searchBills(); });
$("next").addEventListener("click", () => { state.page++; searchBills(); });
$("d-close").addEventListener("click", closeDrawer);
$("scrim").addEventListener("click", closeDrawer);
document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeDrawer(); });
$("theme").addEventListener("click", () => {
  const dark = document.documentElement.dataset.theme ? document.documentElement.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.dataset.theme = dark ? "light" : "dark";
  try { localStorage.setItem("theme", document.documentElement.dataset.theme); } catch {}
  loadDashboard();
});
try { const t = localStorage.getItem("theme"); if (t) document.documentElement.dataset.theme = t; } catch {}
let resizeTimer;
addEventListener("resize", () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(loadDashboard, 250); });

(async () => {
  await loadOverview();
  loadDashboard();
  loadRuns();
  pollJob();
})();
