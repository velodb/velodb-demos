package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func loadEnv(path string) map[string]string {
	env := map[string]string{}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				v = strings.TrimSpace(v)
				if i := strings.Index(v, " #"); i >= 0 {
					v = strings.TrimSpace(v[:i])
				}
				env[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = strings.Trim(v, `"'`)
			}
		}
	}
	// variables set in the environment override the file (see env.example)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "VELODB_") && v != "" {
			env[k] = v
		}
	}
	if env["VELODB_MYSQL_PORT"] == "" {
		env["VELODB_MYSQL_PORT"] = "9030"
	}
	if env["VELODB_STREAM_LOAD_URL"] == "" && env["VELODB_HOST"] != "" {
		port := env["VELODB_STREAM_LOAD_PORT"]
		if port == "" {
			port = "8080"
		}
		env["VELODB_STREAM_LOAD_URL"] = "http://" + env["VELODB_HOST"] + ":" + port
	}
	return env
}

type DB struct {
	*sql.DB
	fq string
}

func openDB(env map[string]string, database, table string) (*DB, error) {
	port := env["VELODB_MYSQL_PORT"]
	cfg := mysql.NewConfig() // FormatDSN escapes passwords with / @ ? : etc.
	cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = env["VELODB_USER"], env["VELODB_PASSWORD"], "tcp", env["VELODB_HOST"]+":"+port, database
	cfg.ParseTime, cfg.Loc, cfg.Timeout, cfg.ReadTimeout, cfg.InterpolateParams = true, time.UTC, 10*time.Second, 600*time.Second, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connect %s:%s: %w", env["VELODB_HOST"], port, err)
	}
	return &DB{DB: db, fq: "`" + database + "`.`" + table + "`"}, nil
}

// lastTs returns the newest ping in the table. Recent partitions are tried first so the
// common case (feeder restarted after a short pause) prunes to a couple of partitions.
func (d *DB) lastTs(ctx context.Context) (time.Time, bool, error) {
	var t sql.NullTime
	for _, since := range []time.Duration{7 * 24 * time.Hour, 0} {
		q := "SELECT max(ts) FROM " + d.fq
		var args []any
		if since > 0 {
			q += " WHERE ts >= ?"
			args = append(args, time.Now().UTC().Add(-since))
		}
		if err := d.QueryRowContext(ctx, q, args...).Scan(&t); err != nil {
			return time.Time{}, false, err
		}
		if t.Valid {
			return t.Time.UTC(), true, nil
		}
	}
	return time.Time{}, false, nil
}

// hourlyCounts returns rows per hour-of-day (UTC) in [from, to) and the oldest ts seen there,
// so a partially covered window (table younger than the window) can be detected.
func (d *DB) hourlyCounts(ctx context.Context, from, to time.Time) ([24]int64, time.Time, error) {
	var out [24]int64
	var minTs time.Time
	rows, err := d.QueryContext(ctx,
		"SELECT hour(ts), count(*), min(ts) FROM "+d.fq+" WHERE ts >= ? AND ts < ? GROUP BY 1", from, to)
	if err != nil {
		return out, minTs, err
	}
	defer rows.Close()
	for rows.Next() {
		var h int
		var n int64
		var m sql.NullTime
		if err := rows.Scan(&h, &n, &m); err != nil {
			return out, minTs, err
		}
		if h >= 0 && h < 24 {
			out[h] += n
		}
		if m.Valid && (minTs.IsZero() || m.Time.Before(minTs)) {
			minTs = m.Time.UTC()
		}
	}
	return out, minTs, rows.Err()
}

type fleetInfo struct{ plate, company string }

// fleetMap reads plate/company per taxi seen since `from` so new pings match history.
func (d *DB) fleetMap(ctx context.Context, from time.Time) (map[int]fleetInfo, error) {
	rows, err := d.QueryContext(ctx,
		"SELECT taxi_id, max(plate), max(company) FROM "+d.fq+" WHERE ts >= ? GROUP BY taxi_id", from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int]fleetInfo{}
	for rows.Next() {
		var id int
		var fi fleetInfo
		if err := rows.Scan(&id, &fi.plate, &fi.company); err != nil {
			return nil, err
		}
		m[id] = fi
	}
	return m, rows.Err()
}

type activeRow struct {
	id     int
	v      Vehicle
	lastMs int64
}

// activeAt returns the taxis whose shift was in progress at `last` (latest ping within the
// preceding 2 minutes and not OFFLINE), so the feed continues their trajectories.
func (d *DB) activeAt(ctx context.Context, last time.Time) ([]activeRow, error) {
	q := `SELECT taxi_id, lat, lon, heading, speed_kmh, status, ts FROM (
	        SELECT taxi_id, lat, lon, heading, speed_kmh, status, ts,
	               row_number() OVER (PARTITION BY taxi_id ORDER BY ts DESC) AS rn
	        FROM ` + d.fq + ` WHERE ts >= ? AND ts <= ?) t
	      WHERE rn = 1 AND status <> 'OFFLINE'`
	rows, err := d.QueryContext(ctx, q, last.Add(-2*time.Minute), last)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activeRow
	for rows.Next() {
		var a activeRow
		var status string
		var ts time.Time
		var heading, speed int
		if err := rows.Scan(&a.id, &a.v.Lat, &a.v.Lon, &heading, &speed, &status, &ts); err != nil {
			return nil, err
		}
		a.v.Heading, a.v.Speed, a.v.Status = float64(heading), float64(speed), parseStatus(status)
		a.lastMs = ts.UnixMilli()
		out = append(out, a)
	}
	return out, rows.Err()
}
