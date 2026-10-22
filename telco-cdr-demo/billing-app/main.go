// telco-billing: a CDR dedup -> bill run -> bill dashboard pipeline that runs entirely inside Doris / VeloDB.
//
//	VELODB_HOST, VELODB_MYSQL_PORT, VELODB_USER, VELODB_PASSWORD   cluster (MySQL protocol)
//	VELODB_DATABASE   database holding call_log, data_stream_log, rate_plan (default telco_demo)
//	LISTEN     http listen address (default :8088)
//	PARALLEL   concurrent INSERT statements per step (default 2)
package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

//go:embed web
var webFS embed.FS

type App struct {
	db       *sql.DB
	dbName   string
	host     string
	parallel int
	runner   *Runner

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	at  time.Time
	val any
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	cfg := mysql.NewConfig()
	cfg.User = env("VELODB_USER", "root")
	cfg.Passwd = os.Getenv("VELODB_PASSWORD")
	cfg.Net = "tcp"
	host := env("VELODB_HOST", "127.0.0.1")
	cfg.Addr = host + ":" + env("VELODB_MYSQL_PORT", "9030")
	cfg.DBName = env("VELODB_DATABASE", "telco_demo")
	cfg.InterpolateParams = true
	cfg.Timeout = 10 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	parallel, _ := strconv.Atoi(env("PARALLEL", "2"))
	app := &App{db: db, dbName: cfg.DBName, host: host, parallel: max(parallel, 1), cache: map[string]cacheEntry{}}
	app.runner = &Runner{app: app}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := app.setup(ctx); err != nil {
		log.Fatalf("connecting to %s / %s: %v", cfg.Addr, cfg.DBName, err)
	}
	cancel()

	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServerFS(static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache") // embedded files carry no mod time; always revalidate
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /api/overview", app.handleOverview)
	mux.HandleFunc("POST /api/steps/{step}", app.handleStartStep)
	mux.HandleFunc("GET /api/job", app.handleJob)
	mux.HandleFunc("POST /api/job/cancel", app.handleCancel)
	mux.HandleFunc("GET /api/runs", app.handleRuns)
	mux.HandleFunc("GET /api/bills", app.handleBillSearch)
	mux.HandleFunc("GET /api/bills/summary", app.handleBillSummary)
	mux.HandleFunc("GET /api/bills/{month}/{msisdn}", app.handleBillDetail)
	mux.HandleFunc("GET /api/bills/{month}/{msisdn}/pdf", app.handleBillPDF)

	addr := env("LISTEN", ":8088")
	log.Printf("telco-billing on http://localhost%s  (doris %s, database %s, parallel %d)", addr, cfg.Addr, cfg.DBName, app.parallel)
	log.Fatal(http.ListenAndServe(addr, logRequests(mux)))
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		h.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/job" {
			log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(t0).Round(time.Millisecond))
		}
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (a *App) invalidateCache() {
	a.cacheMu.Lock()
	a.cache = map[string]cacheEntry{}
	a.cacheMu.Unlock()
}

func (a *App) cached(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	a.cacheMu.Lock()
	if e, ok := a.cache[key]; ok && time.Since(e.at) < ttl {
		a.cacheMu.Unlock()
		return e.val, nil
	}
	a.cacheMu.Unlock()
	v, err := fn()
	if err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	a.cache[key] = cacheEntry{time.Now(), v}
	a.cacheMu.Unlock()
	return v, nil
}

