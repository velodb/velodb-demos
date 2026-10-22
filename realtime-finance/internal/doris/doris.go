// Package doris is the MySQL-protocol side: schema, reset, and state restore.
package doris

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"finsim/internal/config"

	"github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"
)

func dsn(env *config.DorisEnv, database string) string {
	c := mysql.NewConfig()
	c.User = env.User
	c.Passwd = env.Password
	c.Net = "tcp"
	c.Addr = fmt.Sprintf("%s:%d", env.Host, env.Port)
	c.DBName = database
	c.Timeout = 15 * time.Second
	c.ReadTimeout = 120 * time.Second
	c.WriteTimeout = 30 * time.Second
	c.InterpolateParams = true // Doris prefers text protocol
	c.AllowNativePasswords = true
	return c.FormatDSN()
}

// Open connects to the configured database (creating it first).
func Open(env *config.DorisEnv) (*sql.DB, error) {
	root, err := sql.Open("mysql", dsn(env, ""))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if _, err := root.Exec("CREATE DATABASE IF NOT EXISTS " + env.Database); err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	db, err := sql.Open("mysql", dsn(env, env.Database))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(10 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connect %s: %w", env.Host, err)
	}
	return db, nil
}

// EnsureSchema applies schema.sql (idempotent) and records starting cash.
// migrations add columns to tables created by earlier versions. Errors from columns
// that already exist are ignored.
var migrations = []string{
	"ALTER TABLE prices ADD COLUMN fair_price DECIMAL(18,6) NULL AFTER price",
	"ALTER TABLE latest_price ADD COLUMN fair_price DECIMAL(18,6) NULL AFTER price",
}

func EnsureSchema(db *sql.DB, schemaSQL string) error {
	for _, stmt := range splitStatements(schemaSQL) {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w\n--- statement:\n%s", err, firstLines(stmt, 3))
		}
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(strings.ToLower(err.Error()), "exist") {
			return fmt.Errorf("migration: %w\n%s", err, m)
		}
	}
	return nil
}

// StartingCash returns the recorded starting cash, or zero if none is recorded yet.
func StartingCash(db *sql.DB) (decimal.Decimal, error) {
	var v sql.NullString
	err := db.QueryRow("SELECT v FROM book_meta WHERE k = 'starting_cash'").Scan(&v)
	if err == sql.ErrNoRows {
		return decimal.Zero, nil
	}
	if err != nil {
		return decimal.Zero, err
	}
	return dec(v), nil
}

// SetStartingCash records (overwrites) the starting cash used by v_pnl.
func SetStartingCash(db *sql.DB, v decimal.Decimal) error {
	_, err := db.Exec("INSERT INTO book_meta (k, v) VALUES ('starting_cash', " + v.StringFixed(6) + ")")
	return err
}

// metaForgotten is the book_meta key holding how much paid-out cash has been
// written off ("forget withdrawals"): v_pnl and the simulator stop counting it as profit.
const metaForgotten = "paid_out_forgotten"

// PaidOutForgotten returns the written-off part of the cashouts (zero if never set).
func PaidOutForgotten(ctx context.Context, db *sql.DB) (decimal.Decimal, error) {
	var v sql.NullString
	err := db.QueryRowContext(ctx, "SELECT SUM(v) FROM book_meta WHERE k = '"+metaForgotten+"'").Scan(&v)
	if err != nil {
		return decimal.Zero, err
	}
	return dec(v), nil
}

// SetPaidOutForgotten records (overwrites) the written-off amount.
func SetPaidOutForgotten(ctx context.Context, db *sql.DB, v decimal.Decimal) error {
	_, err := db.ExecContext(ctx, "INSERT INTO book_meta (k, v) VALUES ('"+metaForgotten+"', "+v.StringFixed(6)+")")
	return err
}

var dataTables = []string{"trades", "prices", "latest_price", "book_snapshots", "calibrations", "cashouts"}
var materializedViews = []string{"mv_positions", "mv_cash", "mv_position_minute", "mv_price_minute"}

