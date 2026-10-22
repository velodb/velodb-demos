package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// callCols is the column list of call_log and of cdr_duplicate_correction's copy of it (same order as schema.sql).
const callCols = "msisdn, start_time, call_id, end_time, imsi, other_party_number, call_type, direction, other_party_type, " +
	"dest_country_code, call_result, release_cause, ring_duration_ms, duration_sec, rat, cell_id, tac, msc_id, " +
	"roaming, visited_plmn, subscriber_type, rate_plan"

// setupDDL is what the app owns. Source tables (call_log, data_stream_log, rate_plan) come from schema.sql / run-load.sh.
// Partitions are fixed ranges copied from call_log's day partitions at startup (no auto or dynamic partitioning, so
// nothing is ever dropped); the __FIRST_DAY__ .. __END_MONTH__ placeholders are filled in by setup.
var setupDDL = []string{
	`CREATE TABLE IF NOT EXISTS cdr_duplicate_correction (
  msisdn             BIGINT       NOT NULL,
  start_time         DATETIME(3)  NOT NULL,
  call_id            VARCHAR(20)  NOT NULL,
  extra_copies       INT          NOT NULL COMMENT 'how many times this CDR was delivered again (copies - 1)',
  end_time           DATETIME(3)  NOT NULL,
  imsi               VARCHAR(15)  NOT NULL,
  other_party_number VARCHAR(20)  NOT NULL,
  call_type          VARCHAR(8)   NOT NULL,
  direction          VARCHAR(4)   NOT NULL,
  other_party_type   VARCHAR(16)  NOT NULL,
  dest_country_code  SMALLINT     NOT NULL,
  call_result        VARCHAR(10)  NOT NULL,
  release_cause      SMALLINT     NOT NULL,
  ring_duration_ms   INT          NOT NULL,
  duration_sec       INT          NOT NULL,
  rat                VARCHAR(5)   NOT NULL,
  cell_id            INT          NOT NULL,
  tac                INT          NOT NULL,
  msc_id             VARCHAR(8)   NOT NULL,
  roaming            BOOLEAN      NOT NULL,
  visited_plmn       VARCHAR(6)   NOT NULL,
  subscriber_type    VARCHAR(8)   NOT NULL,
  rate_plan          VARCHAR(16)  NOT NULL
)
DUPLICATE KEY(msisdn, start_time, call_id)
COMMENT 'one row per re-delivered copy of a CDR; billing counts call_log once and subtracts these'
PARTITION BY RANGE (start_time) (FROM ("__FIRST_DAY__") TO ("__END_DAY__") INTERVAL 1 DAY)
DISTRIBUTED BY HASH(msisdn) BUCKETS 8
PROPERTIES ("replication_num" = "1", "compression" = "LZ4")`,

	`CREATE TABLE IF NOT EXISTS subscriber_bill (
  bill_month           DATE          NOT NULL,
  msisdn               BIGINT        NOT NULL,
  bill_no              VARCHAR(24)   NOT NULL,
  subscriber_type      VARCHAR(8)    NOT NULL,
  rate_plan            VARCHAR(16)   NOT NULL,
  monthly_fee          DECIMAL(12,2) NOT NULL,
  voice_calls          BIGINT        NOT NULL,
  dom_voice_min        BIGINT        NOT NULL,
  incl_voice_min       INT           NOT NULL,
  voice_overage_min    BIGINT        NOT NULL,
  voice_overage_charge DECIMAL(12,2) NOT NULL,
  intl_min             BIGINT        NOT NULL,
  intl_charge          DECIMAL(12,2) NOT NULL,
  premium_min          BIGINT        NOT NULL,
  premium_charge       DECIMAL(12,2) NOT NULL,
  roam_voice_min       BIGINT        NOT NULL,
  roam_voice_charge    DECIMAL(12,2) NOT NULL,
  sms_sent             BIGINT        NOT NULL,
  incl_sms             INT           NOT NULL,
  sms_overage          BIGINT        NOT NULL,
  sms_charge           DECIMAL(12,2) NOT NULL,
  roam_sms             BIGINT        NOT NULL,
  roam_sms_charge      DECIMAL(12,2) NOT NULL,
  data_sessions        BIGINT        NOT NULL,
  data_total_mb        DECIMAL(16,2) NOT NULL,
  zero_rated_mb        DECIMAL(16,2) NOT NULL,
  chargeable_mb        DECIMAL(16,2) NOT NULL,
  incl_data_mb         BIGINT        NOT NULL,
  data_overage_mb      BIGINT        NOT NULL,
  data_overage_charge  DECIMAL(12,2) NOT NULL,
  roam_data_mb         DECIMAL(16,2) NOT NULL,
  roam_data_charge     DECIMAL(12,2) NOT NULL,
  usage_charges        DECIMAL(12,2) NOT NULL COMMENT 'everything on top of the monthly fee',
  subtotal             DECIMAL(12,2) NOT NULL,
  tax                  DECIMAL(12,2) NOT NULL COMMENT 'GST 9%',
  total                DECIMAL(12,2) NOT NULL,
  roaming              BOOLEAN       NOT NULL,
  bill_run_id          VARCHAR(40)   NOT NULL,
  generated_at         DATETIME      NOT NULL
)
UNIQUE KEY(bill_month, msisdn)
COMMENT 'one rated bill per subscriber per month'
PARTITION BY RANGE (bill_month) (FROM ("__FIRST_MONTH__") TO ("__END_MONTH__") INTERVAL 1 MONTH)
DISTRIBUTED BY HASH(msisdn) BUCKETS 8
PROPERTIES ("replication_num" = "1", "enable_unique_key_merge_on_write" = "true")`,

	`CREATE TABLE IF NOT EXISTS pipeline_run (
  run_id      VARCHAR(40)  NOT NULL,
  step        VARCHAR(24)  NOT NULL,
  month       VARCHAR(7)   NOT NULL,
  status      VARCHAR(12)  NOT NULL,
  started_at  DATETIME     NOT NULL,
  finished_at DATETIME     NULL,
  tasks_total INT          NOT NULL,
  tasks_done  INT          NOT NULL,
  rows_in     BIGINT       NOT NULL,
  rows_out    BIGINT       NOT NULL,
  message     STRING       NULL
)
UNIQUE KEY(run_id)
DISTRIBUTED BY HASH(run_id) BUCKETS 1
PROPERTIES ("replication_num" = "1")`,
}