// ---------------------------------------------------------------------------------------------
// overview: months available and row counts per pipeline stage
// ---------------------------------------------------------------------------------------------

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	refresh := r.URL.Query().Get("refresh") == "1"
	if refresh {
		a.invalidateCache()
	}
	ctx := r.Context()
	monthsAny, err := a.cached("months", 5*time.Minute, func() (any, error) {
		days, err := a.partitionDays(ctx, "call_log", "")
		if err != nil {
			return nil, err
		}
		counts := map[string]int{}
		order := []string{}
		for _, d := range days {
			m := d[:7]
			if counts[m] == 0 {
				order = append(order, m)
			}
			counts[m]++
		}
		// partitions exist before (and without) data, so only offer months that actually hold CDRs;
		// LIMIT 1 with a pruned partition range is a cheap probe
		has := make([]bool, len(order))
		var wg sync.WaitGroup
		for i, m := range order {
			wg.Add(1)
			go func() {
				defer wg.Done()
				m0, m1 := monthBounds(m)
				n, err := queryInt(ctx, a.db, fmt.Sprintf("SELECT COUNT(*) FROM (SELECT 1 FROM call_log WHERE start_time >= '%s' AND start_time < '%s' LIMIT 1) t", m0, m1))
				has[i] = err == nil && n > 0
			}()
		}
		wg.Wait()
		out := []map[string]any{}
		for i, m := range order {
			if has[i] {
				out = append(out, map[string]any{"month": m, "days": counts[m]})
			}
		}
		return out, nil
	})
	if err != nil {
		writeErr(w, 500, fmt.Errorf("reading call_log partitions (is %s loaded?): %w", a.dbName, err))
		return
	}
	months := monthsAny.([]map[string]any)
	if month == "" && len(months) > 0 {
		month = months[len(months)-1]["month"].(string)
	}
	resp := map[string]any{"database": a.dbName, "host": a.host, "parallel": a.parallel, "months": months, "month": month}
	if monthRe.MatchString(month) {
		stats, err := a.cached("stats:"+month, 2*time.Minute, func() (any, error) {
			m0, m1 := monthBounds(month)
			win := fmt.Sprintf("start_time >= '%s' AND start_time < '%s'", m0, m1)
			queries := map[string]string{
				"call_log": "SELECT COUNT(*) FROM call_log WHERE " + win,
				"dedup": fmt.Sprintf("SELECT (SELECT COUNT(*) FROM call_log WHERE %[1]s) - "+
					"(SELECT COALESCE(SUM(extra_copies), 0) FROM cdr_duplicate_correction WHERE %[1]s)", win),
				"data_stream_log": "SELECT COUNT(*) FROM data_stream_log WHERE " + win,
				"bills":           fmt.Sprintf("SELECT COUNT(*) FROM subscriber_bill WHERE bill_month = '%s'", m0),
				"rate_plans":      "SELECT COUNT(*) FROM rate_plan",
			}
			var mu sync.Mutex
			var wg sync.WaitGroup
			out := map[string]any{}
			for k, q := range queries {
				wg.Add(1)
				go func() {
					defer wg.Done()
					t0 := time.Now()
					n, err := queryInt(ctx, a.db, q)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						out[k] = map[string]any{"error": err.Error()}
						return
					}
					out[k] = map[string]any{"rows": n, "ms": time.Since(t0).Milliseconds()}
				}()
			}
			wg.Wait()
			return out, nil
		})
		if err == nil {
			resp["stats"] = stats
		}
	}
	writeJSON(w, 200, resp)
}

// ---------------------------------------------------------------------------------------------
// pipeline control
// ---------------------------------------------------------------------------------------------

func (a *App) handleStartStep(w http.ResponseWriter, r *http.Request) {
	var p StepParams
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, 400, err)
		return
	}
	if !monthRe.MatchString(p.Month) {
		writeErr(w, 400, fmt.Errorf("month must be YYYY-MM"))
		return
	}
	j, err := a.runner.Start(r.PathValue("step"), p)
	if err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 202, j.Snapshot())
}

func (a *App) handleJob(w http.ResponseWriter, r *http.Request) {
	j := a.runner.Get()
	if j == nil {
		writeJSON(w, 200, map[string]any{"status": "idle"})
		return
	}
	writeJSON(w, 200, j.Snapshot())
}

func (a *App) handleCancel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"cancelled": a.runner.Cancel()})
}