// Reset truncates every data table and asks the MVs to refresh.
func Reset(db *sql.DB) error {
	for _, t := range dataTables {
		if _, err := db.Exec("TRUNCATE TABLE " + t); err != nil {
			return fmt.Errorf("truncate %s: %w", t, err)
		}
	}
	RefreshMVs(db)
	return nil
}

// LastTickAge returns how long ago the simulator last wrote latest_price, or
// -1 when the table is empty. Used to refuse a reset while a simulator is running.
func LastTickAge(db *sql.DB) (time.Duration, error) {
	var age sql.NullFloat64
	if err := db.QueryRow("SELECT TIMESTAMPDIFF(SECOND, MAX(ts), UTC_TIMESTAMP()) FROM latest_price").Scan(&age); err != nil {
		return 0, err
	}
	if !age.Valid {
		return -1, nil
	}
	return time.Duration(age.Float64 * float64(time.Second)), nil
}

// DropAll drops every object the demo owns (views, materialized views, data
// tables, book_meta and any table left over from an earlier schema) so that
// EnsureSchema recreates the database from scratch. Used by `finsim reset`.
func DropAll(db *sql.DB) error {
	views := []string{"v_positions", "v_pnl", "v_position_history", "v_block_shortfall", "v_sectors"}
	tables := append([]string{"book_meta", "market_macro"}, dataTables...)
	for _, v := range views {
		if _, err := db.Exec("DROP VIEW IF EXISTS " + v); err != nil {
			return fmt.Errorf("drop view %s: %w", v, err)
		}
	}
	for _, mv := range materializedViews {
		if _, err := db.Exec("DROP MATERIALIZED VIEW IF EXISTS " + mv); err != nil {
			return fmt.Errorf("drop materialized view %s: %w", mv, err)
		}
	}
	for _, t := range tables {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + t + " FORCE"); err != nil {
			return fmt.Errorf("drop table %s: %w", t, err)
		}
	}
	return nil
}

// RefreshMVs triggers an immediate refresh of every MV (best effort, async).
func RefreshMVs(db *sql.DB) {
	for _, mv := range materializedViews {
		if _, err := db.Exec("REFRESH MATERIALIZED VIEW " + mv + " COMPLETE"); err != nil {
			log.Printf("doris: refresh %s: %v", mv, err)
		}
	}
}

// State is what the simulator restores on start.
type State struct {
	Prices    map[string]decimal.Decimal
	Positions map[string]struct {
		Qty     int64
		AvgCost decimal.Decimal
	}
	Bought, Sold, TradeCost, Realized decimal.Decimal
	PaidOut                           decimal.Decimal // sum of cashouts
	PaidOutForgotten                  decimal.Decimal // part of PaidOut written off (book_meta)
	Trades                            int64
	MaxTradeID, MaxBlockID            int64
	MaxCashoutID                      int64
}

