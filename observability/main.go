package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	dorisstreamload "github.com/wushilin/doris_go_stream_load"
)

const csvSeparator = "|"

var (
	eventYearStart      = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	eventYearEnd        = time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	eventTimeAnchorTime = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	queryTimeAnchorTime = time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
)

type Payload struct {
	City        string `json:"city"`
	CountryCode string `json:"country_code"`
	Message     string `json:"message"`
	Quantity    int    `json:"quantity"`
	Severity    string `json:"severity"`
	DueDate     int64  `json:"due_date"`
}

var cities = []struct {
	City    string
	Country string
}{
	{"Shanghai", "CN"},
	{"Singapore", "SG"},
	{"Mumbai", "IN"},
	{"Tokyo", "JP"},
	{"New York", "US"},
	{"London", "GB"},
	{"Berlin", "DE"},
	{"Paris", "FR"},
	{"Seoul", "KR"},
}

var severities = []string{"info", "warn", "debug", "error", "fatal"}

var loremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet",
	"consectetur", "adipiscing", "elit",
	"sed", "do", "eiusmod", "tempor",
	"incididunt", "ut", "labore", "et",
	"dolore", "magna", "aliqua",
	"enim", "ad", "minim", "veniam",
	"quis", "nostrud", "exercitation",
	"ullamco", "laboris", "nisi",
	"aliquip", "ex", "ea", "commodo",
	"consequat", "duis", "aute",
	"irure", "reprehenderit", "voluptate",
	"velit", "esse", "cillum", "eu",
	"fugiat", "nulla", "pariatur",
	"excepteur", "sint", "occaecat",
	"cupidatat", "non", "proident",
	"sunt", "culpa", "officia",
	"deserunt", "mollit", "anim",
	"laborum",
}

type appConfig struct {
	StreamLoadURL                  string
	User                           string
	Password                       string
	Database                       string
	Table                          string
	MySQLAddr                      string
	StreamLoadBatchBytes           int
	StreamLoadLinger               time.Duration
	StreamLoadCompression          string
	StreamLoadUploadWorkers        int
	GeneratorThreads               int
	StreamLoadUploadRequestTimeout time.Duration
	StreamLoadUploadTimeout        time.Duration
	StreamLoadMaxQueueSize         int
	StreamLoadMaxUploadQueueSize   int
	StreamLoadMaxQueueWait         time.Duration
	StreamLoadLabelPrefix          string
	StreamLoadGeneratorBatchRows   int
	RowsPerSecond                  int
	TotalRows                      int64
}

func (c appConfig) FullStreamLoadURL() string {
	if streamLoadURLHasPath(c.StreamLoadURL) {
		return c.StreamLoadURL
	}
	return ""
}

func (c appConfig) StreamLoadEndpoint() string {
	if streamLoadURLHasPath(c.StreamLoadURL) {
		return ""
	}
	return c.StreamLoadURL
}

func streamLoadURLHasPath(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.Contains(raw, "_stream_load")
	}
	return strings.Contains(strings.Trim(parsed.Path, "/"), "_stream_load")
}

func loadEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open env file %q: %w", path, err)
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=value", path, lineNumber)
		}

		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		value = strings.TrimSpace(value)
		if value != "" && value[0] != '"' && value[0] != '\'' {
			// unquoted value: drop a trailing " # comment", as a shell would
			if i := strings.Index(value, " #"); i >= 0 {
				value = strings.TrimSpace(value[:i])
			} else if i := strings.Index(value, "\t#"); i >= 0 {
				value = strings.TrimSpace(value[:i])
			}
		}
		value = strings.Trim(value, `"'`)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: env key cannot be empty", path, lineNumber)
		}
		values[key] = os.Expand(value, os.Getenv)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file %q: %w", path, err)
	}

	return values, nil
}

// tuningKeys are the loader settings that, like every VELODB_* variable, can be
// overridden from the process environment without editing the env file.
var tuningKeys = []string{
	"ROWS_PER_SECOND",
	"TOTAL_ROWS",
	"GENERATOR_THREADS",
	"STREAMLOAD_BATCH_MIB",
	"STREAMLOAD_GENERATOR_BATCH_ROWS",
	"STREAMLOAD_LINGER",
	"STREAMLOAD_COMPRESSION",
	"STREAMLOAD_UPLOAD_WORKERS",
	"STREAMLOAD_UPLOAD_REQUEST_TIMEOUT",
	"STREAMLOAD_UPLOAD_TIMEOUT",
	"STREAMLOAD_MAX_QUEUE_SIZE",
	"STREAMLOAD_MAX_UPLOAD_QUEUE_SIZE",
	"STREAMLOAD_MAX_QUEUE_WAIT",
	"STREAMLOAD_LABEL_PREFIX",
}

