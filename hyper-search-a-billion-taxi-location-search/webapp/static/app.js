/* Taxi hyper-search UI: pick a place + a moment, see which taxis were there — and how little Doris had to scan. */
(() => {
  const $ = (id) => document.getElementById(id);
  const fmtInt = (n) => n == null ? '–' : Number(n).toLocaleString('en-US');
  const pad = (n, w = 2) => String(n).padStart(w, '0');
  const fmtTs = (ms) => { const d = new Date(ms);
    return `${d.getUTCFullYear()}-${pad(d.getUTCMonth()+1)}-${pad(d.getUTCDate())} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}.${pad(d.getUTCMilliseconds(),3)}`; };
  const fmtShort = (ms) => fmtTs(ms).slice(0, 16);
  const fmtDur = (ms) => { const s = Math.round(Math.abs(ms) / 1000);
    if (s < 60) return `${s} s`; if (s < 3600) return `${Math.floor(s/60)} min ${s%60} s`;
    if (s < 86400) return `${Math.floor(s/3600)} h ${Math.floor(s%3600/60)} min`; return `${Math.floor(s/86400)} d ${Math.floor(s%86400/3600)} h`; };
  const statusClass = (s) => ({AVAILABLE:'available', ON_CALL:'oncall', HIRED:'hired', OFFLINE:'offline'})[s] || 'offline';
  const statusColor = (s) => ({available:'#3ddc97', oncall:'#5aa9ff', hired:'#ffb347', offline:'#7a8594'})[statusClass(s)];

  const S = {
    meta: null, stations: [], center: {lat: 1.2840, lon: 103.8515, name: 'Raffles Place'},
    radius: 500, lookback: 120, t: 0, minTs: 0, maxTs: 0, win: 86400000, winStart: 0, winEnd: 0,
    playing: false, speed: 30000, selected: null, lastResult: null, inflight: null, timer: null, fetchSeq: 0,
  };
  const STEPS = 4000;

  /* ---------- map ---------- */
  const map = L.map('map', {zoomControl: true, attributionControl: true}).setView([1.30, 103.85], 12);
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19, className: 'dark-tiles', attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
  }).addTo(map);
  const circle = L.circle([S.center.lat, S.center.lon], {radius: S.radius, color: '#5aa9ff', weight: 1.5, fillColor: '#5aa9ff', fillOpacity: .08}).addTo(map);
  const pin = L.marker([S.center.lat, S.center.lon], {draggable: true, icon: L.divIcon({className: 'center-pin', iconSize: [18, 18]}), zIndexOffset: 1000}).addTo(map);
  const cellLayer = L.layerGroup().addTo(map);
  const trailLayer = L.layerGroup().addTo(map);
  const markers = new Map(); // taxi_id -> L.marker

  pin.on('dragend', () => { const p = pin.getLatLng(); setCenter(p.lat, p.lng, 'custom location'); });
  map.on('click', (e) => setCenter(e.latlng.lat, e.latlng.lng, 'custom location'));

  // phase: 'enter' (fade/scale in, no position transition), 'moving' (glide between positions), 'leave' (fade out)
  function taxiIcon(t, selected, phase = 'moving') {
    const col = statusColor(t.status);
    const svg = t.status === 'OFFLINE'
      ? `<svg viewBox="0 0 26 26"><circle cx="13" cy="13" r="6" fill="${col}" stroke="#0b0e12" stroke-width="1.5"/></svg>`
      : `<svg viewBox="0 0 26 26" style="transform:rotate(${t.heading}deg)"><path d="M13 3 L21 21 L13 17 L5 21 Z" fill="${col}" stroke="#0b0e12" stroke-width="1.5" stroke-linejoin="round"/></svg>`;
    return L.divIcon({className: `taxi-marker ${phase}` + (selected ? ' selected' : ''), html: `<div class="ic">${svg}</div>`, iconSize: [26, 26], iconAnchor: [13, 13], popupAnchor: [0, -10]});
  }
  // Suspend position transitions while the map zooms (Leaflet re-projects every marker at zoom end).
  const mapEl = map.getContainer();
  map.on('zoomstart', () => mapEl.classList.add('zooming'));
  map.on('zoomend', () => setTimeout(() => mapEl.classList.remove('zooming'), 60));

  function popupHtml(t) {
    const cls = statusClass(t.status);
    return `<div class="popup"><h4>${t.plate}<span class="pill" style="background:${statusColor(t.status)}">${t.status}</span></h4>
      <table>
      <tr><td>company</td><td>${t.company}</td></tr>
      <tr><td>taxi_id</td><td>${t.taxi_id}</td></tr>
      <tr><td>last ping</td><td>${fmtTs(Date.parse(t.last_seen))} UTC</td></tr>
      <tr><td>age at T</td><td>${fmtDur(t.age_ms)} before</td></tr>
      <tr><td>distance</td><td>${Math.round(t.dist_m)} m</td></tr>
      <tr><td>speed / heading</td><td>${t.speed_kmh} km/h · ${t.heading}°</td></tr>
      <tr><td>position</td><td>${t.lat.toFixed(5)}, ${t.lon.toFixed(5)}</td></tr>
      <tr><td>geohash6</td><td><code>${t.geohash6}</code></td></tr>
      </table>
      <button class="btn" onclick="window.__trail(${t.taxi_id})">Show trail ±15 min</button></div>`;
  }

  function renderTaxis(taxis) {
    const seen = new Set();
    for (const t of taxis) {
      seen.add(t.taxi_id);
      const sel = S.selected === t.taxi_id;
      let m = markers.get(t.taxi_id);
      if (!m) {
        // New taxi in the circle: fade in where it is (no transform transition on the first placement).
        m = L.marker([t.lat, t.lon], {icon: taxiIcon(t, sel, 'enter')}).addTo(map);
        m.on('click', () => select(t.taxi_id));
        m.bindPopup('', {closeButton: true});
        markers.set(t.taxi_id, m);
        const mm = m;
        setTimeout(() => { if (markers.get(t.taxi_id) === mm) mm.setIcon(taxiIcon(mm.taxi, S.selected === t.taxi_id, 'moving')); }, 450);
      } else {
        m.setLatLng([t.lat, t.lon]);          // existing taxi: glide to its new position
        m.setIcon(taxiIcon(t, sel, 'moving'));
      }
      m.taxi = t;
      m.setPopupContent(popupHtml(t));
    }
    for (const [id, m] of markers) if (!seen.has(id)) {
      // Taxi left the circle (or no ping in the look-back): fade out in place, then remove.
      markers.delete(id);
      m.closePopup();
      m.setIcon(taxiIcon(m.taxi, false, 'leave'));
      setTimeout(() => map.removeLayer(m), 380);
      if (S.selected === id) { S.selected = null; trailLayer.clearLayers(); }
    }
  }

  function renderCells(cells) {
    cellLayer.clearLayers();
    for (const g of cells) {
      const b = geohashBounds(g);
      L.rectangle(b, {color: '#3ddc97', weight: 1, opacity: .35, fill: false, dashArray: '4 4', interactive: false}).addTo(cellLayer);
    }
  }

  function geohashBounds(g) {
    const B = '0123456789bcdefghjkmnpqrstuvwxyz';
    let latLo = -90, latHi = 90, lonLo = -180, lonHi = 180, even = true;
    for (const c of g) { const v = B.indexOf(c);
      for (let bit = 4; bit >= 0; bit--) { const on = (v >> bit) & 1;
        if (even) { const mid = (lonLo + lonHi) / 2; if (on) lonLo = mid; else lonHi = mid; }
        else { const mid = (latLo + latHi) / 2; if (on) latLo = mid; else latHi = mid; }
        even = !even; } }
    return [[latLo, lonLo], [latHi, lonHi]];
  }

  /* ---------- list & selection ---------- */
  function renderList(taxis) {
    const ul = $('list'); ul.innerHTML = '';
    $('list-title').textContent = taxis.length ? `within ${S.radius} m of ${S.center.name}` : '';
    if (!taxis.length) { ul.innerHTML = '<li class="empty">No taxi pinged inside the radius in the look-back window.</li>'; return; }
    for (const t of taxis) {
      const li = document.createElement('li');
      li.dataset.id = t.taxi_id;
      if (S.selected === t.taxi_id) li.classList.add('selected');
      li.innerHTML = `<i class="dot ${statusClass(t.status)}"></i><div><div class="plate">${t.plate}</div><div class="sub">${t.company} · ${t.status.toLowerCase().replace('_',' ')} · ${t.speed_kmh} km/h · ${fmtDur(t.age_ms)} ago</div></div><div class="dist">${Math.round(t.dist_m)} m</div>`;
      li.onclick = () => { select(t.taxi_id); const m = markers.get(t.taxi_id); if (m) { m.openPopup(); } };
      ul.appendChild(li);
    }
  }

  function select(id) {
    S.selected = id;
    for (const [tid, m] of markers) m.setIcon(taxiIcon(m.taxi, tid === id, 'moving'));
    document.querySelectorAll('#list li').forEach(li => li.classList.toggle('selected', Number(li.dataset.id) === id));
  }

  window.__trail = async (id) => {
    select(id);
    trailLayer.clearLayers();
    try {
      const r = await fetch(`/api/trail?taxi_id=${id}&t=${new Date(S.t).toISOString()}&minutes=15`);
      const d = await r.json();
      if (d.error) return toast(d.error);
      const pts = d.points.map(p => [p.lat, p.lon]);
      if (!pts.length) return;
      L.polyline(pts, {color: '#fff', weight: 2, opacity: .6}).addTo(trailLayer);
      for (const p of d.points) {
        const past = Date.parse(p.ts) <= S.t;
        L.circleMarker([p.lat, p.lon], {radius: 3, color: statusColor(p.status), fillColor: statusColor(p.status), fillOpacity: past ? .9 : .25, opacity: past ? 1 : .35, weight: 1})
          .bindTooltip(`${fmtTs(Date.parse(p.ts))} · ${p.status} · ${p.speed_kmh} km/h`).addTo(trailLayer);
      }
      toast(`trail: ${d.points.length} pings in ${Math.round(d.query_ms)} ms (partitions ${d.pruning.partitions}, tablets ${d.pruning.tablets})`, false);
    } catch (e) { toast(String(e)); }
  };

  /* ---------- stats ---------- */
  function renderStats(r) {
    $('stat-ms').textContent = Math.round(r.query_ms).toLocaleString('en-US');
    $('stat-ms').classList.remove('busy');
    $('stat-partitions').textContent = r.pruning.partitions || '–';
    $('stat-tablets').textContent = r.pruning.tablets || '–';
    $('stat-cells').textContent = r.cells.length;
    $('stat-taxis').textContent = r.taxis.length;
    const pp = r.pruning.partitions_total ? r.pruning.partitions_used / r.pruning.partitions_total : 0;
    const tp = r.pruning.tablets_total ? r.pruning.tablets_used / r.pruning.tablets_total : 0;
    $('bar-partitions').style.width = `${Math.max(0.5, pp * 100)}%`; $('bar-partitions-pct').textContent = `${(pp * 100).toFixed(pp < .01 ? 2 : 1)}%`;
    $('bar-tablets').style.width = `${Math.max(0.5, tp * 100)}%`; $('bar-tablets-pct').textContent = `${(tp * 100).toFixed(1)}%`;
    $('sql').textContent = r.sql;
  }

  /* ---------- search ---------- */
  let debounce = null;
  function scheduleSearch(delay = 120) { clearTimeout(debounce); debounce = setTimeout(search, delay); }

  async function search() {
    if (S.inflight) S.inflight.abort();
    const ctl = new AbortController(); S.inflight = ctl;
    const seq = ++S.fetchSeq;
    $('stat-ms').classList.add('busy');
    const url = `/api/search?lat=${S.center.lat}&lon=${S.center.lon}&t=${new Date(S.t).toISOString()}&radius=${S.radius}&lookback=${S.lookback}`;
    try {
      const r = await fetch(url, {signal: ctl.signal});
      const d = await r.json();
      if (seq !== S.fetchSeq) return;
      if (d.error) { toast(d.error); return; }
      S.lastResult = d;
      renderStats(d); renderCells(d.cells); renderTaxis(d.taxis); renderList(d.taxis);
      hideToast();
    } catch (e) { if (e.name !== 'AbortError') toast(String(e)); }
    finally { if (S.inflight === ctl) S.inflight = null; }
  }

  /* ---------- all-time history comparison ---------- */
  const hist = {pruned: null, nocell: null};
  async function runHistory(mode) {
    const col = $(mode === 'nocell' ? 'hist-col-nocell' : 'hist-col-pruned');
    col.classList.add('busy'); col.querySelector('.hist-ms').textContent = '…'; col.querySelector('.hist-sub').textContent = 'scanning';
    $('hist-pruned').disabled = $('hist-full').disabled = true;
    const t0 = performance.now();
    const tick = setInterval(() => { col.querySelector('.hist-ms').textContent = `${((performance.now() - t0) / 1000).toFixed(1)} s`; }, 100);
    try {
      const d = await (await fetch(`/api/history?lat=${S.center.lat}&lon=${S.center.lon}&radius=${S.radius}&mode=${mode}`)).json();
      if (d.error) throw new Error(d.error);
      hist[mode] = d;
      col.querySelector('.hist-ms').textContent = d.query_ms >= 1000 ? `${(d.query_ms / 1000).toFixed(2)} s` : `${Math.round(d.query_ms)} ms`;
      col.querySelector('.hist-sub').textContent = `partitions ${d.pruning.partitions} · tablets ${d.pruning.tablets}`;
      const a = hist.pruned, b = hist.nocell;
      let msg = `${fmtInt(d.pings)} pings from ${fmtInt(d.taxis)} taxis, ${fmtShort(Date.parse(d.first_seen))} → ${fmtShort(Date.parse(d.last_seen))}`;
      if (a && b) msg += ` · cells are ${(b.query_ms / Math.max(1, a.query_ms)).toFixed(1)}× faster`;
      $('hist-summary').textContent = msg;
      $('sql').textContent = d.sql;
    } catch (e) { col.querySelector('.hist-ms').textContent = 'error'; col.querySelector('.hist-sub').textContent = String(e); }
    finally { clearInterval(tick); col.classList.remove('busy'); $('hist-pruned').disabled = $('hist-full').disabled = false; }
  }
  $('hist-pruned').onclick = () => runHistory('pruned');
  $('hist-full').onclick = () => runHistory('nocell');
  function resetHistory() { hist.pruned = hist.nocell = null; for (const id of ['hist-col-pruned', 'hist-col-nocell']) { $(id).querySelector('.hist-ms').textContent = '–'; $(id).querySelector('.hist-sub').textContent = '–'; } $('hist-summary').textContent = ''; }

  /* ---------- location controls ---------- */
  function setCenter(lat, lon, name) {
    S.center = {lat, lon, name}; resetHistory();
    pin.setLatLng([lat, lon]); circle.setLatLng([lat, lon]);
    if (name === 'custom location') $('station').value = '';
    scheduleSearch(0);
  }
  function fillStations(filter = '') {
    const sel = $('station'); const cur = sel.value; sel.innerHTML = '<option value="">— choose an MRT station —</option>';
    const f = filter.trim().toLowerCase();
    S.stations.filter(s => !f || s.name.toLowerCase().includes(f) || s.lines.toLowerCase().includes(f))
      .sort((a, b) => a.name.localeCompare(b.name))
      .forEach(s => { const o = document.createElement('option'); o.value = s.name; o.textContent = `${s.name}  (${s.lines})`; sel.appendChild(o); });
    sel.value = cur;
  }
  $('station-filter').oninput = (e) => fillStations(e.target.value);
  $('station').onchange = (e) => { const s = S.stations.find(x => x.name === e.target.value); if (!s) return;
    S.center = {lat: s.lat, lon: s.lon, name: s.name}; resetHistory(); pin.setLatLng([s.lat, s.lon]); circle.setLatLng([s.lat, s.lon]);
    map.flyTo([s.lat, s.lon], Math.max(map.getZoom(), 15), {duration: .6}); scheduleSearch(0); };
  $('radius').oninput = (e) => { S.radius = Number(e.target.value); $('radius-label').textContent = `${S.radius} m`; circle.setRadius(S.radius); resetHistory(); scheduleSearch(); };
  $('lookback').oninput = (e) => { S.lookback = Number(e.target.value); $('lookback-label').textContent = `${S.lookback} s`; scheduleSearch(); };

  /* ---------- time slider ---------- */
  function setWindow(win, anchorEnd) {
    S.win = win === 'all' ? (S.maxTs - S.minTs) : Number(win);
    let end = anchorEnd != null ? anchorEnd : Math.min(S.maxTs, S.t + S.win * 0.1);
    if (S.t >= S.maxTs - 1000) end = S.maxTs;
    let start = end - S.win;
    if (start < S.minTs) { start = S.minTs; end = Math.min(S.maxTs, start + S.win); }
    S.winStart = start; S.winEnd = end;
    $('time-min').textContent = fmtShort(start); $('time-max').textContent = fmtShort(end);
    syncSlider();
  }
  function syncSlider() { $('time').value = Math.round((S.t - S.winStart) / (S.winEnd - S.winStart) * STEPS); renderTime(); }
  function renderTime() {
    $('time-value').textContent = fmtTs(S.t);
    const d = S.maxTs - S.t;
    $('time-rel').textContent = d < 1000 ? 'latest row in table' : `${fmtDur(d)} before latest`;
  }
  function setT(t, {query = true} = {}) {
    S.t = Math.min(S.maxTs, Math.max(S.minTs, t));
    if (S.t < S.winStart || S.t > S.winEnd) setWindow(S.win, Math.min(S.maxTs, S.t + S.win * 0.5));
    syncSlider();
    if (query) scheduleSearch();
  }
  $('time').oninput = (e) => { S.t = S.winStart + Number(e.target.value) / STEPS * (S.winEnd - S.winStart); renderTime(); scheduleSearch(); };
  $('window').onchange = (e) => setWindow(e.target.value);
  $('step-back').onclick = () => setT(S.t - 30000);
  $('step-fwd').onclick = () => setT(S.t + 30000);
  $('speed').onchange = (e) => { S.speed = Number(e.target.value); };
  $('latest').onclick = async () => { await loadMeta(true); setWindow(S.win, S.maxTs); setT(S.maxTs); };

  async function playLoop() {
    while (S.playing) {
      const t0 = performance.now();
      if (S.t >= S.maxTs) { setPlaying(false); break; }
      S.t = Math.min(S.maxTs, S.t + S.speed);
      if (S.t > S.winEnd) setWindow(S.win, Math.min(S.maxTs, S.t + S.win * 0.5));
      syncSlider();
      await search();
      const wait = Math.max(0, 600 - (performance.now() - t0));
      await new Promise(r => setTimeout(r, wait));
    }
  }
  function setPlaying(p) { S.playing = p; $('play').textContent = p ? '❚❚' : '▶'; if (p) playLoop(); }
  $('play').onclick = () => setPlaying(!S.playing);
  document.addEventListener('keydown', (e) => {
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT') return;
    if (e.key === 'ArrowLeft') setT(S.t - (e.shiftKey ? 300000 : 30000));
    else if (e.key === 'ArrowRight') setT(S.t + (e.shiftKey ? 300000 : 30000));
    else if (e.key === ' ') { e.preventDefault(); setPlaying(!S.playing); }
  });

  /* ---------- toast ---------- */
  let toastTimer = null;
  function toast(msg, isError = true) { const el = $('toast'); el.textContent = msg; el.style.display = 'block';
    el.style.background = isError ? '#3a1f1f' : '#1f3a2a'; el.style.color = isError ? '#ffb4b4' : '#b4ffd0'; el.style.borderColor = isError ? '#6b2b2b' : '#2b6b40';
    clearTimeout(toastTimer); if (!isError) toastTimer = setTimeout(hideToast, 4000); }
  function hideToast() { $('toast').style.display = 'none'; }

  /* ---------- boot ---------- */
  async function loadMeta(refresh = false) {
    const d = await (await fetch('/api/meta' + (refresh ? '?refresh=1' : ''))).json();
    S.meta = d.meta; S.stations = d.stations;
    S.minTs = Date.parse(d.meta.min_ts); S.maxTs = Date.parse(d.meta.max_ts);
    $('meta-rows').textContent = fmtInt(d.meta.rows); $('meta-partitions').textContent = fmtInt(d.meta.partitions); $('hist-partitions-total').textContent = fmtInt(d.meta.partitions);
    if (d.meta.error) toast('meta: ' + d.meta.error);
  }
  (async () => {
    try {
      await loadMeta();
      fillStations();
      $('station').value = 'Raffles Place';
      S.t = S.maxTs;
      setWindow($('window').value, S.maxTs);
      map.setView([S.center.lat, S.center.lon], 15);
      search();
    } catch (e) { toast('failed to load /api/meta: ' + e); }
  })();
})();