func LoadState(ctx context.Context, db *sql.DB) (*State, error) {
	st := &State{Prices: map[string]decimal.Decimal{}}
	st.Positions = map[string]struct {
		Qty     int64
		AvgCost decimal.Decimal
	}{}

	rows, err := db.QueryContext(ctx, "SELECT symbol, price FROM latest_price")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sym, price string
		if err := rows.Scan(&sym, &price); err != nil {
			return nil, err
		}
		if d, err := decimal.NewFromString(price); err == nil {
			st.Prices[sym] = d
		}
	}
	rows.Close()

	var bought, sold, cost, realized sql.NullString
	var trades, maxTrade, maxBlock sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(trade_id), MAX(block_id),
		SUM(CASE WHEN side='BUY' THEN notional ELSE 0 END), SUM(CASE WHEN side='SELL' THEN notional ELSE 0 END),
		SUM(trade_cost), SUM(realized_pnl) FROM trades`).Scan(&trades, &maxTrade, &maxBlock, &bought, &sold, &cost, &realized)
	if err != nil {
		return nil, err
	}
	st.Trades = trades.Int64
	st.MaxTradeID = maxTrade.Int64
	st.MaxBlockID = maxBlock.Int64
	st.Bought, st.Sold, st.TradeCost, st.Realized = dec(bought), dec(sold), dec(cost), dec(realized)
	var paidOut sql.NullString
	var maxCashout sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT SUM(amount), MAX(cashout_id) FROM cashouts").Scan(&paidOut, &maxCashout); err != nil {
		return nil, err
	}
	st.PaidOut, st.MaxCashoutID = dec(paidOut), maxCashout.Int64
	if st.PaidOutForgotten, err = PaidOutForgotten(ctx, db); err != nil {
		return nil, err
	}
	if st.Trades == 0 {
		return st, nil
	}

	rows, err = db.QueryContext(ctx, `SELECT t.symbol, t.qty, t.avg_cost_after FROM (
		SELECT symbol, avg_cost_after, ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY ts DESC, trade_id DESC) AS rn,
		       SUM(CASE WHEN side='BUY' THEN qty ELSE -qty END) OVER (PARTITION BY symbol) AS qty
		FROM trades) t WHERE t.rn = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sym, avg string
		var qty int64
		if err := rows.Scan(&sym, &qty, &avg); err != nil {
			return nil, err
		}
		if qty > 0 {
			st.Positions[sym] = struct {
				Qty     int64
				AvgCost decimal.Decimal
			}{qty, decOr(avg)}
		}
	}
	return st, rows.Err()
}

func dec(s sql.NullString) decimal.Decimal {
	if !s.Valid {
		return decimal.Zero
	}
	return decOr(s.String)
}

