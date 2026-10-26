// Package web serves the dashboard and its JSON API.
package web

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
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"finsim/internal/doris"
	"finsim/internal/sim"
	"finsim/internal/sink"

	"github.com/shopspring/decimal"
)

//go:embed static
var static embed.FS

const BasePath = "/finsim"

// Book is the running simulator and its loader. A reset detaches it, wipes Doris
// and attaches a fresh one; handlers answer 503 in between.
type Book struct {
	Engine *sim.Engine
	Sink   *sink.Sink
}

type Server struct {
	book     atomic.Pointer[Book]
	db       *sql.DB
	database string
	dir      string
	stockDir string
	mux      *http.ServeMux
	resetFn  func() error // set by main: stop trading, wipe Doris, start a fresh book
}

func New(engine *sim.Engine, db *sql.DB, snk *sink.Sink, database, dir, stockDir string) *Server {
	s := &Server{db: db, database: database, dir: dir, stockDir: stockDir, mux: http.NewServeMux()}
	s.Attach(engine, snk)
	sub, _ := fs.Sub(static, "static")
	app := http.NewServeMux()
	app.Handle("/", http.FileServer(http.FS(sub)))
	app.HandleFunc("/api/live", s.handleLive)
	app.HandleFunc("/api/stocks", s.handleStocks)
	app.HandleFunc("/api/pnl", s.handlePnl)
	app.HandleFunc("/api/positions", s.handlePositions)
	app.HandleFunc("/api/sectors", s.handleSectors)
	app.HandleFunc("/api/nav", s.handleNav)
	app.HandleFunc("/api/tape", s.handleTape)
	app.HandleFunc("/api/blocks", s.handleBlocks)
	app.HandleFunc("/api/mv", s.handleMV)
	app.HandleFunc("/api/symbol/", s.handleSymbol)
	app.HandleFunc("/api/config", s.handleConfig)
	app.HandleFunc("/api/cashout", s.handleCashout)
	app.HandleFunc("/api/cashout/forget", s.handleCashoutForget)
	app.HandleFunc("/api/topup", s.handleTopup)
	app.HandleFunc("/api/reset", s.handleReset)
	app.HandleFunc("/admin", s.handleAdmin)
	app.HandleFunc("/admin/", s.handleAdmin)
	s.mux.Handle(BasePath+"/", http.StripPrefix(BasePath, app))
	return s
}

// ListenAndServe serves the dashboard under BasePath (/finsim/), which is what
// the reverse proxy forwards unchanged. / and /finsim redirect there so a bare
// host URL still lands on the dashboard.
func (s *Server) ListenAndServe(addr string) error {
	s.mux.Handle("/", http.RedirectHandler(BasePath+"/", http.StatusFound))
	srv := &http.Server{Addr: addr, Handler: s.mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// ---- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("web: encode: %v", err)
	}
}

