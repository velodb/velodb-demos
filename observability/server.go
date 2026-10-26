package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

type serverConfig struct {
	DSN      string
	Database string
	Table    string
}

type filterForm struct {
	TimeStart     string `json:"time_start"`
	TimeEnd       string `json:"time_end"`
	HostOperator  string `json:"host_operator"`
	HostValue     string `json:"host_value"`
	CountryCode   string `json:"country_code"`
	City          string `json:"city"`
	MessagePhrase string `json:"message_phrase"`
	Severity      string `json:"severity"`
	Limit         int    `json:"limit"`
}

type queryResult struct {
	Columns   []string   `json:"columns"`
	Rows      [][]string `json:"rows"`
	SQL       string     `json:"sql"`
	ElapsedMS int64      `json:"elapsed_ms"`
	Error     string     `json:"error,omitempty"`
}

type rowCountResult struct {
	Rows      int64  `json:"rows"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Error     string `json:"error,omitempty"`
}

type pageData struct {
	EnvPath     string
	Target      string
	QueryAnchor string
	Form        filterForm
	Result      queryResult
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "recreate-table" {
		if err := runRecreateTable(args[1:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(args) > 0 && args[0] == "server" {
		if err := runServer(args[1:]); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := runLoader(args); err != nil {
		log.Fatal(err)
	}
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	envPath := fs.String("env", ".env", "Optional env file; VELODB_* and tuning variables in the environment take precedence")
	addr := fs.String("addr", ":9273", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadServerConfig(*envPath)
	if err != nil {
		return err
	}
	if err := validateIdentifier("DATABASE", cfg.Database); err != nil {
		return err
	}
	if err := validateIdentifier("TABLE", cfg.Table); err != nil {
		return err
	}

	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return fmt.Errorf("open Doris SQL connection: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("connect to Doris SQL endpoint: %w", err)
	}

	app := &logUI{
		db:       db,
		envPath:  *envPath,
		target:   cfg.Database + "." + cfg.Table,
		table:    fmt.Sprintf("`%s`.`%s`", cfg.Database, cfg.Table),
		loadJobs: newLoadJobController(*envPath),
	}

	appMux := http.NewServeMux()
	appMux.HandleFunc("/", app.handleIndex)
	appMux.HandleFunc("/loadgen", app.handleLoadGen)
	appMux.HandleFunc("/api/logs", app.handleLogsAPI)
	appMux.HandleFunc("/api/row-count", app.handleRowCountAPI)
	appMux.HandleFunc("/api/loadgen/status", app.handleLoadGenStatusAPI)
	appMux.HandleFunc("/api/loadgen/backfill/start", app.handleLoadGenStartAPI("backfill"))
	appMux.HandleFunc("/api/loadgen/backfill/stop", app.handleLoadGenStopAPI("backfill"))

	mux := http.NewServeMux()
	mux.Handle("/logsearch/", http.StripPrefix("/logsearch", appMux))
	mux.HandleFunc("/logsearch", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/logsearch/", http.StatusPermanentRedirect)
	})

	fmt.Printf("serving log UI for %s on http://127.0.0.1%s/logsearch/\n", app.target, displayAddr(*addr))
	return http.ListenAndServe(*addr, mux)
}

func loadServerConfig(envPath string) (serverConfig, error) {
	values, err := loadSettings(envPath)
	if err != nil {
		return serverConfig{}, err
	}
	conn, err := loadConnection(values)
	if err != nil {
		return serverConfig{}, err
	}

	cfg := mysql.Config{
		User:                 conn.User,
		Passwd:               conn.Password,
		Net:                  "tcp",
		Addr:                 conn.MySQLAddr,
		AllowNativePasswords: true,
		Timeout:              10 * time.Second,
		ReadTimeout:          30 * time.Second,
		WriteTimeout:         30 * time.Second,
		Params: map[string]string{
			"parseTime": "true",
		},
	}
	return serverConfig{
		DSN:      cfg.FormatDSN(),
		Database: conn.Database,
		Table:    conn.Table,
	}, nil
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" {
			return ":" + port
		}
		return addr
	}
	return addr
}

type logUI struct {
	db       *sql.DB
	envPath  string
	target   string
	table    string
	loadJobs *loadJobController
}

func (app *logUI) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	form := parseFilterForm(r)

	data := pageData{
		EnvPath:     app.envPath,
		Target:      app.target,
		QueryAnchor: queryTimeAnchorTime.Format("2006-01-02T15:04:05Z"),
		Form:        form,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := logTemplate.Execute(w, data); err != nil {
		log.Printf("render failed: %v", err)
	}
}

func (app *logUI) handleLoadGen(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/loadgen" {
		http.NotFound(w, r)
		return
	}

	data := pageData{
		EnvPath:     app.envPath,
		Target:      app.target,
		QueryAnchor: queryTimeAnchorTime.Format("2006-01-02T15:04:05Z"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := loadGenTemplate.Execute(w, data); err != nil {
		log.Printf("render load generation UI failed: %v", err)
	}
}

func (app *logUI) handleLogsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result := app.queryLogs(r, parseFilterForm(r))
	if result.Columns == nil {
		result.Columns = []string{}
	}
	if result.Rows == nil {
		result.Rows = [][]string{}
	}
	status := http.StatusOK
	if result.Error != "" {
		status = http.StatusBadRequest
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("encode API response failed: %v", err)
	}
}

func (app *logUI) handleRowCountAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result := app.queryRowCount(r)
	status := http.StatusOK
	if result.Error != "" {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

type loadJobController struct {
	envPath string
	mu      sync.Mutex
	jobs    map[string]*loadJob
}

type loadJob struct {
	kind          string
	status        string
	startedAt     time.Time
	finishedAt    time.Time
	cancel        context.CancelFunc
	stopRequested bool
	err           string
	params        loadJobParams
	stats         *ingestStats
}

type loadJobParams struct {
	RowsPerSecond int   `json:"rows_per_second"`
	TotalRows     int64 `json:"total_rows,omitempty"`
}

type loadJobSnapshot struct {
	Kind                  string  `json:"kind"`
	Status                string  `json:"status"`
	RowsPerSecond         int     `json:"rows_per_second"`
	TotalRows             int64   `json:"total_rows,omitempty"`
	RowsLoaded            int64   `json:"rows_loaded"`
	RowsProduced          int64   `json:"rows_produced"`
	RowsInFlight          int64   `json:"rows_in_flight"`
	BytesLoaded           int64   `json:"bytes_loaded"`
	StreamLoads           int64   `json:"stream_loads"`
	StreamLoadSuccesses   int64   `json:"stream_load_successes"`
	StreamLoadErrors      int64   `json:"stream_load_errors"`
	AverageLoadTimeMS     float64 `json:"average_load_time_ms"`
	Handles               int64   `json:"handles"`
	Succeeded             int64   `json:"succeeded"`
	Failed                int64   `json:"failed"`
	Attempts              int64   `json:"attempts"`
	ElapsedSeconds        int64   `json:"elapsed_seconds"`
	ObservedRowsPerSecond float64 `json:"observed_rows_per_second"`
	Progress              float64 `json:"progress"`
	Error                 string  `json:"error,omitempty"`
}

func newLoadJobController(envPath string) *loadJobController {
	return &loadJobController{
		envPath: envPath,
		jobs:    map[string]*loadJob{},
	}
}

func (c *loadJobController) start(kind string, params loadJobParams) (loadJobSnapshot, error) {
	if kind != "backfill" {
		return loadJobSnapshot{}, fmt.Errorf("invalid load job kind %q", kind)
	}
	if params.RowsPerSecond <= 0 {
		return loadJobSnapshot{}, fmt.Errorf("rows_per_second must be greater than 0")
	}
	if params.TotalRows <= 0 {
		return loadJobSnapshot{}, fmt.Errorf("total_rows must be greater than 0")
	}

	c.mu.Lock()
	for existingKind, existing := range c.jobs {
		if existing.status == "running" || existing.status == "stopping" {
			snapshot := existing.snapshotLocked()
			c.mu.Unlock()
			return loadJobSnapshot{}, fmt.Errorf("%s load job is already %s; only one loader can run at a time", existingKind, snapshot.Status)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &loadJob{
		kind:      kind,
		status:    "running",
		startedAt: time.Now(),
		cancel:    cancel,
		params:    params,
		stats:     &ingestStats{},
	}
	c.jobs[kind] = job
	snapshot := job.snapshotLocked()
	c.mu.Unlock()

	opts := defaultLoaderOptions()
	opts.envPath = c.envPath
	opts.rowsPerSecond = params.RowsPerSecond
	opts.totalRows = params.TotalRows
	opts.stats = job.stats
	go func() {
		err := runLoaderWithContext(ctx, opts)
		c.mu.Lock()
		defer c.mu.Unlock()
		job.finishedAt = time.Now()
		if job.stopRequested || err == context.Canceled {
			job.status = "stopped"
			return
		}
		if err != nil {
			job.status = "failed"
			job.err = err.Error()
			return
		}
		job.status = "completed"
	}()

	return snapshot, nil
}

func (c *loadJobController) stop(kind string) (loadJobSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	job := c.jobs[kind]
	if job == nil {
		return loadJobSnapshot{Kind: kind, Status: "idle"}, nil
	}
	if job.status != "running" && job.status != "stopping" {
		return job.snapshotLocked(), nil
	}
	job.status = "stopping"
	job.stopRequested = true
	job.cancel()
	return job.snapshotLocked(), nil
}

func (c *loadJobController) snapshots() map[string]loadJobSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := map[string]loadJobSnapshot{
		"backfill": {Kind: "backfill", Status: "idle"},
	}
	for kind, job := range c.jobs {
		out[kind] = job.snapshotLocked()
	}
	return out
}

func (j *loadJob) snapshotLocked() loadJobSnapshot {
	elapsed := time.Since(j.startedAt)
	if !j.finishedAt.IsZero() {
		elapsed = j.finishedAt.Sub(j.startedAt)
	}
	elapsedSeconds := int64(elapsed.Seconds())
	newlyLoaded := j.stats.rows.Load()
	rows := j.stats.initialRows.Load() + newlyLoaded
	produced := j.stats.produced.Load()
	observed := 0.0
	if elapsed.Seconds() > 0 {
		observed = float64(newlyLoaded) / elapsed.Seconds()
	}
	progress := 0.0
	if j.params.TotalRows > 0 {
		progress = float64(rows) / float64(j.params.TotalRows)
		if progress > 1 {
			progress = 1
		}
	}
	var streamLoads, streamLoadErrors int64
	var averageLoadTimeMS float64
	if j.stats.clientStats != nil {
		clientStats := j.stats.clientStats()
		streamLoads = clientStats.TotalLoadJobs
		streamLoadErrors = clientStats.ErrorJobs
		averageLoadTimeMS = float64(clientStats.AverageLoadTime) / float64(time.Millisecond)
	}

	return loadJobSnapshot{
		Kind:                  j.kind,
		Status:                j.status,
		RowsPerSecond:         j.params.RowsPerSecond,
		TotalRows:             j.params.TotalRows,
		RowsLoaded:            rows,
		RowsProduced:          produced,
		RowsInFlight:          produced - newlyLoaded,
		BytesLoaded:           j.stats.bytes.Load(),
		StreamLoads:           streamLoads,
		StreamLoadSuccesses:   streamLoads - streamLoadErrors,
		StreamLoadErrors:      streamLoadErrors,
		AverageLoadTimeMS:     averageLoadTimeMS,
		Handles:               j.stats.handles.Load(),
		Succeeded:             j.stats.succeeded.Load(),
		Failed:                j.stats.failed.Load(),
		Attempts:              j.stats.attempts.Load(),
		ElapsedSeconds:        elapsedSeconds,
		ObservedRowsPerSecond: observed,
		Progress:              progress,
		Error:                 j.err,
	}
}

func (app *logUI) handleLoadGenStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, app.loadJobs.snapshots())
}

func (app *logUI) handleLoadGenStartAPI(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var params loadJobParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		snapshot, err := app.loadJobs.start(kind, params)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "job": snapshot})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func (app *logUI) handleLoadGenStopAPI(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snapshot, err := app.loadJobs.stop(kind)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode JSON response failed: %v", err)
	}
}

func (app *logUI) queryRowCount(r *http.Request) rowCountResult {
	startedAt := time.Now()
	var rows int64
	if err := app.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+app.table).Scan(&rows); err != nil {
		return rowCountResult{Error: err.Error()}
	}
	return rowCountResult{
		Rows:      rows,
		ElapsedMS: time.Since(startedAt).Milliseconds(),
	}
}

func parseFilterForm(r *http.Request) filterForm {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 || limit > 1000 {
		limit = 100
	}
	return filterForm{
		TimeStart:     strings.TrimSpace(q.Get("time_start")),
		TimeEnd:       strings.TrimSpace(q.Get("time_end")),
		HostOperator:  strings.TrimSpace(q.Get("host_operator")),
		HostValue:     strings.TrimSpace(q.Get("host_value")),
		CountryCode:   strings.TrimSpace(q.Get("country_code")),
		City:          strings.TrimSpace(q.Get("city")),
		MessagePhrase: strings.TrimSpace(q.Get("message_phrase")),
		Severity:      strings.TrimSpace(q.Get("severity")),
		Limit:         limit,
	}
}

func (app *logUI) queryLogs(r *http.Request, form filterForm) queryResult {
	startedAt := time.Now()
	where, args, err := buildWhere(form)
	if err != nil {
		return queryResult{Error: err.Error()}
	}

	query := "SELECT event_time, host_id, payload FROM " + app.table
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += fmt.Sprintf(" LIMIT %d", form.Limit)
	effectiveQuery := renderEffectiveSQL(query, args)

	rows, err := app.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		return queryResult{SQL: effectiveQuery, Error: err.Error()}
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return queryResult{SQL: effectiveQuery, Error: err.Error()}
	}

	result := queryResult{Columns: columns, Rows: [][]string{}, SQL: effectiveQuery}
	for rows.Next() {
		raw := make([]sql.NullString, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return queryResult{SQL: effectiveQuery, Error: err.Error()}
		}
		row := make([]string, len(columns))
		for i, value := range raw {
			if value.Valid {
				row[i] = value.String
			}
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return queryResult{SQL: effectiveQuery, Error: err.Error()}
	}

	result.ElapsedMS = time.Since(startedAt).Milliseconds()
	return result
}

func buildWhere(form filterForm) ([]string, []any, error) {
	var where []string
	var args []any

	add := func(condition string, arg any) {
		where = append(where, condition)
		args = append(args, arg)
	}
	addString := func(value, condition string) {
		value = strings.TrimSpace(value)
		if value != "" {
			add(condition, value)
		}
	}

	if form.TimeStart != "" {
		value, err := parseDatetimeLocal(form.TimeStart)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid start time: %w", err)
		}
		add("event_time >= ?", value)
	}
	if form.TimeEnd != "" {
		value, err := parseDatetimeLocal(form.TimeEnd)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid end time: %w", err)
		}
		add("event_time <= ?", value)
	}

	if value := strings.TrimSpace(form.HostValue); value != "" {
		switch form.HostOperator {
		case "", "is":
			add("host_id = ?", value)
		case "is_not":
			add("host_id <> ?", value)
		case "contains":
			add("host_id LIKE ?", "%"+escapeLike(value)+"%")
		default:
			return nil, nil, fmt.Errorf("invalid host operator %q", form.HostOperator)
		}
	}
	addString(form.CountryCode, "payload['country_code'] = ?")
	addString(form.City, "payload['city'] = ?")
	addString(form.Severity, "payload['severity'] = ?")
	if value := strings.TrimSpace(form.MessagePhrase); value != "" {
		where = append(where, "payload['message'] MATCH_PHRASE "+sqlStringLiteral(value))
	}

	return where, args, nil
}

func parseDatetimeLocal(value string) (string, error) {
	parsed, err := time.Parse("2006-01-02T15:04", value)
	if err != nil {
		return "", err
	}
	return parsed.Format("2006-01-02 15:04:05"), nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func renderEffectiveSQL(query string, args []any) string {
	out := query
	for _, arg := range args {
		out = strings.Replace(out, "?", sqlValueLiteral(arg), 1)
	}
	return out
}

func sqlValueLiteral(value any) string {
	switch v := value.(type) {
	case string:
		return sqlStringLiteral(v)
	case fmt.Stringer:
		return sqlStringLiteral(v.String())
	default:
		return fmt.Sprint(v)
	}
}

var logTemplate = template.Must(template.New("logs").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Doris Logs</title>
  <style>
    :root { color-scheme: light; --ink:#101828; --muted:#667085; --line:#d7dde8; --soft:#f3f6fa; --panel:#ffffff; --accent:#13a3a3; --accent2:#7c3aed; --danger:#d92d20; --ok:#079455; --navy:#111827; }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: var(--ink); background: linear-gradient(180deg, #111827 0, #111827 126px, #f6f8fb 126px); }
    header { min-height: 76px; padding: 18px 24px; display: flex; align-items: center; justify-content: space-between; gap: 16px; color: #fff; }
    h1 { margin: 0; font-size: 22px; font-weight: 780; letter-spacing: 0; }
    .target { color: #b8c2d6; font-size: 13px; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
    .header-actions { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; justify-content: flex-end; }
    .header-link { min-height: 34px; display: inline-flex; align-items: center; color: #fff; text-decoration: none; border: 1px solid rgba(255,255,255,.28); border-radius: 6px; padding: 6px 10px; font-size: 13px; font-weight: 760; background: rgba(255,255,255,.10); }
    main { padding: 0 24px 30px; }
    .surface { background: rgba(255,255,255,.98); border: 1px solid rgba(255,255,255,.28); border-radius: 8px; box-shadow: 0 18px 44px rgba(16, 24, 40, .18); }
    form { display: grid; grid-template-columns: repeat(8, minmax(112px, 1fr)); gap: 12px; padding: 16px; align-items: end; }
    label { display: grid; gap: 6px; font-size: 11px; font-weight: 780; color: #475467; text-transform: uppercase; }
    input, select { width: 100%; min-height: 38px; border: 1px solid #cbd5e1; border-radius: 6px; padding: 8px 10px; font: inherit; font-size: 14px; background: #fff; color: var(--ink); outline: none; }
    input:focus, select:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(19, 163, 163, .16); }
    button { min-height: 38px; border: 0; border-radius: 6px; padding: 8px 13px; font: inherit; font-weight: 760; cursor: pointer; transition: transform .12s ease, box-shadow .12s ease, background .12s ease; }
    button:hover { transform: translateY(-1px); }
    .primary { color: #fff; background: linear-gradient(135deg, var(--accent), #2563eb); box-shadow: 0 8px 20px rgba(37, 99, 235, .22); }
    .ghost { color: #344054; background: #f8fafc; border: 1px solid #d7dde8; }
    .ghost.active { color: #fff; border-color: #1f2937; background: #1f2937; }
    .range-bar { grid-column: 1 / -1; display: flex; flex-wrap: wrap; gap: 8px; align-items: center; padding-bottom: 2px; }
    .range-bar button { min-height: 32px; padding: 6px 10px; font-size: 13px; }
    .row-count { color: #475467; font-size: 13px; font-weight: 700; min-height: 32px; display: inline-flex; align-items: center; }
    .row-count.error { color: var(--danger); }
    .span2 { grid-column: span 2; }
    .span3 { grid-column: span 3; }
    .actions { display: flex; gap: 8px; align-items: center; }
    .status { margin: 16px 0 10px; color: #536079; font-size: 13px; min-height: 18px; font-weight: 650; }
    .status.error { color: var(--danger); font-weight: 760; }
    .status.ok { color: var(--ok); }
    .sql { margin: 0 0 12px; padding: 11px 12px; background: #0b1220; color: #d6e0ef; border: 1px solid #1e293b; border-radius: 8px; overflow: auto; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; box-shadow: inset 0 1px 0 rgba(255,255,255,.04); }
    .table-wrap { overflow: auto; border: 1px solid var(--line); border-radius: 8px; background: #fff; box-shadow: 0 8px 24px rgba(16, 24, 40, .07); }
    table { width: 100%; border-collapse: collapse; min-width: 900px; }
    th, td { padding: 9px 10px; border-bottom: 1px solid #eaecf0; text-align: left; vertical-align: top; font-size: 13px; }
    th { position: sticky; top: 0; background: #eef3f8; font-size: 12px; color: #344054; }
    tbody tr:hover { background: #f8fbff; }
    td { max-width: 620px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    .empty { padding: 26px; color: var(--muted); text-align: center; }
    @media (max-width: 1100px) { form { grid-template-columns: repeat(4, minmax(0, 1fr)); } .span3 { grid-column: span 2; } }
    @media (max-width: 640px) { main, header { padding-left: 14px; padding-right: 14px; } form { grid-template-columns: 1fr; } .span2, .span3 { grid-column: span 1; } header { display:block; } }
  </style>
</head>
<body>
  <header>
    <h1>Doris Logs</h1>
    <div class="header-actions">
      <div class="target">{{.Target}} / {{.EnvPath}}</div>
      <a class="header-link" href="/logsearch/loadgen" target="_blank" rel="noreferrer">Load generation</a>
    </div>
  </header>
  <main>
    <section class="surface">
    <form id="filters">
      <div class="range-bar">
        <button class="ghost" type="button" data-range="1m">Last 1 minute</button>
        <button class="ghost" type="button" data-range="5m">Last 5 minutes</button>
        <button class="ghost" type="button" data-range="1h">Last hour</button>
        <button class="ghost" type="button" data-range="24h">Last 24 hours</button>
        <button class="ghost" type="button" data-range="3d">Last 3 days</button>
        <button class="ghost" type="button" data-range="7d">Last 7 days</button>
        <button class="ghost" type="button" data-range="30d">Last 30 days</button>
        <button class="ghost" type="button" id="refreshRows">Refresh rows</button>
        <span class="row-count" id="rowCount">Total rows: not loaded</span>
      </div>
      <label>Start time UTC<input type="datetime-local" name="time_start"></label>
      <label>End time UTC<input type="datetime-local" name="time_end"></label>
      <label>Host operator<select name="host_operator">
        <option value="is">is</option>
        <option value="is_not">is not</option>
        <option value="contains">contains</option>
      </select></label>
      <label class="span2">Host value<input name="host_value" placeholder="host086"></label>
      <label>Country code<input name="country_code" placeholder="US"></label>
      <label>City<input name="city" placeholder="New York"></label>
      <label>Severity<select name="severity">
        <option value="">Any</option>
        <option value="debug">debug</option>
        <option value="info">info</option>
        <option value="warn">warn</option>
        <option value="error">error</option>
        <option value="fatal">fatal</option>
      </select></label>
      <label class="span3">Message phrase<input name="message_phrase" placeholder="lorem dolore"></label>
      <label>Limit<input name="limit" value="100" inputmode="numeric"></label>
      <div class="actions"><button class="primary" type="submit">Filter</button><button class="ghost" type="button" id="reset">Reset</button></div>
    </form>
    </section>

    <div id="status" class="status">Choose filters and run a query.</div>
    <pre id="sql" class="sql" hidden></pre>
    <div id="results" class="table-wrap"><div class="empty">No results yet.</div></div>
  </main>
  <script>
    const form = document.querySelector("#filters");
    const statusEl = document.querySelector("#status");
    const sqlEl = document.querySelector("#sql");
    const resultsEl = document.querySelector("#results");
    const rowCountEl = document.querySelector("#rowCount");
    const refreshRowsButton = document.querySelector("#refreshRows");

    function utcInputValue(date) {
      const pad = (n) => String(n).padStart(2, "0");
      return date.getUTCFullYear() + "-" + pad(date.getUTCMonth() + 1) + "-" + pad(date.getUTCDate()) + "T" + pad(date.getUTCHours()) + ":" + pad(date.getUTCMinutes());
    }

    function setDateInputs(start, end) {
      form.elements.time_start.value = utcInputValue(start);
      form.elements.time_end.value = utcInputValue(end);
      form.elements.time_start.dispatchEvent(new Event("change", { bubbles: true }));
      form.elements.time_end.dispatchEvent(new Event("change", { bubbles: true }));
    }

    function setRange(range) {
      const end = new Date("{{.QueryAnchor}}");
      const start = new Date(end);
      if (range === "1m") start.setUTCMinutes(start.getUTCMinutes() - 1);
      if (range === "5m") start.setUTCMinutes(start.getUTCMinutes() - 5);
      if (range === "1h") start.setUTCHours(start.getUTCHours() - 1);
      if (range === "24h") start.setUTCHours(start.getUTCHours() - 24);
      if (range === "3d") start.setUTCDate(start.getUTCDate() - 3);
      if (range === "7d") start.setUTCDate(start.getUTCDate() - 7);
      if (range === "30d") start.setUTCDate(start.getUTCDate() - 30);
      setDateInputs(start, end);
      document.querySelectorAll("[data-range]").forEach(button => button.classList.toggle("active", button.dataset.range === range));
      loadLogs();
    }

    function sortRowsByEventTimeDesc(columns, rows) {
      const index = columns.indexOf("event_time");
      if (index < 0) return rows;
      return rows.slice().sort((a, b) => String(b[index] || "").localeCompare(String(a[index] || "")));
    }

    function tableHTML(columns, rows) {
      columns = Array.isArray(columns) ? columns : [];
      rows = Array.isArray(rows) ? rows : [];
      if (!rows || rows.length === 0) return '<div class="empty">No matching rows.</div>';
      const esc = (v) => String(v ?? "").replace(/[&<>"']/g, (c) => ({ "&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;" }[c]));
      const sortedRows = sortRowsByEventTimeDesc(columns, rows);
      return '<table><thead><tr>' + columns.map(c => '<th>' + esc(c) + '</th>').join("") + '</tr></thead><tbody>' + sortedRows.map(row => '<tr>' + row.map(cell => '<td title="' + esc(cell) + '">' + esc(cell) + '</td>').join("") + '</tr>').join("") + '</tbody></table>';
    }

    async function loadLogs() {
      const params = new URLSearchParams(new FormData(form));
      statusEl.className = "status";
      statusEl.textContent = "Loading...";
      sqlEl.hidden = true;
      try {
	        const response = await fetch('/logsearch/api/logs?' + params.toString(), { headers: { "Accept": "application/json" } });
	        const data = await response.json();
	        if (!response.ok || data.error) throw new Error(data.error || ('HTTP ' + response.status));
	        const rows = Array.isArray(data.rows) ? data.rows : [];
	        const columns = Array.isArray(data.columns) ? data.columns : [];
	        statusEl.className = "status ok";
		        statusEl.textContent = rows.length + ' row' + (rows.length === 1 ? '' : 's') + ' returned in ' + (data.elapsed_ms ?? 0) + ' ms';
        sqlEl.textContent = data.sql;
        sqlEl.hidden = false;
        resultsEl.innerHTML = tableHTML(columns, rows);
      } catch (err) {
        statusEl.className = "status error";
        statusEl.textContent = err.message;
	        resultsEl.innerHTML = '<div class="empty">Query failed.</div>';
      }
    }

    async function refreshRowCount() {
      refreshRowsButton.disabled = true;
      rowCountEl.className = "row-count";
      rowCountEl.textContent = "Total rows: loading...";
      try {
        const response = await fetch('/logsearch/api/row-count', { headers: { "Accept": "application/json" } });
        const data = await response.json();
        if (!response.ok || data.error) throw new Error(data.error || ('HTTP ' + response.status));
        rowCountEl.textContent = 'Total rows: ' + new Intl.NumberFormat().format(data.rows || 0) + ' (' + (data.elapsed_ms ?? 0) + ' ms)';
      } catch (err) {
        rowCountEl.className = "row-count error";
        rowCountEl.textContent = "Total rows: " + err.message;
      } finally {
        refreshRowsButton.disabled = false;
      }
    }

    document.querySelectorAll("[data-range]").forEach(button => {
      button.addEventListener("click", () => setRange(button.dataset.range));
    });
    refreshRowsButton.addEventListener("click", refreshRowCount);
    document.querySelector("#reset").addEventListener("click", () => {
      form.reset();
      document.querySelectorAll("[data-range]").forEach(button => button.classList.remove("active"));
      sqlEl.hidden = true;
      statusEl.className = "status";
      statusEl.textContent = "Choose filters and run a query.";
      resultsEl.innerHTML = '<div class="empty">No results yet.</div>';
    });
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      loadLogs();
    });
  </script>
</body>
</html>`))

