package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

const tsLayout = "2006-01-02 15:04:05.000"

type DB struct {
	*sql.DB
	Database, Table string
	fq              string

	metaMu sync.RWMutex
	meta   Meta
}

type Meta struct {
	Rows       int64     `json:"rows"`
	MinTs      time.Time `json:"min_ts"`
	MaxTs      time.Time `json:"max_ts"`
	Partitions int       `json:"partitions"`
	Buckets    int       `json:"buckets"`
	RefreshMs  float64   `json:"refresh_ms"`
	UpdatedAt  time.Time `json:"updated_at"`
	Error      string    `json:"error,omitempty"`
}

type Taxi struct {
	TaxiID   int       `json:"taxi_id"`
	Plate    string    `json:"plate"`
	Company  string    `json:"company"`
	Status   string    `json:"status"`
	Speed    int       `json:"speed_kmh"`
	Heading  int       `json:"heading"`
	Lat      float64   `json:"lat"`
	Lon      float64   `json:"lon"`
	Geohash6 string    `json:"geohash6"`
	LastSeen time.Time `json:"last_seen"`
	AgeMs    int64     `json:"age_ms"`
	DistM    float64   `json:"dist_m"`
}

type Pruning struct {
	Partitions      string `json:"partitions"`
	PartitionsUsed  int    `json:"partitions_used"`
	PartitionsTotal int    `json:"partitions_total"`
	Tablets         string `json:"tablets"`
	TabletsUsed     int    `json:"tablets_used"`
	TabletsTotal    int    `json:"tablets_total"`
}