// loadSettings reads the optional env file and overlays the process
// environment on top of it: environment > env file > built-in defaults.
func loadSettings(envPath string) (map[string]string, error) {
	values := map[string]string{}
	if envPath != "" {
		fileValues, err := loadEnvFile(envPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		maps.Copy(values, fileValues)
	}
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "VELODB_") || slices.Contains(tuningKeys, key) {
			values[key] = value
		}
	}
	return values, nil
}

// connection holds the VeloDB / Doris endpoint, credentials and demo table.
type connection struct {
	User          string
	Password      string
	Database      string
	Table         string
	MySQLAddr     string
	StreamLoadURL string
}

func loadConnection(values map[string]string) (connection, error) {
	setting := func(key, fallback string) string {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
		return fallback
	}

	host := setting("VELODB_HOST", "")
	user := setting("VELODB_USER", "")
	var missing []string
	if host == "" {
		missing = append(missing, "VELODB_HOST")
	}
	if user == "" {
		missing = append(missing, "VELODB_USER")
	}
	if len(missing) > 0 {
		return connection{}, fmt.Errorf("%s not set - copy env.example to .env or export it (see README)", strings.Join(missing, ", "))
	}

	streamLoadPort := setting("VELODB_STREAM_LOAD_PORT", "8080")
	conn := connection{
		User:          user,
		Password:      values["VELODB_PASSWORD"],
		Database:      setting("VELODB_DATABASE", "demo_observability"),
		Table:         setting("VELODB_TABLE", "log_events"),
		MySQLAddr:     net.JoinHostPort(host, setting("VELODB_MYSQL_PORT", "9030")),
		StreamLoadURL: setting("VELODB_STREAM_LOAD_URL", "http://"+net.JoinHostPort(host, streamLoadPort)),
	}
	if err := validateIdentifier("VELODB_DATABASE", conn.Database); err != nil {
		return connection{}, err
	}
	if err := validateIdentifier("VELODB_TABLE", conn.Table); err != nil {
		return connection{}, err
	}
	return conn, nil
}

func loadConfig(envPath string) (appConfig, error) {
	values, err := loadSettings(envPath)
	if err != nil {
		return appConfig{}, err
	}
	conn, err := loadConnection(values)
	if err != nil {
		return appConfig{}, err
	}

	streamLoadBatchBytes, err := envMiB(values, "STREAMLOAD_BATCH_MIB", 80)
	if err != nil {
		return appConfig{}, err
	}
	streamLoadLinger, err := envDuration(values, "STREAMLOAD_LINGER", 5000*time.Millisecond)
	if err != nil {
		return appConfig{}, err
	}
	streamLoadCompression := strings.ToLower(strings.TrimSpace(values["STREAMLOAD_COMPRESSION"]))
	if streamLoadCompression == "" {
		streamLoadCompression = string(dorisstreamload.CompressionGzip)
	}
	streamLoadUploadWorkers, err := envInt(values, "STREAMLOAD_UPLOAD_WORKERS", 1)
	if err != nil {
		return appConfig{}, err
	}
	generatorThreads, err := envInt(values, "GENERATOR_THREADS", 4)
	if err != nil {
		return appConfig{}, err
	}
	if generatorThreads == 0 {
		return appConfig{}, fmt.Errorf("GENERATOR_THREADS must be greater than 0")
	}
	streamLoadUploadRequestTimeout, err := envDuration(values, "STREAMLOAD_UPLOAD_REQUEST_TIMEOUT", 30*time.Second)
	if err != nil {
		return appConfig{}, err
	}
	streamLoadUploadTimeout, err := envDuration(values, "STREAMLOAD_UPLOAD_TIMEOUT", 30*time.Second)
	if err != nil {
		return appConfig{}, err
	}
	streamLoadMaxQueueSize, err := envInt(values, "STREAMLOAD_MAX_QUEUE_SIZE", 0)
	if err != nil {
		return appConfig{}, err
	}
	streamLoadMaxUploadQueueSize, err := envInt(values, "STREAMLOAD_MAX_UPLOAD_QUEUE_SIZE", 0)
	if err != nil {
		return appConfig{}, err
	}
	streamLoadMaxQueueWait, err := envDuration(values, "STREAMLOAD_MAX_QUEUE_WAIT", 0)
	if err != nil {
		return appConfig{}, err
	}
	streamLoadLabelPrefix := strings.TrimSpace(values["STREAMLOAD_LABEL_PREFIX"])
	streamLoadGeneratorBatchRows, err := envInt(values, "STREAMLOAD_GENERATOR_BATCH_ROWS", 10000)
	if err != nil {
		return appConfig{}, err
	}
	if streamLoadGeneratorBatchRows <= 0 {
		return appConfig{}, fmt.Errorf("STREAMLOAD_GENERATOR_BATCH_ROWS must be greater than 0")
	}
	rowsPerSecond, err := envInt(values, "ROWS_PER_SECOND", 100)
	if err != nil {
		return appConfig{}, err
	}
	totalRows, err := envInt64(values, "TOTAL_ROWS", 0)
	if err != nil {
		return appConfig{}, err
	}

	return appConfig{
		StreamLoadURL:                  conn.StreamLoadURL,
		User:                           conn.User,
		Password:                       conn.Password,
		Database:                       conn.Database,
		Table:                          conn.Table,
		MySQLAddr:                      conn.MySQLAddr,
		StreamLoadBatchBytes:           streamLoadBatchBytes,
		StreamLoadLinger:               streamLoadLinger,
		StreamLoadCompression:          streamLoadCompression,
		StreamLoadUploadWorkers:        streamLoadUploadWorkers,
		GeneratorThreads:               generatorThreads,
		StreamLoadUploadRequestTimeout: streamLoadUploadRequestTimeout,
		StreamLoadUploadTimeout:        streamLoadUploadTimeout,
		StreamLoadMaxQueueSize:         streamLoadMaxQueueSize,
		StreamLoadMaxUploadQueueSize:   streamLoadMaxUploadQueueSize,
		StreamLoadMaxQueueWait:         streamLoadMaxQueueWait,
		StreamLoadLabelPrefix:          streamLoadLabelPrefix,
		StreamLoadGeneratorBatchRows:   streamLoadGeneratorBatchRows,
		RowsPerSecond:                  rowsPerSecond,
		TotalRows:                      totalRows,
	}, nil
}