func writeBadRequest(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func writeErr(w http.ResponseWriter, err error) {
	log.Printf("web: %v", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func intParam(r *http.Request, name string, def, min, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// query runs SQL and returns rows as maps. Numbers come back as strings
// (Doris DECIMALs must not pass through float64); NULL becomes nil.
func (s *Server) query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("%w\n%s", err, q)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if vals[i] == nil {
				m[c] = nil
			} else {
				m[c] = string(vals[i])
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func utcAgo(d time.Duration) string {
	return time.Now().UTC().Add(-d).Format("2006-01-02 15:04:05")
}

// ---- handlers --------------------------------------------------------------

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	b, ok := s.current(w)
	if !ok {
		return
	}
	writeJSON(w, map[string]any{"live": b.Engine.Live(), "sink": b.Sink.Stats()})
}

func (s *Server) handleStocks(w http.ResponseWriter, r *http.Request) {
	b, ok := s.current(w)
	if !ok {
		return
	}
	writeJSON(w, b.Engine.Stocks(intParam(r, "history", 0, 0, 300)))
}

func (s *Server) handlePnl(w http.ResponseWriter, r *http.Request) {
	rows, err := s.query(r.Context(), "SELECT * FROM v_pnl")
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(rows) == 0 {
		writeJSON(w, nil)
		return
	}
	writeJSON(w, rows[0])
}

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.query(r.Context(), "SELECT * FROM v_positions ORDER BY market_value DESC LIMIT "+strconv.Itoa(intParam(r, "limit", 200, 1, 1000)))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleSectors(w http.ResponseWriter, r *http.Request) {
	rows, err := s.query(r.Context(), "SELECT * FROM v_sectors ORDER BY market_value DESC")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleNav(w http.ResponseWriter, r *http.Request) {
	minutes := intParam(r, "minutes", 15, 1, 24*60)
	since := utcAgo(time.Duration(minutes) * time.Minute)
	// Beyond 30 minutes, sample every 10th second to keep the payload small.
	q := "SELECT ts, cash, equity, nav, pnl, trade_cost_total, realized_pnl_total, cash_ratio, buy_chance, calibrated FROM book_snapshots WHERE ts >= '" + since + "'"
	if minutes > 30 {
		q += " AND SECOND(ts) % 10 = 0"
	}
	q += " ORDER BY ts"
	snaps, err := s.query(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}
	cals, err := s.query(r.Context(), "SELECT * FROM calibrations WHERE ts >= '"+since+"' ORDER BY ts")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"snapshots": snaps, "calibrations": cals})
}

func (s *Server) handleTape(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 50, 1, 500)
	rows, err := s.query(r.Context(), "SELECT ts, trade_id, block_id, slice_no, symbol, side, qty, price, notional, trade_cost, realized_pnl, perceived_pos FROM trades WHERE is_seed = 0 AND ts >= '"+utcAgo(10*time.Minute)+"' ORDER BY ts DESC, trade_id DESC LIMIT "+strconv.Itoa(limit))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 20, 1, 200)
	rows, err := s.query(r.Context(), "SELECT * FROM v_block_shortfall WHERE started >= '"+utcAgo(60*time.Minute)+"' ORDER BY started DESC LIMIT "+strconv.Itoa(limit))
	if err != nil {
		writeErr(w, err)
		return
	}
	var active any
	if b := s.book.Load(); b != nil {
		active = b.Engine.Live().ActiveBlocks
	}
	writeJSON(w, map[string]any{"recent": rows, "active": active})
}

// handleMV reports the latest refresh task per materialized view.
func (s *Server) handleMV(w http.ResponseWriter, r *http.Request) {
	rows, err := s.query(r.Context(), "SELECT MvName, Status, StartTime, FinishTime, DurationMs, ErrorMsg, RefreshMode FROM tasks('type'='mv') WHERE MvDatabaseName = '"+s.database+"' ORDER BY CreateTime DESC LIMIT 60")
	if err != nil {
		writeErr(w, err)
		return
	}
	latest := map[string]map[string]any{}
	order := []string{}
	for _, row := range rows {
		name, _ := row["MvName"].(string)
		if _, seen := latest[name]; !seen {
			latest[name] = row
			order = append(order, name)
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, n := range order {
		out = append(out, latest[n])
	}
	// Doris reports task times in the session time zone; expose it so the UI can convert.
	tz, _ := s.query(r.Context(), "SELECT @@time_zone AS tz, NOW() AS now")
	resp := map[string]any{"views": out}
	if len(tz) > 0 {
		resp["doris_time_zone"] = tz[0]["tz"]
		resp["doris_now"] = tz[0]["now"]
	}
	writeJSON(w, resp)
}

func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) {
	sym := strings.ToUpper(strings.TrimPrefix(r.URL.Path, "/api/symbol/"))
	if sym == "" || strings.ContainsAny(sym, "'\";\\ ") {
		http.NotFound(w, r)
		return
	}
	minutes := intParam(r, "minutes", 30, 1, 24*60)
	var live *sim.LiveStock
	if b := s.book.Load(); b != nil {
		live = b.Engine.Stock(sym, 300)
	}
	prices, err := s.query(r.Context(), "SELECT ts, price, real_pos, perceived_pos, dir FROM prices WHERE symbol = '"+sym+"' AND ts >= '"+utcAgo(time.Duration(minutes)*time.Minute)+"' ORDER BY ts")
	if err != nil {
		writeErr(w, err)
		return
	}
	hist, err := s.query(r.Context(), "SELECT * FROM v_position_history WHERE symbol = '"+sym+"' ORDER BY minute")
	if err != nil {
		writeErr(w, err)
		return
	}
	trades, err := s.query(r.Context(), "SELECT ts, trade_id, block_id, slice_no, side, qty, price, notional, trade_cost, realized_pnl, avg_cost_after, perceived_pos, is_seed FROM trades WHERE symbol = '"+sym+"' AND ts >= '"+utcAgo(time.Duration(minutes)*time.Minute)+"' ORDER BY ts DESC LIMIT 200")
	if err != nil {
		writeErr(w, err)
		return
	}
	pos, err := s.query(r.Context(), "SELECT * FROM v_positions WHERE symbol = '"+sym+"'")
	if err != nil {
		writeErr(w, err)
		return
	}
	var position map[string]any
	if len(pos) > 0 {
		position = pos[0]
	}
	cfgText, _ := os.ReadFile(filepath.Join(s.stockDir, sym+".toml"))
	writeJSON(w, map[string]any{
		"symbol": sym, "live": live, "prices": prices, "position_history": hist, "trades": trades,
		"position": position, "config": string(cfgText), "config_path": filepath.Join("stocks", sym+".toml"),
	})
}

// handleCashout: POST {"amount": "1.5T"} (or "200B", "50M", "1000000") transfers
// that much cash out of the book. Responds with the event and the new cash.
func (s *Server) handleCashout(w http.ResponseWriter, r *http.Request) {
	if b, ok := s.current(w); ok {
		s.cashMove(w, r, b.Engine.Cashout)
	}
}

// handleTopup adds cash to the book (always allowed).
func (s *Server) handleTopup(w http.ResponseWriter, r *http.Request) {
	if b, ok := s.current(w); ok {
		s.cashMove(w, r, b.Engine.Topup)
	}
}

func (s *Server) cashMove(w http.ResponseWriter, r *http.Request, move func(decimal.Decimal) (sim.CashoutEvent, error)) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Amount string `json:"amount"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeBadRequest(w, fmt.Errorf("bad request: %w", err))
		return
	}
	amount, err := ParseAmount(req.Amount)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	ev, err := move(amount)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, ev)
}

// ParseAmount reads "1.5T", "200B", "50M", "10K" or a plain number of dollars.
// Suffixes are case-insensitive; "$", commas and spaces are ignored.
func ParseAmount(text string) (decimal.Decimal, error) {
	t := strings.ToUpper(strings.NewReplacer("$", "", ",", "", " ", "", "_", "").Replace(text))
	if t == "" {
		return decimal.Zero, fmt.Errorf("amount is empty")
	}
	mult := decimal.NewFromInt(1)
	switch t[len(t)-1] {
	case 'T':
		mult = decimal.NewFromInt(1_000_000_000_000)
	case 'B':
		mult = decimal.NewFromInt(1_000_000_000)
	case 'M':
		mult = decimal.NewFromInt(1_000_000)
	case 'K':
		mult = decimal.NewFromInt(1_000)
	}
	if !mult.Equal(decimal.NewFromInt(1)) {
		t = t[:len(t)-1]
	}
	v, err := decimal.NewFromString(t)
	if err != nil {
		return decimal.Zero, fmt.Errorf("amount %q: use a number with an optional T, B, M or K suffix", text)
	}
	v = v.Mul(mult).Round(2)
	if v.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf("amount must be positive")
	}
	return v, nil
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	b, ok := s.current(w)
	if !ok {
		return
	}
	strat, _ := os.ReadFile(filepath.Join(s.dir, "strategy.toml"))
	mkt, _ := os.ReadFile(filepath.Join(s.dir, "market.toml"))
	writeJSON(w, map[string]any{
		"status": b.Engine.Status(), "strategy": b.Engine.Strategy(), "market": b.Engine.Market(),
		"strategy_toml": string(strat), "market_toml": string(mkt), "symbols": b.Engine.Symbols(),
	})
}

// handleAdmin serves the admin page (cash withdrawals, forget withdrawals).
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" { // "/admin/" and anything below: one canonical URL
		http.Redirect(w, r, BasePath+"/admin", http.StatusMovedPermanently)
		return
	}
	page, err := static.ReadFile("static/admin.html")
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}

// handleCashoutForget writes off every cashout made so far: P&L stops counting
// them as profit. The amount is persisted to Doris (book_meta) first, so v_pnl
// and a restart agree with memory; cash, NAV and positions do not change.
func (s *Server) handleCashoutForget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.current(w)
	if !ok {
		return
	}
	total := b.Engine.PaidOutTotal()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := doris.SetPaidOutForgotten(ctx, s.db, total); err != nil {
		writeErr(w, fmt.Errorf("forget cashouts: %w", err))
		return
	}
	newly := b.Engine.ForgetCashouts(total)
	writeJSON(w, map[string]any{"forgotten": newly, "forgotten_total": total, "live": b.Engine.Live()})
}

// Attach makes engine and snk the book served by the API.
func (s *Server) Attach(engine *sim.Engine, snk *sink.Sink) {
	if engine == nil {
		s.book.Store(nil)
		return
	}
	s.book.Store(&Book{Engine: engine, Sink: snk})
}

// Detach removes the book: handlers that need it answer 503 "resetting".
func (s *Server) Detach() { s.book.Store(nil) }

// SetResetFunc installs the reset the admin page triggers.
func (s *Server) SetResetFunc(fn func() error) { s.resetFn = fn }

// current returns the attached book, or writes a 503 and returns false.
func (s *Server) current(w http.ResponseWriter) (*Book, bool) {
	b := s.book.Load()
	if b == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "the book is being reset", "resetting": true})
		return nil, false
	}
	return b, true
}

// handleReset wipes the book: trading stops, queued loads drain, every demo
// table, materialized view and view in Doris is dropped and recreated, and a
// fresh book is seeded from strategy.toml. The reply comes back as soon as the
// reset has started; /api/live answers 503 until the new book is attached.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || req.Confirm != "RESET" {
		writeBadRequest(w, fmt.Errorf(`send {"confirm":"RESET"} to wipe the book`))
		return
	}
	if s.resetFn == nil {
		writeErr(w, fmt.Errorf("reset is not available in this process"))
		return
	}
	if err := s.resetFn(); err != nil {
		writeBadRequest(w, err)
		return
	}
	writeJSON(w, map[string]any{"status": "resetting"})
}

// Engine returns the attached engine, or nil while a reset is in progress.
func (s *Server) Engine() *sim.Engine {
	if b := s.book.Load(); b != nil {
		return b.Engine
	}
	return nil
}
