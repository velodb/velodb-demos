// Taxi hyper-search demo web app: "which taxis were within R metres of X at time T?"
// against 1B rows in Doris, showing the partition / bucket / inverted-index pruning at work.
package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

type Station struct {
	Name  string  `json:"name"`
	Lines string  `json:"lines"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}

type server struct {
	db       *DB
	stations []Station
	static   http.Handler
}

func loadEnv(path string) map[string]string {
	env := map[string]string{}
	if f, err := os.Open(path); err == nil {
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
	return env
}

func loadStations(path string) ([]Station, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	var out []Station
	for i, r := range recs {
		if i == 0 || len(r) < 4 {
			continue
		}
		lat, _ := strconv.ParseFloat(r[2], 64)
		lon, _ := strconv.ParseFloat(r[3], 64)
		out = append(out, Station{Name: r[0], Lines: r[1], Lat: lat, Lon: lon})
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func qFloat(r *http.Request, k string, def float64) float64 {
	if v, err := strconv.ParseFloat(r.URL.Query().Get(k), 64); err == nil {
		return v
	}
	return def
}

func qInt(r *http.Request, k string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(k)); err == nil {
		return v
	}
	return def
}

func qTime(r *http.Request, k string, def time.Time) (time.Time, error) {
	s := r.URL.Query().Get(k)
	if s == "" {
		return def, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000", "2006-01-02T15:04:05", "2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC(), nil
		}
	}
	return def, fmt.Errorf("bad time %q (use RFC3339, e.g. 2026-08-26T13:37:38.917Z)", s)
}

func (s *server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "1" {
		s.db.RefreshMeta(r.Context())
	}
	writeJSON(w, 200, map[string]any{"meta": s.db.GetMeta(), "stations": s.stations, "database": s.db.Database, "table": s.db.Table})
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	meta := s.db.GetMeta()
	t, err := qTime(r, "t", meta.MaxTs)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	lat := qFloat(r, "lat", 1.2840)
	lon := qFloat(r, "lon", 103.8515)
	radius := qInt(r, "radius", 500)
	lookback := qInt(r, "lookback", 120)
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "pruned"
	}
	if radius < 50 || radius > 20000 || lookback < 10 || lookback > 3600 {
		writeErr(w, 400, fmt.Errorf("radius must be 50..20000 m, lookback 10..3600 s"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res, err := s.db.Search(ctx, lat, lon, t, radius, lookback, mode)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	log.Printf("search t=%s lat=%.4f lon=%.4f r=%dm mode=%s -> %d taxis in %.0f ms (partitions %s, tablets %s)",
		t.Format(tsLayout), lat, lon, radius, mode, len(res.Taxis), res.QueryMs, res.Pruning.Partitions, res.Pruning.Tablets)
	writeJSON(w, 200, res)
}

func (s *server) handleTrail(w http.ResponseWriter, r *http.Request) {
	meta := s.db.GetMeta()
	t, err := qTime(r, "t", meta.MaxTs)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	id := qInt(r, "taxi_id", -1)
	if id < 0 {
		writeErr(w, 400, fmt.Errorf("taxi_id required"))
		return
	}
	minutes := qInt(r, "minutes", 15)
	if minutes < 1 || minutes > 720 {
		writeErr(w, 400, fmt.Errorf("minutes must be 1..720"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.db.Trail(ctx, id, t, minutes)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	log.Printf("trail taxi=%d t=%s ±%dm -> %d points in %.0f ms", id, t.Format(tsLayout), minutes, len(res.Points), res.QueryMs)
	writeJSON(w, 200, res)
}

func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	lat := qFloat(r, "lat", 1.2840)
	lon := qFloat(r, "lon", 103.8515)
	radius := qInt(r, "radius", 500)
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "pruned"
	}
	if radius < 50 || radius > 20000 {
		writeErr(w, 400, fmt.Errorf("radius must be 50..20000 m"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	res, err := s.db.History(ctx, lat, lon, radius, mode)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	log.Printf("history lat=%.4f lon=%.4f r=%dm mode=%s -> %d pings / %d taxis in %.0f ms (partitions %s, tablets %s)",
		lat, lon, radius, mode, res.Pings, res.Taxis, res.QueryMs, res.Pruning.Partitions, res.Pruning.Tablets)
	writeJSON(w, 200, res)
}

func main() {
	listen := flag.String("listen", ":9727", "listen address")
	envPath := flag.String("env", "../.env", "env file with VELODB_* settings (see env.example); VELODB_* environment variables override it")
	stationsPath := flag.String("stations", "../mrt_stations.csv", "MRT station list")
	devStatic := flag.String("dev-static", "", "serve static files from this directory instead of the embedded copy (live editing)")
	flag.Parse()

	env := loadEnv(*envPath)
	port, _ := strconv.Atoi(env["VELODB_MYSQL_PORT"])
	if port == 0 {
		port = 9030
	}
	database := env["VELODB_DATABASE"]
	if database == "" {
		database = "demo_hyper_search"
	}
	table := env["VELODB_TABLE"]
	if table == "" {
		table = "taxi_ping"
	}
	if env["VELODB_HOST"] == "" || env["VELODB_USER"] == "" {
		log.Fatalf("VELODB_HOST / VELODB_USER not set (env file %s or environment)", *envPath)
	}

	db, err := OpenDB(env["VELODB_HOST"], port, env["VELODB_USER"], env["VELODB_PASSWORD"], database, table)
	if err != nil {
		log.Fatal(err)
	}
	stations, err := loadStations(*stationsPath)
	if err != nil {
		log.Fatalf("stations: %v", err)
	}

	ctx := context.Background()
	db.RefreshMeta(ctx)
	m := db.GetMeta()
	if m.Error != "" {
		log.Printf("WARNING: meta refresh failed: %s", m.Error)
	}
	log.Printf("connected to %s:%d %s.%s: %d rows, ts %s .. %s, %d partitions, %d buckets (%.0f ms)",
		env["VELODB_HOST"], port, database, table, m.Rows, m.MinTs.Format(tsLayout), m.MaxTs.Format(tsLayout), m.Partitions, m.Buckets, m.RefreshMs)
	go func() {
		for range time.Tick(15 * time.Second) {
			db.RefreshMeta(ctx)
		}
	}()

	var static http.Handler
	if *devStatic != "" {
		static = http.FileServer(http.Dir(*devStatic))
	} else {
		sub, _ := fs.Sub(staticFS, "static")
		static = http.FileServer(http.FS(sub))
	}
	s := &server{db: db, stations: stations, static: static}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/meta", s.handleMeta)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/trail", s.handleTrail)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.Handle("/", static)

	log.Printf("listening on http://localhost%s", strings.TrimPrefix(*listen, "0.0.0.0"))
	log.Fatal(http.ListenAndServe(*listen, mux))
}