func envInt(values map[string]string, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must be >= 0, got %d", key, value)
	}
	return value, nil
}

func envInt64(values map[string]string, key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must be >= 0, got %d", key, value)
	}
	return value, nil
}

func envMiB(values map[string]string, key string, fallbackMiB int) (int, error) {
	mib, err := envInt(values, key, fallbackMiB)
	if err != nil {
		return 0, err
	}
	return mib * 1024 * 1024, nil
}

func envDuration(values map[string]string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration like 20ms, 30s, or 2m, got %q", key, raw)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must be >= 0, got %s", key, raw)
	}
	return value, nil
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateIdentifier(name, value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q is not a simple Doris identifier; use letters, numbers, and underscores, starting with a letter or underscore", name, value)
	}
	return nil
}

func schemaPath(envPath string) (string, error) {
	candidates := []string{
		"create.sql",
		filepath.Join(filepath.Dir(envPath), "create.sql"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("create.sql not found; looked in current directory and next to %s", envPath)
}

func runRecreateTable(args []string) error {
	fs := flag.NewFlagSet("recreate-table", flag.ExitOnError)
	envPath := fs.String("env", ".env", "Optional env file; VELODB_* and tuning variables in the environment take precedence")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*envPath)
	if err != nil {
		return err
	}
	if err := validateIdentifier("DATABASE", cfg.Database); err != nil {
		return err
	}
	if err := validateIdentifier("TABLE", cfg.Table); err != nil {
		return err
	}
	mysqlCfg := mysql.Config{
		User:                 cfg.User,
		Passwd:               cfg.Password,
		Net:                  "tcp",
		Addr:                 cfg.MySQLAddr,
		AllowNativePasswords: true,
		Timeout:              10 * time.Second,
		ReadTimeout:          30 * time.Second,
		WriteTimeout:         30 * time.Second,
	}
	db, err := sql.Open("mysql", mysqlCfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("open Doris SQL connection: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("connect to Doris MySQL endpoint %s: %w", cfg.MySQLAddr, err)
	}
	_, dropErr := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`.`%s`", cfg.Database, cfg.Table))
	closeErr := db.Close()
	if dropErr != nil {
		return fmt.Errorf("drop table %s.%s: %w", cfg.Database, cfg.Table, dropErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Doris SQL connection: %w", closeErr)
	}
	if err := applySchema(cfg, *envPath); err != nil {
		return err
	}
	fmt.Printf("recreated table %s.%s with the static 2026 weekly layout\n", cfg.Database, cfg.Table)
	return nil
}

func applySchema(cfg appConfig, envPath string) error {
	if err := validateIdentifier("DATABASE", cfg.Database); err != nil {
		return err
	}
	if err := validateIdentifier("TABLE", cfg.Table); err != nil {
		return err
	}

	path, err := schemaPath(envPath)
	if err != nil {
		return err
	}
	ddl, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	sqlText := strings.ReplaceAll(string(ddl), "{{DATABASE}}", cfg.Database)
	sqlText = strings.ReplaceAll(sqlText, "{{TABLE}}", cfg.Table)

	mysqlCfg := mysql.Config{
		User:                 cfg.User,
		Passwd:               cfg.Password,
		Net:                  "tcp",
		Addr:                 cfg.MySQLAddr,
		AllowNativePasswords: true,
		Timeout:              10 * time.Second,
		ReadTimeout:          30 * time.Second,
		WriteTimeout:         30 * time.Second,
	}

	db, err := sql.Open("mysql", mysqlCfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("open Doris SQL connection: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("connect to Doris MySQL endpoint %s: %w", cfg.MySQLAddr, err)
	}

	for _, stmt := range splitSQLStatements(sqlText) {
		if isCreateTableStatement(stmt) {
			exists, err := tableExists(db, cfg.Database, cfg.Table)
			if err != nil {
				return err
			}
			if exists {
				fmt.Printf("table %s.%s already exists; skipping CREATE TABLE\n", cfg.Database, cfg.Table)
				continue
			}
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("execute schema statement %q: %w", firstLine(stmt), err)
		}
	}

	return nil
}

func isCreateTableStatement(stmt string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(stmt))
	return strings.HasPrefix(normalized, "CREATE TABLE ")
}

func tableExists(db *sql.DB, database, table string) (bool, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		database,
		table,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check whether table %s.%s exists: %w", database, table, err)
	}
	return count > 0, nil
}

func splitSQLStatements(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt != "" {
			statements = append(statements, stmt)
		}
	}
	return statements
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > 120 {
		return line[:120] + "..."
	}
	return line
}

func randomMessage(rng *rand.Rand) string {
	n := rng.Intn(40) + 8
	out := make([]string, n)
	for i := range out {
		out[i] = loremWords[rng.Intn(len(loremWords))]
	}
	return strings.Join(out, " ")
}

func randomPayload(rng *rand.Rand) Payload {
	c := cities[rng.Intn(len(cities))]
	due := eventTimeAnchorTime.Add(time.Duration(rng.Intn(29*24)+24) * time.Hour)

	return Payload{
		City:        c.City,
		CountryCode: c.Country,
		Message:     randomMessage(rng),
		Quantity:    rng.Intn(10) + 1,
		Severity:    severities[rng.Intn(len(severities))],
		DueDate:     due.UnixMilli(),
	}
}

func randomHostID(rng *rand.Rand) string {
	return fmt.Sprintf("host%03d", rng.Intn(100))
}

func eventTimeForIndex(index, partitionRows int64, partition int, rng *rand.Rand) time.Time {
	weekStart := eventYearStart.AddDate(0, 0, partition*7)
	daysInWeek := int64(7)
	if partition == 52 {
		daysInWeek = 1 // The final static partition covers Dec 31 only.
	}
	dayInWeek, rowInDay, rowsInDay := distributedGroup(index, partitionRows, daysInWeek)
	hour, rowInHour, rowsInHour := distributedGroup(rowInDay, rowsInDay, 24)

	// Divide each hour into one ordered slot per row, then randomize within
	// that slot. This gives uniform hourly coverage without timestamp ties or
	// moving backwards in time.
	rowsPerHourSlot := time.Hour.Microseconds() / rowsInHour
	withinHour := rowInHour*rowsPerHourSlot + rng.Int63n(rowsPerHourSlot)
	eventTime := weekStart.AddDate(0, 0, int(dayInWeek)).
		Add(time.Duration(hour)*time.Hour + time.Duration(withinHour)*time.Microsecond)
	return eventTime
}

// distributedGroup splits total rows across ordered groups. Earlier groups
// receive one extra row until the remainder is exhausted, so group sizes differ
// by at most one. The returned row offset is local to the selected group.
func distributedGroup(index, total, groups int64) (group, rowInGroup, rowsInGroup int64) {
	base, extra := total/groups, total%groups
	longGroupRows := (base + 1) * extra
	if index < longGroupRows {
		rowsInGroup = base + 1
		group = index / rowsInGroup
		rowInGroup = index % rowsInGroup
		return
	}
	rowsInGroup = base
	remaining := index - longGroupRows
	group = extra + remaining/rowsInGroup
	rowInGroup = remaining % rowsInGroup
	return
}

// partitionTargets allocates the requested total in proportion to each static
// partition's covered days. Rounding leftovers go to the largest fractional
// shares, so every partition is within one row of its proportional target.
func partitionTargets(totalRows int64) []int64 {
	targets := make([]int64, 53)
	remainders := make([]int64, 53)
	var assigned int64
	dailyBase, dailyRemainder := totalRows/365, totalRows%365
	for partition := range targets {
		days := int64(7)
		if partition == 52 {
			days = 1
		}
		weightedRemainder := dailyRemainder * days
		targets[partition] = dailyBase*days + weightedRemainder/365
		remainders[partition] = weightedRemainder % 365
		assigned += targets[partition]
	}
	for left := totalRows - assigned; left > 0; left-- {
		best := 0
		for partition := 1; partition < len(remainders); partition++ {
			if remainders[partition] > remainders[best] {
				best = partition
			}
		}
		targets[best]++
		remainders[best] = -1
	}
	return targets
}

func currentPartitionRows(cfg appConfig) ([]int64, int64, error) {
	mysqlCfg := mysql.Config{
		User: cfg.User, Passwd: cfg.Password, Net: "tcp", Addr: cfg.MySQLAddr,
		AllowNativePasswords: true, Timeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second,
	}
	db, err := sql.Open("mysql", mysqlCfg.FormatDSN())
	if err != nil {
		return nil, 0, fmt.Errorf("open Doris SQL connection: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, 0, fmt.Errorf("connect to Doris MySQL endpoint %s: %w", cfg.MySQLAddr, err)
	}
	partitionExpr := `CASE WHEN event_time < '2026-01-01 00:00:00' OR event_time >= '2027-01-01 00:00:00'
       THEN -1 ELSE CAST(FLOOR(DATEDIFF(event_time, '2026-01-01') / 7) AS BIGINT) END`
	query := fmt.Sprintf(`SELECT
  %s AS partition_index,
  COUNT(*)
FROM %s.%s
GROUP BY %s`, partitionExpr, quoteIdentifier(cfg.Database), quoteIdentifier(cfg.Table), partitionExpr)
	rows, err := db.Query(query)
	if err != nil {
		return nil, 0, fmt.Errorf("read current row counts by partition: %w", err)
	}
	defer rows.Close()
	counts := make([]int64, 53)
	var total int64
	for rows.Next() {
		var partition, count int64
		if err := rows.Scan(&partition, &count); err != nil {
			return nil, 0, fmt.Errorf("read partition row count: %w", err)
		}
		if partition < 0 || partition >= int64(len(counts)) {
			return nil, 0, fmt.Errorf("found %d rows outside the static 2026 partition range", count)
		}
		counts[partition] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("finish reading partition row counts: %w", err)
	}
	return counts, total, nil
}

func quoteIdentifier(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }

func randomEventCSV(index, partitionRows int64, partition int, rng *rand.Rand) (string, error) {
	eventTime := eventTimeForIndex(index, partitionRows, partition, rng)
	payload, err := json.Marshal(randomPayload(rng))
	if err != nil {
		return "", err
	}

	return strings.Join([]string{
		eventTime.Format("2006-01-02 15:04:05.000000"),
		randomHostID(rng),
		string(payload),
	}, csvSeparator), nil
}

// rateLimiterTick is the fixed cadence at which the limiter wakes up. It stays
// well above the runtime's timer granularity so the achievable row rate is
// never bounded by how often we can be scheduled.
const rateLimiterTick = 10 * time.Millisecond

// startRateLimiter emits one token per row. It ticks at a fixed interval and
// releases every row that has come due since the run started, rather than one
// row per tick: a token-per-tick scheme needs rowsPerSecond ticks per second,
// and the Go runtime tops out at a few thousand ticks per second, which capped
// throughput at ~13% of target at 10k rows/s and ~3% at 100k rows/s.
func startRateLimiter(ctx context.Context, rowsPerSecond int) <-chan struct{} {
	if rowsPerSecond <= 0 {
		return nil
	}

	tokens := make(chan struct{}, rowsPerSecond)

	go func() {
		ticker := time.NewTicker(rateLimiterTick)
		defer ticker.Stop()
		defer close(tokens)

		start := time.Now()
		var released int64

		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				target := int64(now.Sub(start).Seconds() * float64(rowsPerSecond))
				for released < target {
					select {
					case tokens <- struct{}{}:
						released++
					default:
						// A full buffer means the producers are the constraint,
						// not the limiter. Discard the backlog so a stall can't
						// bank credit and burst later.
						released = target
					}
				}
			}
		}
	}()

	return tokens
}

type loaderOptions struct {
	envPath       string
	inspect       bool
	batchRows     int
	threads       int
	rowsPerSecond int
	totalRows     int64
	stats         *ingestStats
}

func defaultLoaderOptions() loaderOptions {
	return loaderOptions{
		envPath:       ".env",
		threads:       0,
		rowsPerSecond: -1,
		totalRows:     -1,
	}
}

func applyLoaderOverrides(cfg appConfig, opts loaderOptions) appConfig {
	if opts.rowsPerSecond >= 0 {
		cfg.RowsPerSecond = opts.rowsPerSecond
	}
	if opts.totalRows >= 0 {
		cfg.TotalRows = opts.totalRows
	}
	if opts.batchRows > 0 {
		cfg.StreamLoadGeneratorBatchRows = opts.batchRows
	}
	return cfg
}

func effectiveGeneratorBatchRows(cfg appConfig) int {
	batchRows := cfg.StreamLoadGeneratorBatchRows
	if cfg.RowsPerSecond > 0 {
		batchRows = min(batchRows, cfg.RowsPerSecond*3)
	}
	if batchRows < 1 {
		return 1
	}
	return batchRows
}

func runLoaderWithContext(ctx context.Context, opts loaderOptions) error {
	if opts.threads < 0 {
		return fmt.Errorf("--threads must not be negative")
	}
	cfg, err := loadConfig(opts.envPath)
	if err != nil {
		return err
	}
	if opts.threads <= 0 {
		opts.threads = cfg.GeneratorThreads
	}
	if opts.threads <= 0 {
		return fmt.Errorf("generator thread count must be greater than 0")
	}
	cfg = applyLoaderOverrides(cfg, opts)
	if cfg.StreamLoadGeneratorBatchRows <= 0 {
		return fmt.Errorf("STREAMLOAD_GENERATOR_BATCH_ROWS must be greater than 0")
	}
	if cfg.TotalRows <= 0 {
		return fmt.Errorf("TOTAL_ROWS must be greater than 0 for the fixed-year event-time schedule")
	}
	if opts.stats == nil {
		opts.stats = &ingestStats{}
	}
	generatorBatchRows := effectiveGeneratorBatchRows(cfg)

	fmt.Printf("ensuring schema %s.%s via %s...\n", cfg.Database, cfg.Table, cfg.MySQLAddr)
	if err := applySchema(cfg, opts.envPath); err != nil {
		return err
	}

	client, err := dorisstreamload.NewClient(dorisstreamload.Config{
		Endpoint:      cfg.StreamLoadEndpoint(),
		StreamLoadURL: cfg.FullStreamLoadURL(),
		Database:      cfg.Database,
		Table:         cfg.Table,

		Columns: []string{
			"event_time",
			"host_id",
			"payload",
		},

		Mode:         dorisstreamload.ModeCSV,
		CSVSeparator: csvSeparator,
		Validation:   dorisstreamload.ValidateNone,

		AuthenticationType:  dorisstreamload.AuthenticationBasic,
		AuthenticationToken: fmt.Sprintf("%s:%s", cfg.User, cfg.Password),

		BatchBytes:                cfg.StreamLoadBatchBytes,
		Linger:                    cfg.StreamLoadLinger,
		CompressionType:           dorisstreamload.CompressionType(cfg.StreamLoadCompression),
		DorisUploadWorkers:        cfg.StreamLoadUploadWorkers,
		DorisUploadRequestTimeout: cfg.StreamLoadUploadRequestTimeout,
		DorisUploadTimeout:        cfg.StreamLoadUploadTimeout,
		MaxQueueSize:              cfg.StreamLoadMaxQueueSize,
		MaxUploadQueueSize:        cfg.StreamLoadMaxUploadQueueSize,
		MaxQueueWaitTime:          cfg.StreamLoadMaxQueueWait,
		LabelPrefix:               cfg.StreamLoadLabelPrefix,
	})
	if err != nil {
		return err
	}
	defer client.Close()

	rateDescription := "as fast as possible"
	if cfg.RowsPerSecond > 0 {
		rateDescription = fmt.Sprintf("%d rows/s", cfg.RowsPerSecond)
	}
	totalDescription := "forever"
	if cfg.TotalRows > 0 {
		totalDescription = fmt.Sprintf("%d total rows", cfg.TotalRows)
	}
	fmt.Printf("streaming %s with %d producer threads and %d Stream Load upload workers until %s using %d generated rows per SendBatch...\n",
		rateDescription, opts.threads, cfg.StreamLoadUploadWorkers, totalDescription, generatorBatchRows)
	targets := partitionTargets(cfg.TotalRows)
	currentCounts, currentTotal, err := currentPartitionRows(cfg)
	if err != nil {
		return err
	}
	if cfg.TotalRows <= currentTotal {
		return fmt.Errorf("target row count %d must be greater than current row count %d", cfg.TotalRows, currentTotal)
	}
	opts.stats.initialRows.Store(currentTotal)
	deficits := make([]int64, len(targets))
	var rowsToGenerate int64
	for partition, target := range targets {
		if currentCounts[partition] > target {
			return fmt.Errorf("partition p%s has %d rows, above its proportional target of %d; top-up cannot remove rows", eventYearStart.AddDate(0, 0, partition*7).Format("20060102"), currentCounts[partition], target)
		}
		deficits[partition] = target - currentCounts[partition]
		rowsToGenerate += deficits[partition]
	}
	fmt.Printf("current rows: %d; target rows: %d; generating exactly %d missing rows\n", currentTotal, cfg.TotalRows, rowsToGenerate)
	fmt.Printf("partition targets are proportional to covered days; weekly counts differ by at most one, query anchor %s UTC\n", queryTimeAnchorTime.Format("2006-01-02 15:04:05"))

	handles := make(chan submittedHandle, 1024)
	opts.stats.clientStats = client.Stats
	_, reaperDone := startHandleReaper(handles, opts.stats)
	startInspector(opts.inspect, rowsToGenerate)

	limiter := startRateLimiter(ctx, cfg.RowsPerSecond)
	for partition, deficit := range deficits {
		if deficit == 0 {
			continue
		}
		fmt.Printf("partition p%s: current=%d target=%d adding=%d\n",
			eventYearStart.AddDate(0, 0, partition*7).Format("20060102"), currentCounts[partition], targets[partition], deficit)
		var generated atomic.Int64
		if err := startOrderedProducers(ctx, client, limiter, handles, generatorBatchRows, deficit, partition, opts.threads, &generated, &opts.stats.produced); err != nil {
			fmt.Println("producer error:", err)
			break
		}
	}
	// Flush the client before retiring the reaper: Close drains buffered rows
	// that have not hit BatchBytes/Linger yet and waits for the upload workers,
	// so those handles resolve while the reaper is still counting them.
	client.Close()
	close(handles)
	<-reaperDone
	finalCounts, finalTotal, err := currentPartitionRows(cfg)
	if err != nil {
		return err
	}
	if finalTotal != cfg.TotalRows {
		return fmt.Errorf("load finished with %d rows; target is %d", finalTotal, cfg.TotalRows)
	}
	for partition, target := range targets {
		if finalCounts[partition] != target {
			return fmt.Errorf("partition p%s finished with %d rows; target is %d", eventYearStart.AddDate(0, 0, partition*7).Format("20060102"), finalCounts[partition], target)
		}
	}

	if err := ctx.Err(); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func claimRowIndex(totalRows int64, generated *atomic.Int64) (int64, bool) {
	for {
		current := generated.Load()
		if current >= totalRows {
			return 0, false
		}
		if generated.CompareAndSwap(current, current+1) {
			return current, true
		}
	}
}

func startInspector(enabled bool, totalRows int64) {
	if !enabled {
		return
	}

	rng := rand.New(rand.NewSource(1))
	printInspectionEvent := func() {
		row, err := randomEventCSV(0, totalRows, 0, rng)
		if err != nil {
			fmt.Println("inspect error:", err)
			return
		}
		fmt.Println("inspect csv:", row)
	}

	printInspectionEvent()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			printInspectionEvent()
		}
	}()
}

func csvBatchBytes(records []string) int {
	if len(records) == 0 {
		return 0
	}

	bytes := 0
	for i, record := range records {
		if i > 0 {
			bytes++ // '\n'
		}
		bytes += len(record)
	}
	return bytes
}

type submittedHandle struct {
	handle *dorisstreamload.Handle
	rows   int
	bytes  int
}

type reapedHandle struct {
	submitted submittedHandle
	result    dorisstreamload.DeliveryResult
}

type ingestStats struct {
	startedAt   time.Time
	initialRows atomic.Int64
	handles     atomic.Int64
	succeeded   atomic.Int64
	failed      atomic.Int64
	// produced counts generated rows enqueued for ordered submission. It runs
	// ahead of rows, which only counts rows Doris has acknowledged.
	produced atomic.Int64
	rows     atomic.Int64
	bytes    atomic.Int64
	attempts atomic.Int64

	// clientStats, when set, reports the SDK's own upload counters so the
	// stats line shows real stream loads rather than SendBatch handles.
	clientStats func() dorisstreamload.ClientStats
}

func startHandleReaper(handles <-chan submittedHandle, stats *ingestStats) (*ingestStats, <-chan struct{}) {
	if stats == nil {
		stats = &ingestStats{}
	}
	stats.startedAt = time.Now()
	results := make(chan reapedHandle, 1024)
	done := make(chan struct{})

	go func() {
		var wg sync.WaitGroup
		for submitted := range handles {
			submitted := submitted
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- reapedHandle{
					submitted: submitted,
					result:    submitted.handle.Wait(),
				}
			}()
		}
		wg.Wait()
		close(results)
	}()

	go func() {
		defer close(done)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case reaped, ok := <-results:
				if !ok {
					printStats(stats)
					return
				}
				submitted := reaped.submitted
				result := reaped.result
				stats.handles.Add(1)
				stats.attempts.Add(int64(result.Attempts))

				if result.Err != nil {
					stats.failed.Add(1)
					fmt.Println("load failed:", result.Err)
					continue
				}

				stats.succeeded.Add(1)
				stats.rows.Add(int64(submitted.rows))
				stats.bytes.Add(int64(submitted.bytes))

			case <-ticker.C:
				printStats(stats)
			}
		}
	}()

	return stats, done
}

func printStats(stats *ingestStats) {
	elapsed := time.Since(stats.startedAt).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	rows := stats.rows.Load()
	bytes := stats.bytes.Load()
	produced := stats.produced.Load()
	loads := ""
	if stats.clientStats != nil {
		cs := stats.clientStats()
		avgRows := 0.0
		if cs.TotalLoadJobs > 0 && bytes > 0 {
			// Rows per load, estimated from the acknowledged byte/row ratio.
			avgRows = cs.AverageLoadSize / (float64(bytes) / float64(rows))
		}
		loads = fmt.Sprintf(" loads=%d avg_load_bytes=%.0f avg_load_rows=%.0f busy_workers=%d/%d",
			cs.TotalLoadJobs, cs.AverageLoadSize, avgRows, cs.BusyWorkers, cs.TotalWorkers)
	}
	fmt.Printf(
		"stats handles=%d ok=%d failed=%d produced=%d rows=%d inflight=%d bytes=%d rows/s=%.2f bytes/s=%.2f attempts=%d%s\n",
		stats.handles.Load(),
		stats.succeeded.Load(),
		stats.failed.Load(),
		produced,
		rows,
		produced-rows,
		bytes,
		float64(rows)/elapsed,
		float64(bytes)/elapsed,
		stats.attempts.Load(),
		loads,
	)
}

type generatedRecord struct {
	index int64
	row   string
	err   error
}

func startOrderedProducers(
	ctx context.Context,
	client *dorisstreamload.Client,
	limiter <-chan struct{},
	handles chan<- submittedHandle,
	batchRows int,
	totalRows int64,
	partition int,
	producerCount int,
	generated *atomic.Int64,
	produced *atomic.Int64,
) error {
	windowSize := max(1, producerCount*4)
	window := make(chan struct{}, windowSize)
	records := make(chan generatedRecord, windowSize)
	var producers sync.WaitGroup

	for i := 0; i < producerCount; i++ {
		workerID := i
		producers.Add(1)
		go func() {
			defer producers.Done()
			seed := int64(partition+1)*1_000_003 + int64(workerID+1)
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case window <- struct{}{}:
				case <-ctx.Done():
					return
				}

				if limiter != nil {
					select {
					case _, ok := <-limiter:
						if !ok {
							<-window
							return
						}
					case <-ctx.Done():
						<-window
						return
					}
				}
				select {
				case <-ctx.Done():
					<-window
					return
				default:
				}

				index, ok := claimRowIndex(totalRows, generated)
				if !ok {
					<-window
					return
				}
				row, err := randomEventCSV(index, totalRows, partition, rng)
				// A claimed index must reach the ordered collector so a row error
				// cannot leave a gap that blocks every later event.
				records <- generatedRecord{index: index, row: row, err: err}
				if err == nil {
					produced.Add(1)
				}
			}
		}()
	}

	go func() {
		producers.Wait()
		close(records)
	}()

	batch := make([]string, 0, batchRows)
	firstErr := error(nil)
	flush := func() {
		if len(batch) == 0 {
			return
		}

		rows := len(batch)
		bytes := csvBatchBytes(batch)
		handle, err := client.SendBatch(batch)
		if err != nil {
			fmt.Println("send error:", err)
			if firstErr == nil {
				firstErr = err
			}
			batch = batch[:0]
			return
		}

		// Send unconditionally, even once ctx is cancelled. The rows are already
		// committed to the client, so dropping the handle here would undercount
		// rows that Doris still ends up loading. The reaper drains a buffered
		// channel and only exits on close(handles), so this cannot deadlock.
		handles <- submittedHandle{handle: handle, rows: rows, bytes: bytes}

		batch = batch[:0]
	}

	pending := make(map[int64]generatedRecord, windowSize)
	var nextIndex int64
	for record := range records {
		pending[record.index] = record
		for {
			ordered, ok := pending[nextIndex]
			if !ok {
				break
			}
			delete(pending, nextIndex)
			if ordered.err != nil {
				fmt.Println("event error:", ordered.err)
				if firstErr == nil {
					firstErr = ordered.err
				}
			} else {
				batch = append(batch, ordered.row)
				if len(batch) >= batchRows {
					flush()
				}
			}
			nextIndex++
			<-window
		}
	}
	flush()
	return firstErr
}

func runLoader(args []string) error {
	fs := flag.NewFlagSet("loader", flag.ExitOnError)
	opts := defaultLoaderOptions()

	fs.StringVar(&opts.envPath, "env", opts.envPath, "Optional env file; VELODB_* and tuning variables in the environment take precedence")
	fs.BoolVar(&opts.inspect, "inspect", false, "Print one generated event every 30 seconds")
	fs.IntVar(&opts.batchRows, "batch-rows", opts.batchRows, "Override STREAMLOAD_GENERATOR_BATCH_ROWS for generated rows per SendBatch")
	fs.IntVar(&opts.threads, "threads", opts.threads, "Number of producer goroutines")

	if err := fs.Parse(args); err != nil {
		return err
	}
	// Cancel on SIGINT/SIGTERM so Ctrl-C unwinds through the producers' flush
	// and client.Close() instead of killing the process outright and dropping
	// every row still buffering or uploading.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runLoaderWithContext(ctx, opts)
}