// roamingCaps backfills rate_plan.roaming_data_cap on databases loaded before the column existed.
var roamingCaps = map[string]string{
	"PRE_BASIC": "100", "PRE_DATA_PACK": "100", "PRE_TOURIST": "150",
	"POST_LITE_20": "200", "POST_PLUS_40": "300", "POST_MAX_80": "500", "POST_BIZ_60": "500",
}

func (a *App) setup(ctx context.Context) error {
	days, err := a.partitionDays(ctx, "call_log", "")
	if err != nil {
		return fmt.Errorf("reading call_log partitions (load the data with run-load.sh first): %w", err)
	}
	if len(days) == 0 {
		return fmt.Errorf("call_log has no partitions; load the data with run-load.sh first")
	}
	first, _ := time.Parse("2006-01-02", days[0])
	last, _ := time.Parse("2006-01-02", days[len(days)-1])
	firstMonth := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	endMonth := time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	fill := strings.NewReplacer(
		"__FIRST_DAY__", first.Format("2006-01-02"), "__END_DAY__", last.AddDate(0, 0, 1).Format("2006-01-02"),
		"__FIRST_MONTH__", firstMonth.Format("2006-01-02"), "__END_MONTH__", endMonth.Format("2006-01-02"))
	// earlier versions materialised a whole deduplicated copy of call_log; corrections replace it (run step 1 again)
	var tbl, create string
	if err := a.db.QueryRowContext(ctx, "SHOW CREATE TABLE cdr_call_log_deduplicate").Scan(&tbl, &create); err == nil {
		log.Printf("dropping cdr_call_log_deduplicate: duplicates are now stored as correction rows; re-run step 1")
		if _, err := a.db.ExecContext(ctx, "DROP TABLE cdr_call_log_deduplicate"); err != nil {
			return fmt.Errorf("dropping old cdr_call_log_deduplicate: %w", err)
		}
	}
	for _, tmpl := range setupDDL {
		ddl := fill.Replace(tmpl)
		if _, err := a.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("setup: %w\n%s", err, ddl)
		}
	}
	// runs still marked running belong to an app instance that stopped mid-step
	if _, err := a.db.ExecContext(ctx, `INSERT INTO pipeline_run
SELECT run_id, step, month, 'abandoned', started_at, started_at, tasks_total, tasks_done, rows_in, rows_out, 'app stopped while this step was running'
FROM pipeline_run WHERE status = 'running'`); err != nil {
		log.Printf("marking abandoned runs: %v", err)
	}
	n, err := queryInt(ctx, a.db, `SELECT COUNT(*) FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'rate_plan' AND column_name = 'roaming_data_cap'`)
	if err != nil || n > 0 {
		return err
	}
	stmts := []string{`ALTER TABLE rate_plan ADD COLUMN roaming_data_cap DECIMAL(10,2) NOT NULL DEFAULT "200"
  COMMENT 'bill-shock cap on roaming data charges per bill cycle'`}
	var cases strings.Builder
	for plan, cap := range roamingCaps {
		fmt.Fprintf(&cases, " WHEN '%s' THEN %s", plan, cap)
	}
	stmts = append(stmts, "UPDATE rate_plan SET roaming_data_cap = CASE rate_plan"+cases.String()+" ELSE roaming_data_cap END")
	for i, s := range stmts {
		for attempt := 0; ; attempt++ { // the column add is a (light) schema change; the update waits for it
			_, err = a.db.ExecContext(ctx, s)
			if err == nil || i == 0 || attempt >= 30 {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			return fmt.Errorf("adding rate_plan.roaming_data_cap: %w", err)
		}
	}
	return nil
}