var loadGenTemplate = template.Must(template.New("loadgen").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Doris Load Generation</title>
  <style>
    :root { color-scheme: light; --ink:#101828; --muted:#667085; --line:#d7dde8; --soft:#f3f6fa; --panel:#ffffff; --accent:#13a3a3; --blue:#2563eb; --danger:#d92d20; --ok:#079455; --navy:#111827; }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: var(--ink); background: linear-gradient(180deg, #111827 0, #111827 126px, #f6f8fb 126px); }
    header { min-height: 76px; padding: 18px 24px; display: flex; align-items: center; justify-content: space-between; gap: 16px; color: #fff; }
    h1 { margin: 0; font-size: 22px; font-weight: 780; letter-spacing: 0; }
    h2 { margin: 0; font-size: 17px; letter-spacing: 0; }
    .target { color: #b8c2d6; font-size: 13px; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; text-align: right; }
    main { padding: 0 24px 30px; display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; }
    section { background: rgba(255,255,255,.98); border: 1px solid rgba(255,255,255,.28); border-radius: 8px; box-shadow: 0 18px 44px rgba(16, 24, 40, .18); padding: 16px; min-width: 0; }
    form { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-top: 14px; align-items: end; }
    label { display: grid; gap: 6px; font-size: 11px; font-weight: 780; color: #475467; text-transform: uppercase; }
    input { width: 100%; min-height: 38px; border: 1px solid #cbd5e1; border-radius: 6px; padding: 8px 10px; font: inherit; font-size: 14px; background: #fff; color: var(--ink); outline: none; }
    input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(19, 163, 163, .16); }
    button { min-height: 38px; border: 0; border-radius: 6px; padding: 8px 13px; font: inherit; font-weight: 760; cursor: pointer; transition: transform .12s ease, box-shadow .12s ease, background .12s ease; }
    button:hover:not(:disabled) { transform: translateY(-1px); }
    button:disabled { cursor: not-allowed; opacity: .55; }
    .primary { color: #fff; background: linear-gradient(135deg, var(--accent), var(--blue)); box-shadow: 0 8px 20px rgba(37, 99, 235, .22); }
    .danger { color: #fff; background: var(--danger); box-shadow: 0 8px 20px rgba(217, 45, 32, .16); }
    .job-head { display: flex; justify-content: space-between; gap: 12px; align-items: start; }
    .badge { display: inline-flex; align-items: center; min-height: 26px; padding: 4px 8px; border-radius: 6px; background: #eef3f8; color: #344054; font-size: 12px; font-weight: 780; text-transform: capitalize; }
    .badge.running { color: #075985; background: #e0f2fe; }
    .badge.completed { color: var(--ok); background: #dcfae6; }
    .badge.failed, .badge.stopped { color: var(--danger); background: #fee4e2; }
    .metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; margin-top: 16px; }
    .metric { border: 1px solid var(--line); border-radius: 8px; padding: 10px; background: #fff; min-width: 0; }
    .metric span { display: block; color: var(--muted); font-size: 11px; font-weight: 780; text-transform: uppercase; }
    .metric strong { display: block; margin-top: 4px; font-size: 19px; letter-spacing: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    .progress { height: 9px; margin-top: 14px; border-radius: 999px; background: #e5e7eb; overflow: hidden; }
    .bar { width: 0%; height: 100%; background: linear-gradient(90deg, var(--accent), var(--blue)); transition: width .2s ease; }
    .message { min-height: 18px; margin-top: 12px; color: var(--muted); font-size: 13px; font-weight: 650; }
    .message.error { color: var(--danger); }
    .actions { display: flex; gap: 8px; align-items: end; }
    @media (max-width: 980px) { main { grid-template-columns: 1fr; } }
    @media (max-width: 640px) { main, header { padding-left: 14px; padding-right: 14px; } header { display: block; } .target { text-align: left; margin-top: 6px; } form, .metrics { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <header>
    <h1>Doris Load Generation</h1>
    <div class="target">{{.Target}} / {{.EnvPath}}</div>
  </header>
  <main>
    <section data-job="backfill">
      <div class="job-head">
        <h2>Back fill</h2>
        <span class="badge" data-field="status">Idle</span>
      </div>
      <form data-start="backfill">
        <label>Event time year<input type="text" value="2026 (fixed)" readonly></label>
        <label>Total rows<input name="total_rows" type="number" min="1" step="1" value="1000000000"></label>
        <label>Rows per second<input name="rows_per_second" type="number" min="1" step="1" value="1000"></label>
        <div class="actions">
          <button class="primary" type="submit" data-action="start">Start</button>
          <button class="danger" type="button" data-stop="backfill">Stop</button>
        </div>
      </form>
      <div class="progress"><div class="bar" data-field="bar"></div></div>
      <div class="metrics">
        <div class="metric"><span>Rows loaded</span><strong data-field="rows_loaded">0</strong></div>
        <div class="metric"><span>Progress</span><strong data-field="progress">0%</strong></div>
        <div class="metric"><span>Observed rows/s</span><strong data-field="observed_rows_per_second">0</strong></div>
        <div class="metric"><span>Outstanding rows</span><strong data-field="rows_in_flight">0</strong></div>
      </div>
      <div class="metrics">
        <div class="metric"><span>Stream Loads completed</span><strong data-field="stream_loads">0</strong></div>
        <div class="metric"><span>Load successes</span><strong data-field="stream_load_successes">0</strong></div>
        <div class="metric"><span>Load errors</span><strong data-field="stream_load_errors">0</strong></div>
        <div class="metric"><span>Average load time</span><strong data-field="average_load_time">0 ms</strong></div>
      </div>
      <div class="metrics">
        <div class="metric"><span>Rows produced</span><strong data-field="rows_produced">0</strong></div>
        <div class="metric"><span>Elapsed</span><strong data-field="elapsed_seconds">0s</strong></div>
        <div class="metric"><span>Bytes loaded</span><strong data-field="bytes_loaded">0</strong></div>
        <div class="metric"><span>Estimated time remaining</span><strong data-field="eta">—</strong></div>
      </div>
      <div class="message" data-field="message">Ready.</div>
    </section>
  </main>
  <script>
    const sections = Object.fromEntries(Array.from(document.querySelectorAll("[data-job]")).map(el => [el.dataset.job, el]));
    const fmt = new Intl.NumberFormat();

    function numberValue(form, name) {
      return Number(new FormData(form).get(name));
    }

    function formatSeconds(value) {
      value = Number(value || 0);
      const minutes = Math.floor(value / 60);
      const seconds = value % 60;
      return minutes > 0 ? minutes + "m " + seconds + "s" : seconds + "s";
    }

    function estimateRemaining(job) {
      const remaining = Math.max(0, Number(job.total_rows || 0) - Number(job.rows_loaded || 0));
      if (remaining === 0 && job.status === "completed") return "0s";
      if (job.status !== "running" && job.status !== "stopping") return "—";
      const speed = Number(job.observed_rows_per_second || 0);
      if (remaining === 0) return "0s";
      if (speed <= 0) return "Estimating…";
      return formatSeconds(Math.ceil(remaining / speed));
    }

    function setText(section, field, value) {
      const el = section.querySelector('[data-field="' + field + '"]');
      if (el) el.textContent = value;
    }

    function renderJob(kind, job) {
      const section = sections[kind];
      if (!section) return;
      job = job || { kind, status: "idle" };
      const status = job.status || "idle";
      const running = status === "running" || status === "stopping";
      const badge = section.querySelector('[data-field="status"]');
      badge.textContent = status;
      badge.className = "badge " + status;
      section.querySelector('[data-action="start"]').disabled = running;
      section.querySelector('[data-stop]').disabled = !running;
      setText(section, "rows_loaded", fmt.format(job.rows_loaded || 0));
      setText(section, "bytes_loaded", fmt.format(job.bytes_loaded || 0));
      setText(section, "succeeded", fmt.format(job.succeeded || 0));
      setText(section, "rows_produced", fmt.format(job.rows_produced || 0));
      setText(section, "rows_in_flight", fmt.format(job.rows_in_flight || 0));
      setText(section, "stream_loads", fmt.format(job.stream_loads || 0));
      setText(section, "stream_load_successes", fmt.format(job.stream_load_successes || 0));
      setText(section, "stream_load_errors", fmt.format(job.stream_load_errors || 0));
      setText(section, "average_load_time", fmt.format(Math.round((job.average_load_time_ms || 0) * 10) / 10) + " ms");
      setText(section, "elapsed_seconds", formatSeconds(job.elapsed_seconds));
      setText(section, "observed_rows_per_second", fmt.format(Math.round(job.observed_rows_per_second || 0)));
      setText(section, "eta", estimateRemaining(job));
      const progress = Math.round((job.progress || 0) * 1000) / 10;
      setText(section, "progress", progress + "%");
      const bar = section.querySelector('[data-field="bar"]');
      if (bar) bar.style.width = Math.max(0, Math.min(100, progress)) + "%";
      const message = section.querySelector('[data-field="message"]');
      message.className = "message" + (job.error ? " error" : "");
      if (job.error) {
        message.textContent = job.error;
      } else if (status === "running") {
        message.textContent = "Running at target " + fmt.format(job.rows_per_second || 0) + " rows/s.";
      } else if (status === "stopping") {
        message.textContent = "Stopping after in-flight batches finish.";
      } else if (status === "completed") {
        message.textContent = "Completed.";
      } else if (status === "stopped") {
        message.textContent = "Stopped.";
      } else {
        message.textContent = "Ready.";
      }
    }

    async function refresh() {
      const response = await fetch("/logsearch/api/loadgen/status", { headers: { "Accept": "application/json" } });
      const data = await response.json();
      renderJob("backfill", data.backfill);
    }

    async function postJSON(url, body) {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json", "Accept": "application/json" },
        body: JSON.stringify(body || {})
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "Request failed");
      return data;
    }

    document.querySelectorAll("[data-start]").forEach(form => {
      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        const kind = form.dataset.start;
        const payload = { rows_per_second: numberValue(form, "rows_per_second") };
        if (kind === "backfill") {
          payload.total_rows = numberValue(form, "total_rows");
        }
        try {
          await postJSON("/logsearch/api/loadgen/" + kind + "/start", payload);
          await refresh();
        } catch (err) {
          const message = sections[kind].querySelector('[data-field="message"]');
          message.className = "message error";
          message.textContent = err.message;
        }
      });
    });

    document.querySelectorAll("[data-stop]").forEach(button => {
      button.addEventListener("click", async () => {
        const kind = button.dataset.stop;
        try {
          await postJSON("/logsearch/api/loadgen/" + kind + "/stop", {});
          await refresh();
        } catch (err) {
          const message = sections[kind].querySelector('[data-field="message"]');
          message.className = "message error";
          message.textContent = err.message;
        }
      });
    });

    refresh();
    setInterval(refresh, 1000);
  </script>
</body>
</html>`))