func (a *App) handleRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := queryMaps(r.Context(), a.db, `SELECT run_id, step, month, status, started_at, finished_at,
  TIMESTAMPDIFF(SECOND, started_at, COALESCE(finished_at, now())) secs, tasks_total, tasks_done, rows_in, rows_out, message
FROM pipeline_run ORDER BY started_at DESC LIMIT 15`)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ---------------------------------------------------------------------------------------------
// bills
// ---------------------------------------------------------------------------------------------

var planRe = regexp.MustCompile(`^[A-Z0-9_]{1,16}$`)

func (a *App) handleBillSearch(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	month := qs.Get("month")
	if !monthRe.MatchString(month) {
		writeErr(w, 400, fmt.Errorf("month must be YYYY-MM"))
		return
	}
	m0, _ := monthBounds(month)
	where := []string{"bill_month = ?"}
	args := []any{m0}
	if digits := strings.Map(func(c rune) rune {
		if c >= '0' && c <= '9' {
			return c
		}
		return -1
	}, qs.Get("q")); digits != "" {
		if !strings.HasPrefix(digits, "65") {
			digits = "65" + digits // local numbers are stored with the country code
		}
		if len(digits) == 10 {
			n, _ := strconv.ParseInt(digits, 10, 64)
			where, args = append(where, "msisdn = ?"), append(args, n)
		} else {
			where, args = append(where, "CAST(msisdn AS STRING) LIKE ?"), append(args, digits+"%")
		}
	}
	if p := qs.Get("plan"); planRe.MatchString(p) {
		where, args = append(where, "rate_plan = ?"), append(args, p)
	}
	if t := qs.Get("type"); t == "PREPAID" || t == "POSTPAID" {
		where, args = append(where, "subscriber_type = ?"), append(args, t)
	}
	if qs.Get("roaming") == "1" {
		where = append(where, "roaming")
	}
	if v, err := strconv.ParseFloat(qs.Get("min_total"), 64); err == nil && v > 0 {
		where, args = append(where, "total >= ?"), append(args, v)
	}
	sorts := map[string]string{
		"total_desc": "total DESC", "total_asc": "total ASC", "usage_desc": "usage_charges DESC",
		"data_desc": "data_total_mb DESC", "roaming_desc": "roam_voice_charge + roam_sms_charge + roam_data_charge DESC",
		"msisdn": "msisdn ASC",
	}
	order, ok := sorts[qs.Get("sort")]
	if !ok {
		order = sorts["total_desc"]
	}
	page, _ := strconv.Atoi(qs.Get("page"))
	page = max(page, 1)
	const size = 25
	cond := strings.Join(where, " AND ")

	ctx := r.Context()
	t0 := time.Now()
	var total int64
	var countErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		total, countErr = queryInt(ctx, a.db, "SELECT COUNT(*) FROM subscriber_bill WHERE "+cond, args...)
	}()
	rows, err := queryMaps(ctx, a.db, fmt.Sprintf(`SELECT bill_no, msisdn, subscriber_type, rate_plan, monthly_fee, dom_voice_min, sms_sent,
  data_total_mb, usage_charges, total, roaming
FROM subscriber_bill WHERE %s ORDER BY %s LIMIT %d OFFSET %d`, cond, order, size, (page-1)*size), args...)
	wg.Wait()
	if err == nil {
		err = countErr
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"rows": rows, "total": total, "page": page, "size": size, "ms": time.Since(t0).Milliseconds()})
}