// queryMaps runs a query and returns rows as JSON-friendly maps (numbers typed from the column metadata).
func queryMaps(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c.Name()] = convert(vals[i], c.DatabaseTypeName())
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func convert(v any, typ string) any {
	var s string
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		s = string(x)
	case string:
		s = x
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	default:
		return x
	}
	switch typ {
	case "TINYINT", "SMALLINT", "INT", "BIGINT", "MEDIUMINT":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	case "DECIMAL", "DOUBLE", "FLOAT":
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}

func queryInt(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any) (int64, error) {
	var n sql.NullInt64
	err := q.QueryRowContext(ctx, query, args...).Scan(&n)
	return n.Int64, err
}

var rangeKeyRe = regexp.MustCompile(`keys: \[(\d{4}-\d{2}-\d{2})`)

// partitionDays lists the day partitions of an auto-partitioned table that fall into month (YYYY-MM).
func (a *App) partitionDays(ctx context.Context, table, month string) ([]string, error) {
	rows, err := a.db.QueryContext(ctx, "SHOW PARTITIONS FROM "+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	rangeIdx := -1
	for i, c := range cols {
		if c == "Range" {
			rangeIdx = i
		}
	}
	if rangeIdx < 0 {
		return nil, fmt.Errorf("SHOW PARTITIONS FROM %s: no Range column", table)
	}
	seen := map[string]bool{}
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		if m := rangeKeyRe.FindStringSubmatch(string(vals[rangeIdx])); m != nil {
			if month == "" || strings.HasPrefix(m[1], month) {
				seen[m[1]] = true
			}
		}
	}
	days := make([]string, 0, len(seen))
	for d := range seen {
		days = append(days, d)
	}
	sort.Strings(days)
	return days, rows.Err()
}

var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// monthBounds returns 'YYYY-MM-01' and the first day of the next month.
func monthBounds(month string) (string, string) {
	t, _ := time.Parse("2006-01", month)
	return t.Format("2006-01-02"), t.AddDate(0, 1, 0).Format("2006-01-02")
}

func nextDay(day string) string {
	t, _ := time.Parse("2006-01-02", day)
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}
