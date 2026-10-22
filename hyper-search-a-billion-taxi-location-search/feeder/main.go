// taxi-feeder: keeps the taxi_ping table "live".
//
//  1. Finds the newest ping in Doris and measures the ingest rate of the 24 hours before it
//     (hourly counts, aligned to full hours).
//  2. Makes up the gap from that ping to now with the same simulation as the Rust generator,
//     at the measured rate, as fast as the cluster accepts it (progress bar).
//  3. Then keeps generating in realtime at that rate, reporting periodically.
//
// Rows go through the Doris stream-load Go SDK. Rerunning after a stop simply fills the new gap.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type opts struct {
	env, stations, database, table string
	dryRun, makeupOnly             bool
	emptyBackfill, rateWindow      time.Duration
	fleetWindow                    time.Duration
	rate, defaultRate              float64
	flush, report                  time.Duration
	batchMiB, workers, chunkRows   int
	taxis                          int
	platePrefix, companies         string
	pingSecs, jitterSecs           int
	shiftMin, shiftMax             int
	seed                           uint64
}

func main() {
	var o opts
	flag.StringVar(&o.env, "env", "../.env", "env file with VELODB_* settings (see env.example); VELODB_* environment variables override it")
	flag.StringVar(&o.stations, "stations", "../mrt_stations.csv", "MRT stations CSV (shared with the generator)")
	flag.StringVar(&o.database, "database", "", "override DATABASE from env")
	flag.StringVar(&o.table, "table", "", "override TABLE from env")
	flag.BoolVar(&o.dryRun, "dry-run", false, "generate but do not load (SDK FakeSend); Doris is still read for the plan")
	flag.BoolVar(&o.makeupOnly, "makeup-only", false, "exit once caught up instead of continuing in realtime")
	flag.DurationVar(&o.emptyBackfill, "empty-backfill", 24*time.Hour, "when the table is empty, make up this much history before now")
	flag.DurationVar(&o.rateWindow, "rate-window", 24*time.Hour, "window before the last event used to measure the hourly rate")
	flag.DurationVar(&o.fleetWindow, "fleet-window", 30*24*time.Hour, "window used to read plate/company per taxi from Doris")
	flag.Float64Var(&o.rate, "rate", 0, "force this many rows/hour instead of measuring")
	flag.Float64Var(&o.defaultRate, "default-rate", 1e9/490/24, "rows/hour when nothing can be measured (empty table)")
	flag.DurationVar(&o.flush, "flush", 5*time.Second, "realtime: max age of a stream-load batch (SDK linger)")
	flag.DurationVar(&o.report, "report", 10*time.Second, "realtime: status line interval")
	flag.IntVar(&o.batchMiB, "batch-mib", 32, "max stream-load request body (MiB)")
	flag.IntVar(&o.workers, "workers", 2, "concurrent stream loads")
	flag.IntVar(&o.chunkRows, "chunk-rows", 10000, "rows per SendBatch handed to the SDK")
	flag.IntVar(&o.taxis, "taxis", 100000, "fleet size (taxi_id in [0, taxis))")
	flag.StringVar(&o.platePrefix, "plate-prefix", "SH", "plate prefix for taxis not seen in Doris")
	flag.StringVar(&o.companies, "companies", "ComfortDelGro:45,SMRT:15,Trans-Cab:15,Premier:10,Prime:8,Strides:7", "company:weight list for taxis not seen in Doris")
	flag.IntVar(&o.pingSecs, "ping-secs", 30, "seconds between pings on shift")
	flag.IntVar(&o.jitterSecs, "jitter-secs", 3, "+/- jitter per ping interval")
	flag.IntVar(&o.shiftMin, "shift-min", 60, "min shift length (minutes)")
	flag.IntVar(&o.shiftMax, "shift-max", 240, "max shift length (minutes)")
	flag.Uint64Var(&o.seed, "seed", 0, "RNG seed (0 = time based)")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func parseCompanies(s string) ([]Company, error) {
	var out []Company
	for _, part := range strings.Split(s, ",") {
		name, w, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, fmt.Errorf("bad company %q (want name:weight)", part)
		}
		f, err := strconv.ParseFloat(w, 64)
		if err != nil || f <= 0 {
			return nil, fmt.Errorf("bad company weight %q", part)
		}
		out = append(out, Company{Name: name, Weight: f})
	}
	return out, nil
}

func fmtInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func fmtTs(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05") }

func fmtDur(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func run(o opts) error {
	companies, err := parseCompanies(o.companies)
	if err != nil {
		return err
	}
	if o.shiftMin <= 0 || o.shiftMax < o.shiftMin || o.pingSecs <= 0 || o.jitterSecs >= o.pingSecs || o.taxis <= 0 {
		return fmt.Errorf("invalid sim parameters")
	}
	env := loadEnv(o.env)
	if o.database == "" {
		o.database = env["VELODB_DATABASE"]
	}
	if o.table == "" {
		o.table = env["VELODB_TABLE"]
	}
	if o.database == "" || o.table == "" || env["VELODB_HOST"] == "" || env["VELODB_USER"] == "" {
		return fmt.Errorf("VELODB_HOST, VELODB_USER, VELODB_DATABASE, VELODB_TABLE must be set (env file %s or environment)", o.env)
	}
	st, err := loadStations(o.stations)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := openDB(env, o.database, o.table)
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Printf("table %s.%s @ %s  (stream load via %s)%s\n", o.database, o.table, env["VELODB_HOST"], env["VELODB_STREAM_LOAD_URL"],
		map[bool]string{true: "  [DRY RUN: nothing is loaded]", false: ""}[o.dryRun])

	// ---- 1. where does the data end, and at what rate was it arriving?
	t0 := time.Now()
	last, found, err := db.lastTs(ctx)
	if err != nil {
		return fmt.Errorf("max(ts): %w", err)
	}
	now := time.Now().UTC()
	var start time.Time
	var profile [24]float64 // rows/hour by UTC hour-of-day
	avg := o.defaultRate
	if found {
		start = last.Add(time.Millisecond)
		fmt.Printf("last event      %s UTC  (gap to now %s, looked up in %s)\n", last.Format("2006-01-02 15:04:05.000"), fmtDur(now.Sub(last)), fmtDur(time.Since(t0)))
		if last.After(now.Add(time.Minute)) {
			return fmt.Errorf("last event is in the future (%s); check clocks", last)
		}
		hEnd := last.Truncate(time.Hour)
		hStart := hEnd.Add(-o.rateWindow)
		t1 := time.Now()
		counts, minTs, err := db.hourlyCounts(ctx, hStart, hEnd)
		if err != nil {
			return fmt.Errorf("hourly counts: %w", err)
		}
		var total int64
		var buckets int
		for _, c := range counts {
			total += c
			if c > 0 {
				buckets++
			}
		}
		hours := o.rateWindow.Hours()
		partial := total > 0 && minTs.After(hStart.Add(time.Minute)) // table younger than the window
		if partial {
			hours = hEnd.Sub(minTs).Hours()
		}
		switch {
		case total == 0:
			fmt.Printf("rate window     %s .. %s UTC has no rows; using default %s rows/h\n", hStart.Format("2006-01-02 15:04"), hEnd.Format("2006-01-02 15:04"), fmtInt(int64(avg)))
		case partial:
			avg = float64(total) / hours
			fmt.Printf("rate window     data only covers %s .. %s UTC: %s rows in %.2f h => %s rows/h (flat; too little history for an hourly profile; measured in %s)\n",
				minTs.Format("2006-01-02 15:04"), hEnd.Format("2006-01-02 15:04"), fmtInt(total), hours, fmtInt(int64(avg)), fmtDur(time.Since(t1)))
		default:
			avg = float64(total) / hours
			fmt.Printf("rate window     %s .. %s UTC: %s rows in %.0f h => avg %s rows/h (%d hourly buckets; measured in %s)\n",
				hStart.Format("2006-01-02 15:04"), hEnd.Format("2006-01-02 15:04"), fmtInt(total), hours, fmtInt(int64(avg)), buckets, fmtDur(time.Since(t1)))
		}
		for h := range profile {
			profile[h] = avg
		}
		if !partial && hours >= 24 && total > 0 {
			// per hour-of-day profile (each bucket may hold several days: normalise)
			perBucket := hours / 24
			lo, hi := 0, 0
			for h := range profile {
				if counts[h] > 0 {
					profile[h] = float64(counts[h]) / perBucket
				}
				if profile[h] < profile[lo] {
					lo = h
				}
				if profile[h] > profile[hi] {
					hi = h
				}
			}
			fmt.Printf("hourly profile  min %s rows/h at %02d:00, max %s rows/h at %02d:00 UTC (the feed follows this hour-of-day shape)\n",
				fmtInt(int64(profile[lo])), lo, fmtInt(int64(profile[hi])), hi)
		}
	} else {
		start = now.Add(-o.emptyBackfill)
		fmt.Printf("table is empty: making up the last %s at the default rate %s rows/h\n", fmtDur(o.emptyBackfill), fmtInt(int64(avg)))
		for h := range profile {
			profile[h] = avg
		}
	}
	if o.rate > 0 {
		for h := range profile {
			profile[h] = o.rate
		}
		avg = o.rate
		fmt.Printf("rate override   %s rows/h\n", fmtInt(int64(o.rate)))
	}
	rate := func(tMs int64) float64 { return profile[time.UnixMilli(tMs).UTC().Hour()] }

	// ---- 2. fleet identity + shifts in progress at the end of the data
	known := map[int]fleetInfo{}
	var resumed []activeRow
	if found {
		t1 := time.Now()
		known, err = db.fleetMap(ctx, last.Add(-o.fleetWindow))
		if err != nil {
			return fmt.Errorf("fleet map: %w", err)
		}
		resumed, err = db.activeAt(ctx, last)
		if err != nil {
			return fmt.Errorf("active taxis: %w", err)
		}
		fmt.Printf("fleet           %s of %s taxis have plate/company from Doris (last %s); %d shifts in progress at the last event (%s)\n",
			fmtInt(int64(len(known))), fmtInt(int64(o.taxis)), fmtDur(o.fleetWindow), len(resumed), fmtDur(time.Since(t1)))
	}
	fleet := newFleet(o.taxis, o.platePrefix, companies, known)

	// ---- 3. sim + loader
	seed := o.seed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	ld, err := newLoader(LoaderConfig{
		StreamLoadURL: env["VELODB_STREAM_LOAD_URL"], Database: o.database, Table: o.table,
		User: env["VELODB_USER"], Password: env["VELODB_PASSWORD"],
		BatchBytes: o.batchMiB << 20, Linger: o.flush, Workers: o.workers, ChunkRows: o.chunkRows, DryRun: o.dryRun,
	})
	if err != nil {
		return fmt.Errorf("stream load client: %w", err)
	}
	row := make([]byte, 0, 160)
	emit := func(p *Ping) {
		row = row[:0]
		row = time.UnixMilli(p.TsMs).UTC().AppendFormat(row, "2006-01-02 15:04:05.000")
		row = append(row, ',')
		row = strconv.AppendInt(row, int64(p.TaxiID), 10)
		row = append(row, ',')
		row = geohashInto(row, p.Lat, p.Lon, 6)
		row = append(row, ',')
		row = geohashInto(row, p.Lat, p.Lon, 8)
		row = append(row, ',')
		row = strconv.AppendFloat(row, p.Lat, 'f', 6, 64)
		row = append(row, ',')
		row = strconv.AppendFloat(row, p.Lon, 'f', 6, 64)
		row = append(row, ',')
		row = append(row, p.Status.String()...)
		row = append(row, ',')
		row = strconv.AppendInt(row, int64(p.Speed), 10)
		row = append(row, ',')
		row = strconv.AppendInt(row, int64(p.Heading), 10)
		row = append(row, ',')
		row = append(row, fleet.plates[p.TaxiID]...)
		row = append(row, ',')
		row = append(row, fleet.companies[p.TaxiID]...)
		ld.Add(string(row))
	}
	cfg := SimConfig{
		Taxis: o.taxis, PingMs: int64(o.pingSecs) * 1000, JitterMs: int64(o.jitterSecs) * 1000,
		ShiftMinMs: int64(o.shiftMin) * 60000, ShiftMaxMs: int64(o.shiftMax) * 60000,
	}
	sim := newSim(cfg, st, fleet, seed, start.UnixMilli(), rate, emit)
	for _, a := range resumed {
		v := a.v
		v.DestLat, v.DestLon = stationDest(sim.rng, st)
		sim.resume(a.id, &v, a.lastMs, sim.Now)
	}
	filled := sim.Fill()

	// estimated rows in the gap
	var est float64
	for t := start; t.Before(now); t = t.Add(time.Minute) {
		est += rate(t.UnixMilli()) / 60
	}
	fmt.Printf("plan            make up %s .. now (%s) ~ %s rows, %d taxis on shift (%d resumed, %d new); then realtime at ~%s rows/h\n",
		start.Format("2006-01-02 15:04:05"), fmtDur(now.Sub(start)), fmtInt(int64(est)), sim.Active(), sim.Active()-filled, filled, fmtInt(int64(avg)))
	fmt.Println()

	// ---- 4. make up the gap as fast as possible
	tty := isTTY()
	genStart := time.Now()
	lastDraw := time.Time{}
	gapStartMs := start.UnixMilli()
	var genEndMs int64 // set once generation has caught up: freezes the progress denominator
	draw := func(final bool) {
		nowMs := time.Now().UnixMilli()
		if genEndMs > 0 {
			nowMs = genEndMs
		}
		done := float64(sim.Now-gapStartMs) / math.Max(1, float64(nowMs-gapStartMs))
		if done > 1 {
			done = 1
		}
		issued := ld.Issued.Load()
		el := time.Since(genStart)
		rps := float64(issued) / math.Max(el.Seconds(), 0.001)
		eta := "-"
		if done > 0.001 && done < 1 {
			eta = fmtDur(time.Duration(float64(el) * (1 - done) / done))
		}
		bar := [30]byte{}
		for i := range bar {
			if float64(i) < done*30 {
				bar[i] = '='
			} else {
				bar[i] = ' '
			}
		}
		line := fmt.Sprintf("makeup [%s] %5.1f%%  sim %s  gen %s  loaded %s  in-flight %s  loads %d  %s rows/s  ETA %s",
			string(bar[:]), done*100, fmtTs(sim.Now), fmtInt(issued), fmtInt(ld.Loaded.Load()), fmtInt(ld.InFlight()), ld.Loads.Load(), fmtInt(int64(rps)), eta)
		if e := ld.LastErr(); e != "" {
			line += "  ERR " + e
		}
		if tty {
			fmt.Printf("\r\033[K%s", line)
			if final {
				fmt.Println()
			}
		} else {
			fmt.Println(line)
		}
	}
	const stepMs = 60_000
	drawEvery := map[bool]time.Duration{true: 150 * time.Millisecond, false: 5 * time.Second}[tty]
	for ctx.Err() == nil {
		wall := time.Now().UnixMilli()
		if sim.Now >= wall {
			break
		}
		until := sim.Now + stepMs
		if until > wall {
			until = wall
		}
		sim.Advance(until)
		if time.Since(lastDraw) > drawEvery {
			draw(false)
			lastDraw = time.Now()
		}
	}
	genEndMs = time.Now().UnixMilli()
	ld.Flush()
	// wait for delivery of everything generated so far
	for ctx.Err() == nil && ld.InFlight() > 0 {
		if time.Since(lastDraw) > drawEvery {
			draw(false)
			lastDraw = time.Now()
		}
		time.Sleep(200 * time.Millisecond)
	}
	draw(true)
	if ctx.Err() != nil {
		fmt.Println("interrupted during make-up; draining...")
		ld.Close()
		printSummary(ld, sim)
		return nil
	}
	fmt.Printf("✔ up to date: %s rows made up to %s UTC in %s (%s rows loaded, %s failed, %s filtered, %d loads)\n",
		fmtInt(ld.Issued.Load()), fmtTs(sim.Now), fmtDur(time.Since(genStart)),
		fmtInt(ld.Loaded.Load()), fmtInt(ld.Failed.Load()), fmtInt(ld.Filtered.Load()), ld.Loads.Load())
	if o.makeupOnly {
		ld.Close()
		printSummary(ld, sim)
		return nil
	}

	// ---- 5. realtime: emit pings as the wall clock reaches them
	fmt.Printf("▶ realtime: flushing every %s, reporting every %s (Ctrl-C to stop)\n", o.flush, o.report)
	rtStart := time.Now()
	lastFlush, lastReport := time.Now(), time.Now()
	lastReportIssued := ld.Issued.Load()
	for ctx.Err() == nil {
		if time.Since(lastFlush) >= o.flush {
			ld.Flush()
			lastFlush = time.Now()
		}
		if time.Since(lastReport) >= o.report {
			issued := ld.Issued.Load()
			perMin := float64(issued-lastReportIssued) / time.Since(lastReport).Minutes()
			lastReportIssued, lastReport = issued, time.Now()
			line := fmt.Sprintf("%s  realtime up to %s UTC  active %d  issued %s  loaded %s  in-flight %s  failed %s  loads %d  %s rows/min  (%s since start)",
				time.Now().UTC().Format("15:04:05"), fmtTs(sim.Now), sim.Active(), fmtInt(issued), fmtInt(ld.Loaded.Load()),
				fmtInt(ld.InFlight()), fmtInt(ld.Failed.Load()), ld.Loads.Load(), fmtInt(int64(perMin)), fmtDur(time.Since(rtStart)))
			if e := ld.LastErr(); e != "" {
				line += "  lastErr: " + e
			}
			fmt.Println(line)
		}
		wall := time.Now().UnixMilli()
		if next := sim.NextEvent(); next > wall {
			d := time.Duration(next-wall) * time.Millisecond
			if d > 250*time.Millisecond {
				d = 250 * time.Millisecond
			}
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
			continue
		}
		sim.Advance(wall + 1)
	}
	fmt.Println("\nstopping: draining stream loads...")
	ld.Close()
	printSummary(ld, sim)
	return nil
}

func printSummary(ld *Loader, sim *Sim) {
	s := ld.Stats()
	fmt.Printf("summary: sim up to %s UTC | issued %s | loaded %s | failed %s | filtered %s | loads %d (%d errors) | %s bytes | avg load %s p99 %s | shifts started %d\n",
		fmtTs(sim.Now), fmtInt(ld.Issued.Load()), fmtInt(ld.Loaded.Load()), fmtInt(ld.Failed.Load()), fmtInt(ld.Filtered.Load()),
		s.TotalLoadJobs, s.ErrorJobs, fmtInt(s.TotalBytesSent), s.AverageLoadTime.Round(time.Millisecond), s.P99LoadTime.Round(time.Millisecond), sim.Shifts)
}