func (a *App) handleBillSummary(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if !monthRe.MatchString(month) {
		writeErr(w, 400, fmt.Errorf("month must be YYYY-MM"))
		return
	}
	m0, _ := monthBounds(month)
	v, err := a.cached("summary:"+month, 2*time.Minute, func() (any, error) {
		t0 := time.Now()
		queries := map[string]string{
			"kpi": `SELECT COUNT(*) bills, SUM(total) revenue, AVG(total) arpu, SUM(monthly_fee) recurring, SUM(tax) tax,
  SUM(voice_overage_charge + sms_charge + data_overage_charge) overage,
  SUM(intl_charge + premium_charge) intl_premium,
  SUM(roam_voice_charge + roam_sms_charge + roam_data_charge) roaming,
  SUM(IF(roaming, 1, 0)) roaming_bills, SUM(IF(usage_charges > 0, 1, 0)) bills_with_usage,
  SUM(data_total_mb) / 1024 data_gb, SUM(dom_voice_min + intl_min + roam_voice_min) voice_min,
  MAX(generated_at) generated_at, ANY_VALUE(bill_run_id) bill_run_id
FROM subscriber_bill WHERE bill_month = ?`,
			"plans": `SELECT rate_plan, ANY_VALUE(subscriber_type) subscriber_type, COUNT(*) bills, SUM(monthly_fee) recurring,
  SUM(voice_overage_charge + intl_charge + premium_charge) voice, SUM(sms_charge) sms, SUM(data_overage_charge) data,
  SUM(roam_voice_charge + roam_sms_charge + roam_data_charge) roaming, SUM(tax) tax, SUM(total) revenue, AVG(total) arpu
FROM subscriber_bill WHERE bill_month = ? GROUP BY rate_plan ORDER BY revenue DESC`,
			"histogram": `SELECT LEAST(FLOOR(total / 10), 30) * 10 bucket, COUNT(*) bills
FROM subscriber_bill WHERE bill_month = ? GROUP BY 1 ORDER BY 1`,
		}
		out := map[string]any{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		var firstErr error
		for k, q := range queries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rows, err := queryMaps(r.Context(), a.db, q, m0)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					firstErr = err
					return
				}
				if k == "kpi" {
					out[k] = rows[0]
				} else {
					out[k] = rows
				}
			}()
		}
		wg.Wait()
		out["ms"] = time.Since(t0).Milliseconds()
		return out, firstErr
	})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, v)
}

type BillDetail struct {
	Bill     map[string]any   `json:"bill"`
	Plan     map[string]any   `json:"plan"`
	Items    []map[string]any `json:"items"`
	Daily    []map[string]any `json:"daily"`
	Apps     []map[string]any `json:"apps"`
	Ms       int64            `json:"ms"`
	Database string           `json:"database"`
}