func decOr(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func splitStatements(text string) []string {
	var out []string
	var cur []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		cur = append(cur, line)
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSuffix(strings.TrimSpace(strings.Join(cur, "\n")), ";")
			out = append(out, stmt)
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// ---- retention -------------------------------------------------------------

// Retention says how long to keep auto-created time partitions. Zero = forever.
// trades is the source of truth for positions and cash and must be kept forever;
// prices and book_snapshots are history and can be trimmed.
type Retention struct {
	Prices        time.Duration
	BookSnapshots time.Duration
}

// DropExpiredPartitions removes partitions whose upper bound is older than the
// retention window. Auto partitions are named p<yyyyMMddHHmmss> after their
// lower bound; hourly ones end an hour later, daily ones a day later.
func DropExpiredPartitions(db *sql.DB, r Retention) {
	type target struct {
		table string
		keep  time.Duration
		span  time.Duration
	}
	for _, t := range []target{{"prices", r.Prices, time.Hour}, {"book_snapshots", r.BookSnapshots, 24 * time.Hour}} {
		if t.keep <= 0 {
			continue
		}
		cutoff := time.Now().UTC().Add(-t.keep)
		rows, err := db.Query("SHOW PARTITIONS FROM " + t.table)
		if err != nil {
			log.Printf("retention: %s: %v", t.table, err)
			continue
		}
		cols, _ := rows.Columns()
		nameIdx := -1
		for i, c := range cols {
			if c == "PartitionName" {
				nameIdx = i
			}
		}
		var expired []string
		for rows.Next() {
			vals := make([]sql.RawBytes, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil || nameIdx < 0 {
				continue
			}
			name := string(vals[nameIdx])
			start, err := time.Parse("p20060102150405", name)
			if err != nil {
				continue
			}
			if start.Add(t.span).Before(cutoff) {
				expired = append(expired, name)
			}
		}
		rows.Close()
		for _, p := range expired {
			if _, err := db.Exec("ALTER TABLE " + t.table + " DROP PARTITION " + p); err != nil {
				log.Printf("retention: drop %s.%s: %v", t.table, p, err)
			} else {
				log.Printf("retention: dropped %s.%s", t.table, p)
			}
		}
	}
}

// ---- folding old trades ------------------------------------------------------
//
// trades cannot be kept forever, but it is the source of truth for positions,
// cash and P&L. Before an old partition is dropped its net effect per symbol is
// written as two carry-forward rows (one BUY with the total bought, one SELL with
// the total sold, fees and realised P&L attached) timestamped at the start of the
// oldest partition that is kept. Every SUM the MVs and the resume logic use is
// unchanged by the fold, so the book stays exact.

// Partition is one auto-created time partition.
type Partition struct {
	Name  string
	Start time.Time
	End   time.Time
}

// Partitions lists a table's auto partitions (name p<yyyyMMddHHmmss>), oldest first.
func Partitions(db *sql.DB, table string, span time.Duration) ([]Partition, error) {
	rows, err := db.Query("SHOW PARTITIONS FROM " + table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	nameIdx := -1
	for i, c := range cols {
		if c == "PartitionName" {
			nameIdx = i
		}
	}
	var out []Partition
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil || nameIdx < 0 {
			continue
		}
		name := string(vals[nameIdx])
		start, err := time.Parse("p20060102150405", name)
		if err != nil {
			continue
		}
		out = append(out, Partition{Name: name, Start: start, End: start.Add(span)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, rows.Err()
}

// Carry is the net effect of one symbol over a set of partitions.
type Carry struct {
	Symbol                    string
	BuyQty, SellQty           int64
	BuyNotional, SellNotional decimal.Decimal
	BuyCost, SellCost         decimal.Decimal
	Realized                  decimal.Decimal
	AvgCostAfter              decimal.Decimal
	CashAfter                 decimal.Decimal
}

// SummarizeTrades aggregates the named partitions per symbol.
func SummarizeTrades(db *sql.DB, parts []string) ([]Carry, error) {
	plist := strings.Join(parts, ", ")
	rows, err := db.Query(`SELECT t.symbol,
		SUM(CASE WHEN side='BUY' THEN qty ELSE 0 END), SUM(CASE WHEN side='BUY' THEN notional ELSE 0 END), SUM(CASE WHEN side='BUY' THEN trade_cost ELSE 0 END),
		SUM(CASE WHEN side='SELL' THEN qty ELSE 0 END), SUM(CASE WHEN side='SELL' THEN notional ELSE 0 END), SUM(CASE WHEN side='SELL' THEN trade_cost ELSE 0 END),
		SUM(realized_pnl), MAX(l.avg_cost_after), MAX(l.cash_after)
		FROM trades PARTITION (` + plist + `) t
		LEFT JOIN (SELECT symbol, avg_cost_after, cash_after FROM (
			SELECT symbol, avg_cost_after, cash_after, ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY ts DESC, trade_id DESC) rn
			FROM trades PARTITION (` + plist + `)) x WHERE rn = 1) l ON l.symbol = t.symbol
		GROUP BY t.symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Carry
	for rows.Next() {
		var c Carry
		var bq, sq sql.NullInt64
		var bn, bc, sn, sc, rz, avg, cash sql.NullString
		if err := rows.Scan(&c.Symbol, &bq, &bn, &bc, &sq, &sn, &sc, &rz, &avg, &cash); err != nil {
			return nil, err
		}
		c.BuyQty, c.SellQty = bq.Int64, sq.Int64
		c.BuyNotional, c.BuyCost, c.SellNotional, c.SellCost = dec(bn), dec(bc), dec(sn), dec(sc)
		c.Realized, c.AvgCostAfter, c.CashAfter = dec(rz), dec(avg), dec(cash)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountTradesFrom counts rows with trade_id >= first (used to wait for carry rows to land).
func CountTradesFrom(db *sql.DB, first int64) (int64, error) {
	var n int64
	err := db.QueryRow("SELECT COUNT(*) FROM trades WHERE trade_id >= " + fmt.Sprint(first)).Scan(&n)
	return n, err
}

// DropPartitions drops the named partitions of a table.
func DropPartitions(db *sql.DB, table string, parts []string) error {
	for _, p := range parts {
		if _, err := db.Exec("ALTER TABLE " + table + " DROP PARTITION " + p); err != nil {
			return fmt.Errorf("drop %s.%s: %w", table, p, err)
		}
	}
	return nil
}
