package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------------------------
// Job state (what the UI polls)
// ---------------------------------------------------------------------------------------------

type LogEntry struct {
	T     string `json:"t"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	SQL   string `json:"sql,omitempty"`
	Ms    int64  `json:"ms,omitempty"`
	Rows  int64  `json:"rows,omitempty"`
}

type TaskState struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pending | running | done | failed | skipped
	Ms     int64  `json:"ms"`
	In     int64  `json:"in"`
	Out    int64  `json:"out"`
}

type Job struct {
	mu       sync.Mutex
	ID       string
	Step     string
	Month    string
	Status   string // running | succeeded | failed | cancelled
	Error    string
	Started  time.Time
	Finished time.Time
	Tasks    []TaskState
	Logs     []LogEntry
	RowsIn   int64
	RowsOut  int64
	Summary  map[string]any

	cancelled atomic.Bool
	connIDs   sync.Map // Doris connection id -> struct{}, for KILL QUERY on cancel
}

func (j *Job) log(level, msg, sqlText string, ms, rows int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Logs = append(j.Logs, LogEntry{T: time.Now().Format("15:04:05"), Level: level, Msg: msg, SQL: sqlText, Ms: ms, Rows: rows})
	if len(j.Logs) > 400 {
		j.Logs = j.Logs[len(j.Logs)-400:]
	}
}

func (j *Job) setTasks(names []string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Tasks = make([]TaskState, len(names))
	for i, n := range names {
		j.Tasks[i] = TaskState{Name: n, Status: "pending"}
	}
}

func (j *Job) updateTask(i int, f func(t *TaskState)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f(&j.Tasks[i])
}

// Snapshot is the JSON view of a job.
func (j *Job) Snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	done, running := 0, []string{}
	var doneMs int64
	for _, t := range j.Tasks {
		switch t.Status {
		case "done", "failed", "skipped":
			done++
			doneMs += t.Ms
		case "running":
			running = append(running, t.Name)
		}
	}
	end := time.Now()
	if !j.Finished.IsZero() {
		end = j.Finished
	}
	elapsed := end.Sub(j.Started).Seconds()
	eta := -1.0
	if j.Status == "running" && done > 0 && done < len(j.Tasks) {
		eta = elapsed / float64(done) * float64(len(j.Tasks)-done)
	}
	return map[string]any{
		"id": j.ID, "step": j.Step, "month": j.Month, "status": j.Status, "error": j.Error,
		"started": j.Started.Format("2006-01-02 15:04:05"), "elapsed_sec": math.Round(elapsed*10) / 10, "eta_sec": math.Round(eta),
		"total": len(j.Tasks), "done": done, "running": running,
		"rows_in": j.RowsIn, "rows_out": j.RowsOut,
		"tasks": append([]TaskState(nil), j.Tasks...), "logs": append([]LogEntry(nil), j.Logs...), "summary": j.Summary,
	}
}

// exec runs one statement on the job connection and logs it with its timing and affected rows.
func (j *Job) exec(ctx context.Context, conn *sql.Conn, msg, stmt string) (int64, error) {
	t0 := time.Now()
	res, err := conn.ExecContext(ctx, stmt)
	ms := time.Since(t0).Milliseconds()
	if err != nil {
		j.log(j.errLevel(), msg+": "+err.Error(), stmt, ms, 0)
		return 0, err
	}
	n, _ := res.RowsAffected()
	j.log("sql", msg, stmt, ms, n)
	return n, nil
}

func (j *Job) queryInt(ctx context.Context, conn *sql.Conn, msg, stmt string) (int64, error) {
	t0 := time.Now()
	n, err := queryInt(ctx, conn, stmt)
	ms := time.Since(t0).Milliseconds()
	if err != nil {
		j.log(j.errLevel(), msg+": "+err.Error(), stmt, ms, 0)
		return 0, err
	}
	j.log("sql", msg, stmt, ms, n)
	return n, nil
}

// errLevel: statements killed by a cancel are expected, not errors.
func (j *Job) errLevel() string {
	if j.cancelled.Load() {
		return "warn"
	}
	return "error"
}

// ---------------------------------------------------------------------------------------------
// Runner: one job at a time
// ---------------------------------------------------------------------------------------------

type StepParams struct {
	Month   string  `json:"month"`
	DupRate float64 `json:"dup_rate"` // percent, inject step
}

type Runner struct {
	app     *App
	mu      sync.Mutex
	current *Job
	last    *Job
}

var steps = map[string]func(*App, context.Context, *Job, StepParams) error{
	"inject": (*App).stepInjectDuplicates,
	"dedup":  (*App).stepDedup,
	"bill":   (*App).stepBill,
}

func (r *Runner) Start(step string, p StepParams) (*Job, error) {
	fn, ok := steps[step]
	if !ok {
		return nil, fmt.Errorf("unknown step %q", step)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		return nil, fmt.Errorf("%s is still running", r.current.Step)
	}
	j := &Job{ID: fmt.Sprintf("%s-%s-%s", step, strings.ReplaceAll(p.Month, "-", ""), time.Now().Format("20060102-150405")),
		Step: step, Month: p.Month, Status: "running", Started: time.Now()}
	r.current = j
	go r.run(j, fn, p)
	return j, nil
}

func (r *Runner) run(j *Job, fn func(*App, context.Context, *Job, StepParams) error, p StepParams) {
	ctx := context.Background()
	r.app.persistRun(j)
	err := func() (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("panic: %v", rec)
			}
		}()
		return fn(r.app, ctx, j, p)
	}()
	j.mu.Lock()
	j.Finished = time.Now()
	switch {
	case j.cancelled.Load():
		j.Status = "cancelled"
	case err != nil:
		j.Status, j.Error = "failed", err.Error()
	default:
		j.Status = "succeeded"
	}
	status := j.Status
	j.mu.Unlock()
	j.log(map[bool]string{true: "info", false: "error"}[status == "succeeded"],
		fmt.Sprintf("%s %s after %.1fs", j.Step, status, j.Finished.Sub(j.Started).Seconds()), "", 0, 0)
	log.Printf("job %s %s %v", j.ID, status, err)
	r.app.invalidateCache()
	// free the slot as soon as the job reports a final status, so the next step can start right away
	r.mu.Lock()
	r.current, r.last = nil, j
	r.mu.Unlock()
	r.app.persistRun(j)
}

func (r *Runner) Get() *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		return r.current
	}
	return r.last
}

func (r *Runner) Cancel() bool {
	r.mu.Lock()
	j := r.current
	r.mu.Unlock()
	if j == nil {
		return false
	}
	j.cancelled.Store(true)
	j.log("warn", "cancel requested: killing running queries", "", 0, 0)
	j.connIDs.Range(func(k, _ any) bool {
		// "Unknown thread id" just means that connection had no statement running at the moment
		if _, err := r.app.db.Exec(fmt.Sprintf("KILL QUERY %d", k.(int64))); err != nil && !strings.Contains(err.Error(), "Unknown thread id") {
			j.log("warn", fmt.Sprintf("KILL QUERY %d: %v", k.(int64), err), "", 0, 0)
		}
		return true
	})
	return true
}

func (a *App) persistRun(j *Job) {
	j.mu.Lock()
	done := 0
	for _, t := range j.Tasks {
		if t.Status == "done" {
			done++
		}
	}
	finished := "NULL"
	if !j.Finished.IsZero() {
		finished = "'" + j.Finished.Format("2006-01-02 15:04:05") + "'"
	}
	msg := j.Error
	if msg == "" && j.Summary != nil {
		msg = fmt.Sprint(j.Summary["headline"])
	}
	stmt := fmt.Sprintf("INSERT INTO pipeline_run VALUES ('%s', '%s', '%s', '%s', '%s', %s, %d, %d, %d, %d, '%s')",
		j.ID, j.Step, j.Month, j.Status, j.Started.Format("2006-01-02 15:04:05"), finished,
		len(j.Tasks), done, j.RowsIn, j.RowsOut, strings.ReplaceAll(strings.ReplaceAll(msg, `\`, `\\`), "'", "''"))
	j.mu.Unlock()
	if _, err := a.db.Exec(stmt); err != nil {
		log.Printf("persist run %s: %v", j.ID, err)
	}
}

// forEachTask runs fn for task 0..n-1 on `parallel` dedicated connections.
func (a *App) forEachTask(ctx context.Context, j *Job, n int, fn func(ctx context.Context, conn *sql.Conn, i int) error) error {
	workers := a.parallel
	if workers > n {
		workers = n
	}
	conns := make([]*sql.Conn, 0, workers)
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for w := 0; w < workers; w++ {
		c, err := a.jobConn(ctx, j)
		if err != nil {
			return err
		}
		conns = append(conns, c)
	}
	idx := make(chan int)
	var failed atomic.Bool
	var firstErr error
	var once sync.Once
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func(conn *sql.Conn) {
			defer wg.Done()
			for i := range idx {
				if failed.Load() || j.cancelled.Load() {
					j.updateTask(i, func(t *TaskState) { t.Status = "skipped" })
					continue
				}
				j.updateTask(i, func(t *TaskState) { t.Status = "running" })
				t0 := time.Now()
				err := fn(ctx, conn, i)
				j.updateTask(i, func(t *TaskState) {
					t.Ms = time.Since(t0).Milliseconds()
					t.Status = "done"
					if err != nil {
						t.Status = "failed"
						if j.cancelled.Load() {
							t.Status = "skipped"
						}
					}
				})
				if err != nil && !j.cancelled.Load() {
					failed.Store(true)
					once.Do(func() { firstErr = err })
				}
			}
		}(c)
	}
	for i := 0; i < n; i++ {
		idx <- i
	}
	close(idx)
	wg.Wait()
	return firstErr
}

func (a *App) jobConn(ctx context.Context, j *Job) (*sql.Conn, error) {
	c, err := a.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range []string{"SET insert_timeout = 14400", "SET query_timeout = 14400"} {
		if _, err := c.ExecContext(ctx, s); err != nil {
			c.Close()
			return nil, err
		}
	}
	id, err := queryInt(ctx, c, "SELECT CONNECTION_ID()")
	if err == nil {
		j.connIDs.Store(id, struct{}{})
	}
	return c, nil
}

func dayWindow(day string) string {
	return fmt.Sprintf("start_time >= '%s 00:00:00' AND start_time < '%s 00:00:00'", day, nextDay(day))
}

// ---------------------------------------------------------------------------------------------
// Step 0 (optional): simulate mediation re-delivering CDR files -> exact duplicate rows in call_log
// ---------------------------------------------------------------------------------------------

func (a *App) stepInjectDuplicates(ctx context.Context, j *Job, p StepParams) error {
	rate := p.DupRate
	if rate <= 0 || rate > 20 {
		rate = 0.5
	}
	perTenK := int(math.Round(rate * 100))
	days, err := a.partitionDays(ctx, "call_log", p.Month)
	if err != nil {
		return err
	}
	if len(days) == 0 {
		return fmt.Errorf("call_log has no partitions in %s", p.Month)
	}
	j.setTasks(days)
	j.log("info", fmt.Sprintf("re-inserting %.2f%% of call_log rows (chosen by hash of call_id) for %d days, %d in parallel",
		rate, len(days), min(a.parallel, len(days))), "", 0, 0)
	err = a.forEachTask(ctx, j, len(days), func(ctx context.Context, conn *sql.Conn, i int) error {
		stmt := fmt.Sprintf("INSERT INTO call_log (%s)\nSELECT %s\nFROM call_log\nWHERE %s\n  AND abs(murmur_hash3_32(call_id)) %% 10000 < %d",
			callCols, callCols, dayWindow(days[i]), perTenK)
		n, err := j.exec(ctx, conn, "duplicate "+days[i], stmt)
		j.updateTask(i, func(t *TaskState) { t.Out = n })
		j.mu.Lock()
		j.RowsOut += n
		j.mu.Unlock()
		return err
	})
	j.mu.Lock()
	j.Summary = map[string]any{"headline": fmt.Sprintf("%s duplicate CDRs injected into call_log", grouped(float64(j.RowsOut))), "duplicates_injected": j.RowsOut}
	j.mu.Unlock()
	return err
}

// ---------------------------------------------------------------------------------------------
// Step 1: find re-delivered CDRs and write one correction row per extra copy, a day partition per task
// ---------------------------------------------------------------------------------------------

func (a *App) stepDedup(ctx context.Context, j *Job, p StepParams) error {
	days, err := a.partitionDays(ctx, "call_log", p.Month)
	if err != nil {
		return err
	}
	if len(days) == 0 {
		return fmt.Errorf("call_log has no partitions in %s", p.Month)
	}
	// every run starts from an empty month
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = partitionName(d)
	}
	conn, err := a.jobConn(ctx, j)
	if err != nil {
		return err
	}
	_, err = j.exec(ctx, conn, fmt.Sprintf("truncate %d day partitions of %s", len(days), p.Month),
		fmt.Sprintf("TRUNCATE TABLE cdr_duplicate_correction PARTITION (%s)", strings.Join(parts, ", ")))
	conn.Close()
	if err != nil {
		return err
	}
	j.setTasks(days)
	j.log("info", fmt.Sprintf("scanning %d days for re-delivered CDRs: GROUP BY (msisdn, start_time, call_id) HAVING COUNT(*) > 1, "+
		"one correction row per extra copy into cdr_duplicate_correction (0.5%% of the data, so the write is tiny), %d in parallel",
		len(days), min(a.parallel, len(days))), "", 0, 0)
	err = a.forEachTask(ctx, j, len(days), func(ctx context.Context, conn *sql.Conn, i int) error {
		d := days[i]
		in, err := j.queryInt(ctx, conn, "raw CDRs "+d, fmt.Sprintf("SELECT COUNT(*) FROM call_log WHERE %s", dayWindow(d)))
		if err != nil {
			return err
		}
		if _, err := j.exec(ctx, conn, "corrections "+d, fmt.Sprintf(
			"INSERT INTO cdr_duplicate_correction PARTITION (%s) (msisdn, start_time, call_id, extra_copies, %s)\n"+
				"SELECT msisdn, start_time, call_id, CAST(COUNT(*) - 1 AS INT), %s\nFROM call_log\nWHERE %s\n"+
				"GROUP BY msisdn, start_time, call_id\nHAVING COUNT(*) > 1",
			partitionName(d), otherCols, otherColsAgg, dayWindow(d))); err != nil {
			return err
		}
		dups, err := j.queryInt(ctx, conn, "duplicate copies "+d,
			fmt.Sprintf("SELECT COALESCE(SUM(extra_copies), 0) FROM cdr_duplicate_correction WHERE %s", dayWindow(d)))
		if err != nil {
			return err
		}
		out := in - dups
		j.updateTask(i, func(t *TaskState) { t.In, t.Out = in, out })
		j.mu.Lock()
		j.RowsIn += in
		j.RowsOut += out
		j.mu.Unlock()
		if dups > 0 {
			j.log("info", fmt.Sprintf("%s: %s CDRs delivered, %s unique, %s re-delivered copies corrected",
				d, grouped(float64(in)), grouped(float64(out)), grouped(float64(dups))), "", 0, 0)
		}
		return nil
	})
	j.mu.Lock()
	dups := j.RowsIn - j.RowsOut
	secs := time.Since(j.Started).Seconds()
	j.Summary = map[string]any{
		"headline": fmt.Sprintf("%s CDRs in, %s unique, %s duplicate copies corrected", grouped(float64(j.RowsIn)), grouped(float64(j.RowsOut)), grouped(float64(dups))),
		"rows_in":  j.RowsIn, "rows_unique": j.RowsOut, "duplicates": dups,
		"dup_pct":    pct(dups, j.RowsIn),
		"rows_per_s": math.Round(float64(j.RowsIn) / math.Max(secs, 0.001)),
	}
	j.mu.Unlock()
	return err
}

// otherCols / otherColsAgg: every call_log column except the (msisdn, start_time, call_id) identity, plain and
// ANY_VALUE-wrapped (re-delivered CDRs are exact copies, so any copy carries the right values).
var otherCols, otherColsAgg = func() (string, string) {
	keys := map[string]bool{"msisdn": true, "start_time": true, "call_id": true}
	var plain, wrapped []string
	for _, c := range strings.Split(callCols, ",") {
		if c = strings.TrimSpace(c); !keys[c] {
			plain = append(plain, c)
			wrapped = append(wrapped, fmt.Sprintf("ANY_VALUE(%s)", c))
		}
	}
	return strings.Join(plain, ", "), strings.Join(wrapped, ", ")
}()

// partitionName is the name Doris gives a fixed day partition created with FROM .. TO .. INTERVAL 1 DAY.
func partitionName(day string) string { return "p_" + strings.ReplaceAll(day, "-", "") }

func pct(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return math.Round(float64(a)/float64(b)*10000) / 100
}

// ---------------------------------------------------------------------------------------------
// Step 2: bill run for one month, one MSISDN slice per task
// ---------------------------------------------------------------------------------------------

const (
	msisdnBase  = 6580000000
	subscribers = 5000000
	billSlices  = 48
)

func (a *App) stepBill(ctx context.Context, j *Job, p StepParams) error {
	m0, m1 := monthBounds(p.Month)
	conn, err := a.jobConn(ctx, j)
	if err != nil {
		return err
	}
	n, err := j.queryInt(ctx, conn, "check step 1 ran for the month",
		fmt.Sprintf("SELECT COUNT(*) FROM pipeline_run WHERE step = 'dedup' AND month = '%s' AND status = 'succeeded'", p.Month))
	if err == nil && n == 0 {
		err = fmt.Errorf("step 1 has not completed for %s: run deduplication first", p.Month)
	}
	if err == nil {
		n, err = j.queryInt(ctx, conn, "CDRs to rate",
			fmt.Sprintf("SELECT COUNT(*) FROM call_log WHERE start_time >= '%s' AND start_time < '%s'", m0, m1))
	}
	if err == nil {
		_, err = j.exec(ctx, conn, "truncate the month's bills",
			fmt.Sprintf("TRUNCATE TABLE subscriber_bill PARTITION (p_%s)", strings.ReplaceAll(p.Month, "-", "")))
	}
	conn.Close()
	if err != nil {
		return err
	}

	// The loader maps a uniform draw u to MSISDN index 0.3u + 0.7u^2 (skewed to low numbers), so slicing on the same
	// curve gives every slice about the same number of records.
	bounds := make([]int64, billSlices+1)
	names := make([]string, billSlices)
	for i := 0; i <= billSlices; i++ {
		f := float64(i) / billSlices
		bounds[i] = msisdnBase + int64(math.Round(subscribers*(0.3*f+0.7*f*f)))
	}
	bounds[billSlices] = msisdnBase + 100000000 // anything above the base range lands in the last slice
	bounds[0] = 0
	for i := 0; i < billSlices; i++ {
		lo := max(bounds[i], msisdnBase)
		names[i] = fmt.Sprintf("%d…", lo)
	}
	j.setTasks(names)
	j.log("info", fmt.Sprintf("rating %d CDRs + data usage for %s in %d MSISDN slices, %d in parallel", n, p.Month, billSlices, min(a.parallel, billSlices)), "", 0, 0)

	err = a.forEachTask(ctx, j, billSlices, func(ctx context.Context, conn *sql.Conn, i int) error {
		out, err := j.exec(ctx, conn, "bill slice "+names[i], billSQL(p.Month, bounds[i], bounds[i+1], j.ID))
		j.updateTask(i, func(t *TaskState) { t.Out = out })
		j.mu.Lock()
		j.RowsOut += out
		j.mu.Unlock()
		return err
	})
	if err != nil || j.cancelled.Load() {
		return err
	}
	rows, err := queryMaps(ctx, a.db, fmt.Sprintf(`SELECT COUNT(*) bills, SUM(total) revenue, AVG(total) arpu, SUM(usage_charges) usage_charges,
  SUM(voice_overage_charge + sms_charge + data_overage_charge) overage, SUM(IF(roaming, 1, 0)) roaming_bills
FROM subscriber_bill WHERE bill_month = '%s'`, m0))
	if err != nil {
		return err
	}
	s := rows[0]
	s["headline"] = fmt.Sprintf("%s bills, revenue %s, ARPU %s", grouped(num(s, "bills")), strings.TrimSuffix(money(num(s, "revenue")), ".00"), money(num(s, "arpu")))
	j.mu.Lock()
	j.RowsIn = n
	j.Summary = s
	j.mu.Unlock()
	return nil
}

// billSQL rates one MSISDN slice [lo, hi) of a month. Rules:
//   - voice billed per started minute. Domestic MO/FWD (on-net + off-net) draws down the plan allowance, then voice_per_min.
//     International / premium MO are charged per minute; toll-free, emergency and non-roaming MT calls are free.
//   - roaming: every connected call (MO and MT) at roaming_voice_per_min, SMS at 3x sms_each, data at roaming_data_per_mb
//     up to the plan's roaming_data_cap (bill-shock limit).
//   - SMS: delivered MO SMS draw down the allowance, then sms_each.
//   - data: domestic bytes minus zero-rated (MMS/IMS always; video / social rating groups when the plan says so) draw
//     down included_data_mb, overage per started MB.
//   - GST 9% on monthly fee + usage.
func billSQL(month string, lo, hi int64, runID string) string {
	m0, m1 := monthBounds(month)
	win := fmt.Sprintf("start_time >= '%s 00:00:00' AND start_time < '%s 00:00:00' AND msisdn >= %d AND msisdn < %d", m0, m1, lo, hi)
	return fmt.Sprintf(`INSERT INTO subscriber_bill
WITH cdr AS (
  -- every CDR counted once: call_log as delivered, minus one row per re-delivered copy (step 1)
  SELECT 1 sign, msisdn, call_type, direction, other_party_type, call_result, roaming, duration_sec, rate_plan, subscriber_type
  FROM call_log WHERE %[1]s
  UNION ALL
  SELECT -extra_copies, msisdn, call_type, direction, other_party_type, call_result, roaming, duration_sec, rate_plan, subscriber_type
  FROM cdr_duplicate_correction WHERE %[1]s
),
v AS (
  SELECT msisdn, ANY_VALUE(rate_plan) rate_plan, ANY_VALUE(subscriber_type) subscriber_type,
    SUM(sign * IF(call_type <> 'SMS', 1, 0)) voice_calls,
    SUM(sign * IF(call_type <> 'SMS' AND NOT roaming AND direction IN ('MO', 'FWD') AND other_party_type IN ('ONNET', 'OFFNET'), CEIL(duration_sec / 60), 0)) dom_min,
    SUM(sign * IF(call_type <> 'SMS' AND NOT roaming AND direction IN ('MO', 'FWD') AND other_party_type = 'INTERNATIONAL', CEIL(duration_sec / 60), 0)) intl_min,
    SUM(sign * IF(call_type <> 'SMS' AND NOT roaming AND direction IN ('MO', 'FWD') AND other_party_type = 'PREMIUM', CEIL(duration_sec / 60), 0)) premium_min,
    SUM(sign * IF(call_type <> 'SMS' AND roaming, CEIL(duration_sec / 60), 0)) roam_min,
    SUM(sign * IF(call_type = 'SMS' AND direction = 'MO' AND call_result = 'DELIVERED' AND NOT roaming, 1, 0)) sms,
    SUM(sign * IF(call_type = 'SMS' AND direction = 'MO' AND call_result = 'DELIVERED' AND roaming, 1, 0)) roam_sms
  FROM cdr
  GROUP BY msisdn
),
d AS (
  SELECT msisdn, ANY_VALUE(rate_plan) rate_plan, ANY_VALUE(subscriber_type) subscriber_type, COUNT(*) sessions,
    SUM(uplink_bytes + downlink_bytes) total_b,
    SUM(IF(NOT roaming AND rating_group IN (50, 99), uplink_bytes + downlink_bytes, 0)) free_b,
    SUM(IF(NOT roaming AND rating_group = 20, uplink_bytes + downlink_bytes, 0)) video_b,
    SUM(IF(NOT roaming AND rating_group = 30, uplink_bytes + downlink_bytes, 0)) social_b,
    SUM(IF(roaming, uplink_bytes + downlink_bytes, 0)) roam_b
  FROM data_stream_log
  WHERE %[1]s
  GROUP BY msisdn
),
u AS (
  SELECT COALESCE(v.msisdn, d.msisdn) msisdn, COALESCE(v.rate_plan, d.rate_plan) rate_plan,
    COALESCE(v.subscriber_type, d.subscriber_type) subscriber_type,
    COALESCE(v.voice_calls, 0) voice_calls, COALESCE(v.dom_min, 0) dom_min, COALESCE(v.intl_min, 0) intl_min,
    COALESCE(v.premium_min, 0) premium_min, COALESCE(v.roam_min, 0) roam_min, COALESCE(v.sms, 0) sms, COALESCE(v.roam_sms, 0) roam_sms,
    COALESCE(d.sessions, 0) sessions, COALESCE(d.total_b, 0) total_b, COALESCE(d.free_b, 0) free_b,
    COALESCE(d.video_b, 0) video_b, COALESCE(d.social_b, 0) social_b, COALESCE(d.roam_b, 0) roam_b
  FROM v FULL OUTER JOIN d ON v.msisdn = d.msisdn
),
q AS (
  SELECT u.*, p.monthly_fee, p.included_voice_min, p.included_sms, p.included_data_mb,
    p.voice_per_min, p.intl_per_min, p.premium_per_min, p.roaming_voice_per_min, p.sms_each, p.data_per_mb, p.roaming_data_per_mb, p.roaming_data_cap,
    GREATEST(u.dom_min - p.included_voice_min, 0) voice_over_min,
    GREATEST(u.sms - p.included_sms, 0) sms_over,
    u.total_b / 1048576.0 total_mb,
    (u.free_b + IF(p.video_zero_rated, u.video_b, 0) + IF(p.social_zero_rated, u.social_b, 0)) / 1048576.0 zero_mb,
    (u.total_b - u.roam_b - u.free_b - IF(p.video_zero_rated, u.video_b, 0) - IF(p.social_zero_rated, u.social_b, 0)) / 1048576.0 charge_mb,
    u.roam_b / 1048576.0 roam_mb
  FROM u JOIN rate_plan p ON u.rate_plan = p.rate_plan
),
c AS (
  SELECT q.*,
    CAST(CEIL(GREATEST(charge_mb - included_data_mb, 0)) AS BIGINT) data_over_mb,
    ROUND(voice_over_min * voice_per_min, 2) voice_over_chg,
    ROUND(intl_min * intl_per_min, 2) intl_chg,
    ROUND(premium_min * premium_per_min, 2) premium_chg,
    ROUND(roam_min * roaming_voice_per_min, 2) roam_voice_chg,
    ROUND(sms_over * sms_each, 2) sms_chg,
    ROUND(roam_sms * sms_each * 3, 2) roam_sms_chg,
    ROUND(CEIL(GREATEST(charge_mb - included_data_mb, 0)) * data_per_mb, 2) data_over_chg,
    ROUND(LEAST(ROUND(roam_mb, 2) * roaming_data_per_mb, roaming_data_cap), 2) roam_data_chg
  FROM q
),
t AS (
  SELECT c.*, voice_over_chg + intl_chg + premium_chg + roam_voice_chg + sms_chg + roam_sms_chg + data_over_chg + roam_data_chg usage_chg
  FROM c
)
SELECT '%[2]s', msisdn, concat('B%[3]s-', CAST(msisdn AS STRING)), subscriber_type, rate_plan, monthly_fee,
  voice_calls, dom_min, included_voice_min, voice_over_min, voice_over_chg,
  intl_min, intl_chg, premium_min, premium_chg, roam_min, roam_voice_chg,
  sms, included_sms, sms_over, sms_chg, roam_sms, roam_sms_chg,
  sessions, ROUND(total_mb, 2), ROUND(zero_mb, 2), ROUND(charge_mb, 2), included_data_mb, data_over_mb, data_over_chg,
  ROUND(roam_mb, 2), roam_data_chg,
  usage_chg, monthly_fee + usage_chg, ROUND((monthly_fee + usage_chg) * 0.09, 2),
  monthly_fee + usage_chg + ROUND((monthly_fee + usage_chg) * 0.09, 2),
  roam_min > 0 OR roam_sms > 0 OR roam_b > 0, '%[4]s', '%[5]s'
FROM t`, win, m0, strings.ReplaceAll(month, "-", ""), runID, time.Now().Format("2006-01-02 15:04:05"))
}