func (a *App) billDetail(ctx context.Context, month string, msisdn int64) (*BillDetail, error) {
	m0, m1 := monthBounds(month)
	win := fmt.Sprintf("msisdn = %d AND start_time >= '%s' AND start_time < '%s'", msisdn, m0, m1)
	t0 := time.Now()
	queries := []string{
		fmt.Sprintf("SELECT * FROM subscriber_bill WHERE bill_month = '%s' AND msisdn = %d", m0, msisdn),
		fmt.Sprintf("SELECT p.* FROM rate_plan p JOIN (SELECT rate_plan FROM subscriber_bill WHERE bill_month = '%s' AND msisdn = %d) b ON p.rate_plan = b.rate_plan", m0, msisdn),
		// itemised per-record charges (international, premium, roaming); domestic usage is billed in aggregate
		fmt.Sprintf(`SELECT c.start_time, c.other_party_number, c.call_type, c.direction, c.other_party_type, c.dest_country_code,
  c.duration_sec, c.roaming, c.visited_plmn,
  IF(c.call_type = 'SMS', 1, CEIL(c.duration_sec / 60)) units,
  ROUND(CASE WHEN c.call_type = 'SMS' THEN p.sms_each * 3
             WHEN c.roaming THEN CEIL(c.duration_sec / 60) * p.roaming_voice_per_min
             WHEN c.other_party_type = 'INTERNATIONAL' THEN CEIL(c.duration_sec / 60) * p.intl_per_min
             ELSE CEIL(c.duration_sec / 60) * p.premium_per_min END, 2) charge
FROM (SELECT msisdn, start_time, call_id, ANY_VALUE(other_party_number) other_party_number, ANY_VALUE(call_type) call_type,
        ANY_VALUE(direction) direction, ANY_VALUE(other_party_type) other_party_type, ANY_VALUE(dest_country_code) dest_country_code,
        ANY_VALUE(duration_sec) duration_sec, ANY_VALUE(roaming) roaming, ANY_VALUE(visited_plmn) visited_plmn,
        ANY_VALUE(call_result) call_result, ANY_VALUE(rate_plan) rate_plan
      FROM call_log WHERE %s GROUP BY msisdn, start_time, call_id) c
JOIN rate_plan p ON c.rate_plan = p.rate_plan
WHERE TRUE
  AND ((c.call_type <> 'SMS' AND c.duration_sec > 0
        AND (c.roaming OR (c.direction IN ('MO', 'FWD') AND c.other_party_type IN ('INTERNATIONAL', 'PREMIUM'))))
    OR (c.call_type = 'SMS' AND c.direction = 'MO' AND c.call_result = 'DELIVERED' AND c.roaming))
ORDER BY c.start_time LIMIT 300`, win),
		fmt.Sprintf(`SELECT COALESCE(v.d, x.d) day, COALESCE(v.minutes, 0) minutes, COALESCE(v.calls, 0) calls, COALESCE(v.sms, 0) sms, COALESCE(x.mb, 0) mb
FROM (SELECT DATE(start_time) d, ROUND(SUM(sign * IF(call_type <> 'SMS', duration_sec, 0)) / 60, 1) minutes,
             SUM(sign * IF(call_type <> 'SMS', 1, 0)) calls, SUM(sign * IF(call_type = 'SMS', 1, 0)) sms
      FROM (SELECT 1 sign, start_time, call_type, duration_sec FROM call_log WHERE %[1]s
            UNION ALL SELECT -extra_copies, start_time, call_type, duration_sec FROM cdr_duplicate_correction WHERE %[1]s) c
      GROUP BY 1) v
FULL OUTER JOIN (SELECT DATE(start_time) d, ROUND(SUM(uplink_bytes + downlink_bytes) / 1048576, 1) mb
      FROM data_stream_log WHERE %[1]s GROUP BY 1) x ON v.d = x.d
ORDER BY 1`, win),
		fmt.Sprintf(`SELECT app_category, ROUND(SUM(uplink_bytes + downlink_bytes) / 1048576, 1) mb, COUNT(*) sessions,
  ANY_VALUE(rating_group) rating_group
FROM data_stream_log WHERE %s GROUP BY 1 ORDER BY 2 DESC`, win),
	}
	results := make([][]map[string]any, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = queryMaps(ctx, a.db, q)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	if len(results[0]) == 0 {
		return nil, nil
	}
	d := &BillDetail{Bill: results[0][0], Items: results[2], Daily: results[3], Apps: results[4],
		Ms: time.Since(t0).Milliseconds(), Database: a.dbName}
	if len(results[1]) > 0 {
		d.Plan = results[1][0]
	}
	return d, nil
}

func parseBillPath(r *http.Request) (string, int64, error) {
	month := r.PathValue("month")
	msisdn, err := strconv.ParseInt(r.PathValue("msisdn"), 10, 64)
	if !monthRe.MatchString(month) || err != nil {
		return "", 0, fmt.Errorf("bad month or msisdn")
	}
	return month, msisdn, nil
}

func (a *App) handleBillDetail(w http.ResponseWriter, r *http.Request) {
	month, msisdn, err := parseBillPath(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	d, err := a.billDetail(r.Context(), month, msisdn)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if d == nil {
		writeErr(w, 404, fmt.Errorf("no bill for %d in %s", msisdn, month))
		return
	}
	writeJSON(w, 200, d)
}

func (a *App) handleBillPDF(w http.ResponseWriter, r *http.Request) {
	month, msisdn, err := parseBillPath(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	d, err := a.billDetail(r.Context(), month, msisdn)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if d == nil {
		writeErr(w, 404, fmt.Errorf("no bill for %d in %s", msisdn, month))
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="bill-%s-%d.pdf"`, month, msisdn))
	if err := renderBillPDF(w, month, d); err != nil {
		log.Printf("pdf %s %d: %v", month, msisdn, err)
	}
}