type SearchResult struct {
	T         time.Time `json:"t"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	RadiusM   int       `json:"radius_m"`
	LookbackS int       `json:"lookback_s"`
	Mode      string    `json:"mode"`
	Cells     []string  `json:"cells"`
	Taxis     []Taxi    `json:"taxis"`
	QueryMs   float64   `json:"query_ms"`
	ExplainMs float64   `json:"explain_ms"`
	Pruning   Pruning   `json:"pruning"`
	SQL       string    `json:"sql"`
}

type TrailPoint struct {
	Ts      time.Time `json:"ts"`
	Lat     float64   `json:"lat"`
	Lon     float64   `json:"lon"`
	Status  string    `json:"status"`
	Speed   int       `json:"speed_kmh"`
	Heading int       `json:"heading"`
}

type TrailResult struct {
	TaxiID  int          `json:"taxi_id"`
	From    time.Time    `json:"from"`
	To      time.Time    `json:"to"`
	Points  []TrailPoint `json:"points"`
	QueryMs float64      `json:"query_ms"`
	Pruning Pruning      `json:"pruning"`
	SQL     string       `json:"sql"`
}

func OpenDB(host string, port int, user, password, database, table string) (*DB, error) {
	cfg := mysql.NewConfig() // FormatDSN escapes passwords with / @ ? : etc.
	cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = user, password, "tcp", fmt.Sprintf("%s:%d", host, port), database
	cfg.ParseTime, cfg.Loc, cfg.Timeout, cfg.ReadTimeout, cfg.InterpolateParams = true, time.UTC, 10*time.Second, 300*time.Second, true
	cfg.Params = map[string]string{"enable_sql_cache": "false"}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(10 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connect %s:%d: %w", host, port, err)
	}
	return &DB{DB: db, Database: database, Table: table, fq: "`" + database + "`.`" + table + "`"}, nil
}

func (d *DB) RefreshMeta(ctx context.Context) {
	t0 := time.Now()
	var m Meta
	var minTs, maxTs sql.NullTime
	err := d.QueryRowContext(ctx, "SELECT count(*), min(ts), max(ts) FROM "+d.fq).Scan(&m.Rows, &minTs, &maxTs)
	if err == nil {
		m.MinTs, m.MaxTs = minTs.Time.UTC(), maxTs.Time.UTC()
		err = d.QueryRowContext(ctx,
			"SELECT count(*) FROM information_schema.partitions WHERE table_schema=? AND table_name=?",
			d.Database, d.Table).Scan(&m.Partitions)
	}
	if err == nil {
		var name, create string
		if e := d.QueryRowContext(ctx, "SHOW CREATE TABLE "+d.fq).Scan(&name, &create); e == nil {
			if mm := regexp.MustCompile(`BUCKETS (\d+)`).FindStringSubmatch(create); mm != nil {
				fmt.Sscanf(mm[1], "%d", &m.Buckets)
			}
		}
	}
	if err != nil {
		m.Error = err.Error()
	}
	m.RefreshMs = ms(t0)
	m.UpdatedAt = time.Now().UTC()
	d.metaMu.Lock()
	if err != nil && d.meta.Rows > 0 { // keep last good values, just record the error
		d.meta.Error = m.Error
		d.meta.UpdatedAt = m.UpdatedAt
	} else {
		d.meta = m
	}
	d.metaMu.Unlock()
}

func (d *DB) GetMeta() Meta {
	d.metaMu.RLock()
	defer d.metaMu.RUnlock()
	return d.meta
}

func ms(t0 time.Time) float64 { return float64(time.Since(t0).Microseconds()) / 1000 }

func sqlList(cells []string) string {
	q := make([]string, len(cells))
	for i, c := range cells {
		q[i] = "'" + c + "'"
	}
	return strings.Join(q, ",")
}

// Search returns each taxi's last ping at or before t (within lookback) inside radius of (lat, lon).
// mode: "pruned" (ts + geohash6 cells) or "nocell" (ts only — for the side-by-side comparison).
func (d *DB) Search(ctx context.Context, lat, lon float64, t time.Time, radiusM, lookbackS int, mode string) (*SearchResult, error) {
	cells := Cover(lat, lon, float64(radiusM), 6)
	from := t.Add(-time.Duration(lookbackS) * time.Second)
	cellPred := ""
	if mode != "nocell" {
		cellPred = fmt.Sprintf("\n    AND geohash6 IN (%s)", sqlList(cells))
	}
	q := fmt.Sprintf(`SELECT taxi_id, plate, company, status, speed_kmh, heading, lat, lon, geohash6, ts,
       st_distance_sphere(lon, lat, %[3]f, %[4]f) AS dist_m
FROM (
  SELECT *, row_number() OVER (PARTITION BY taxi_id ORDER BY ts DESC) AS rn
  FROM %[1]s
  WHERE ts > '%[5]s' AND ts <= '%[6]s'%[7]s
) t
WHERE rn = 1 AND st_distance_sphere(lon, lat, %[3]f, %[4]f) <= %[2]d
ORDER BY dist_m`, d.fq, radiusM, lon, lat, from.UTC().Format(tsLayout), t.UTC().Format(tsLayout), cellPred)

	res := &SearchResult{T: t.UTC(), Lat: lat, Lon: lon, RadiusM: radiusM, LookbackS: lookbackS, Mode: mode, Cells: cells, SQL: q, Taxis: []Taxi{}}

	// EXPLAIN concurrently with the query itself.
	var pr Pruning
	var explainMs float64
	done := make(chan struct{})
	go func() {
		defer close(done)
		t0 := time.Now()
		pr = d.explain(ctx, q)
		explainMs = ms(t0)
	}()

	t0 := time.Now()
	rows, err := d.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x Taxi
		var ts time.Time
		if err := rows.Scan(&x.TaxiID, &x.Plate, &x.Company, &x.Status, &x.Speed, &x.Heading, &x.Lat, &x.Lon, &x.Geohash6, &ts, &x.DistM); err != nil {
			return nil, err
		}
		x.LastSeen = ts.UTC()
		x.AgeMs = t.Sub(x.LastSeen).Milliseconds()
		res.Taxis = append(res.Taxis, x)
	}
	res.QueryMs = ms(t0)
	<-done
	res.Pruning = pr
	res.ExplainMs = explainMs
	return res, rows.Err()
}

// Trail returns one taxi's pings in [t-minutes, t+minutes].
func (d *DB) Trail(ctx context.Context, taxiID int, t time.Time, minutes int) (*TrailResult, error) {
	from := t.Add(-time.Duration(minutes) * time.Minute)
	to := t.Add(time.Duration(minutes) * time.Minute)
	q := fmt.Sprintf(`SELECT ts, lat, lon, status, speed_kmh, heading
FROM %s
WHERE taxi_id = %d AND ts >= '%s' AND ts <= '%s'
ORDER BY ts`, d.fq, taxiID, from.UTC().Format(tsLayout), to.UTC().Format(tsLayout))
	res := &TrailResult{TaxiID: taxiID, From: from.UTC(), To: to.UTC(), SQL: q, Points: []TrailPoint{}}
	var pr Pruning
	done := make(chan struct{})
	go func() { defer close(done); pr = d.explain(ctx, q) }()
	t0 := time.Now()
	rows, err := d.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p TrailPoint
		var ts time.Time
		if err := rows.Scan(&ts, &p.Lat, &p.Lon, &p.Status, &p.Speed, &p.Heading); err != nil {
			return nil, err
		}
		p.Ts = ts.UTC()
		res.Points = append(res.Points, p)
	}
	res.QueryMs = ms(t0)
	<-done
	res.Pruning = pr
	return res, rows.Err()
}

type HistoryResult struct {
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	RadiusM   int       `json:"radius_m"`
	Mode      string    `json:"mode"`
	Cells     []string  `json:"cells"`
	Pings     int64     `json:"pings"`
	Taxis     int64     `json:"taxis"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	QueryMs   float64   `json:"query_ms"`
	Pruning   Pruning   `json:"pruning"`
	SQL       string    `json:"sql"`
}

// History counts every ping ever recorded inside the circle (all partitions). With mode "pruned" the
// geohash6 cells restrict the scan to a few tablets per partition; "nocell" is the full 1B-row scan.
func (d *DB) History(ctx context.Context, lat, lon float64, radiusM int, mode string) (*HistoryResult, error) {
	cells := Cover(lat, lon, float64(radiusM), 6)
	cellPred := ""
	if mode != "nocell" {
		cellPred = fmt.Sprintf("\n  AND geohash6 IN (%s)", sqlList(cells))
	}
	q := fmt.Sprintf(`SELECT count(*) AS pings, count(DISTINCT taxi_id) AS taxis, min(ts) AS first_seen, max(ts) AS last_seen
FROM %s
WHERE st_distance_sphere(lon, lat, %f, %f) <= %d%s`, d.fq, lon, lat, radiusM, cellPred)
	res := &HistoryResult{Lat: lat, Lon: lon, RadiusM: radiusM, Mode: mode, Cells: cells, SQL: q}
	var pr Pruning
	done := make(chan struct{})
	go func() { defer close(done); pr = d.explain(ctx, q) }()
	t0 := time.Now()
	var first, last sql.NullTime
	if err := d.QueryRowContext(ctx, q).Scan(&res.Pings, &res.Taxis, &first, &last); err != nil {
		return nil, err
	}
	res.QueryMs = ms(t0)
	res.FirstSeen, res.LastSeen = first.Time.UTC(), last.Time.UTC()
	<-done
	res.Pruning = pr
	return res, nil
}

var (
	rePartitions = regexp.MustCompile(`partitions=(\d+)/(\d+)`)
	reTablets    = regexp.MustCompile(`tablets=(\d+)/(\d+)`)
)

func (d *DB) explain(ctx context.Context, q string) Pruning {
	var pr Pruning
	rows, err := d.QueryContext(ctx, "EXPLAIN "+q)
	if err != nil {
		return pr
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if rows.Scan(&line) != nil {
			continue
		}
		if m := rePartitions.FindStringSubmatch(line); m != nil && pr.Partitions == "" {
			pr.Partitions = m[1] + "/" + m[2]
			fmt.Sscanf(m[1], "%d", &pr.PartitionsUsed)
			fmt.Sscanf(m[2], "%d", &pr.PartitionsTotal)
		}
		if m := reTablets.FindStringSubmatch(line); m != nil && pr.Tablets == "" {
			pr.Tablets = m[1] + "/" + m[2]
			fmt.Sscanf(m[1], "%d", &pr.TabletsUsed)
			fmt.Sscanf(m[2], "%d", &pr.TabletsTotal)
		}
	}
	return pr
}
