package sim

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"finsim/internal/config"

	"github.com/shopspring/decimal"
)

// Engine owns all simulator state under one mutex. Throughput is ~150 events/s,
// so a single lock is simpler and safer than fine-grained locking.
type Engine struct {
	mu sync.Mutex

	strategy atomic.Pointer[config.Strategy]
	market   atomic.Pointer[config.Market]
	macro    macro // market-wide multiplier, guarded by mu

	stocks    map[string]*stock
	positions map[string]*Position
	blocks    map[int64]*block
	cash      decimal.Decimal
	buyChance float64

	// optimalChance is the share of trades that follow buy-low / sell-high. It is
	// regulated from NAV by the calibrator (§3.7): rich books trade worse, poor
	// books trade better, so the simulation runs forever without blowing up.
	optimalChance float64
	navRegime     string

	startingCash   decimal.Decimal
	tradeCostTotal decimal.Decimal
	realizedTotal  decimal.Decimal

	nextTradeID int64
	nextBlockID int64

	paidOut       decimal.Decimal // cash transferred out of the book so far (all time; drives cash)
	forgotten     decimal.Decimal // part of paidOut written off: no longer counted as profit
	nextCashoutID int64
	cashouts      []CashoutEvent // recent, for the dashboard

	emit Emitter
	rng  *rand.Rand

	// counters for rates
	tradeCount atomic.Int64
	skipCount  atomic.Int64
	tickCount  atomic.Int64
	rowCount   atomic.Int64

	lastCalibration *CalibrationRow
	extremes        []ExtremeEvent
	calibratedFlag  bool

	status Status
}

// Status is what the dashboard shows about configuration and reloads.
type Status struct {
	StartedAt      time.Time         `json:"started_at"`
	LastReloadAt   time.Time         `json:"last_reload_at"`
	LastReloadFile string            `json:"last_reload_file"`
	Errors         map[string]string `json:"errors"` // file -> error
	Seeded         bool              `json:"seeded"`
	Resumed        bool              `json:"resumed"`
}

type Position struct {
	Qty      int64
	Reserved int64 // quantity held for active sell blocks
	AvgCost  decimal.Decimal
}

func (p *Position) available() int64 { return p.Qty - p.Reserved }

func New(strategy *config.Strategy, market *config.Market, stocks []*config.Stock, emit Emitter) *Engine {
	e := &Engine{
		stocks:        map[string]*stock{},
		positions:     map[string]*Position{},
		blocks:        map[int64]*block{},
		emit:          emit,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
		nextTradeID:   1,
		nextBlockID:   1,
		nextCashoutID: 1,
		startingCash:  decimal.NewFromFloat(strategy.Capital.StartingCashAmount),
		buyChance:     strategy.Trading.BuyChance,
		optimalChance: strategy.Trading.OptimalTradeChance,
		navRegime:     "normal",
		status:        Status{StartedAt: time.Now().UTC(), Errors: map[string]string{}},
	}
	e.cash = e.startingCash
	e.strategy.Store(strategy)
	e.market.Store(market)
	e.macro = newMacro(e.rng)
	for _, s := range stocks {
		e.stocks[s.Symbol] = newStock(s, e.rng)
	}
	return e
}

// ---- hot reload -----------------------------------------------------------

func (e *Engine) ReloadStrategy(s *config.Strategy) {
	old := e.strategy.Load()
	e.strategy.Store(s)
	e.mu.Lock()
	e.navRegime = "normal" // a config change resets the latch
	e.optimalChance = e.regulatedOptimal(s, e.cash.Add(e.equity()))
	if old == nil || old.Trading.BuyChance != s.Trading.BuyChance {
		e.buyChance = clamp(s.Trading.BuyChance, s.Trading.BuyChanceMin, s.Trading.BuyChanceMax)
	} else {
		e.buyChance = clamp(e.buyChance, s.Trading.BuyChanceMin, s.Trading.BuyChanceMax)
	}
	e.mu.Unlock()
	e.reloaded("strategy.toml")
}

// ReloadMarket swaps the macro config; the multiplier walks to the new range
// or pin on the following ticks (see macro.tick).
func (e *Engine) ReloadMarket(m *config.Market) {
	e.market.Store(m)
	e.reloaded("market.toml")
}

func (e *Engine) ReloadStock(c *config.Stock) {
	e.mu.Lock()
	if s, ok := e.stocks[c.Symbol]; ok {
		s.applyConfig(c, e.rng)
		s.delisted = false
	} else {
		e.stocks[c.Symbol] = newStock(c, e.rng)
	}
	e.mu.Unlock()
	e.reloaded("stocks/" + c.Symbol + ".toml")
}

func (e *Engine) DelistStock(symbol string) {
	e.mu.Lock()
	if s, ok := e.stocks[symbol]; ok {
		s.delisted = true
	}
	e.mu.Unlock()
	e.reloaded("stocks/" + symbol + ".toml (deleted)")
}

func (e *Engine) ReloadError(path string, err error) {
	e.mu.Lock()
	e.status.Errors[path] = err.Error()
	e.mu.Unlock()
}

func (e *Engine) reloaded(name string) {
	e.mu.Lock()
	e.status.LastReloadAt = time.Now().UTC()
	e.status.LastReloadFile = name
	delete(e.status.Errors, name)
	for k := range e.status.Errors { // a successful load of the same file clears its error
		if k == name || (len(k) > len(name) && k[len(k)-len(name):] == name) {
			delete(e.status.Errors, k)
		}
	}
	e.mu.Unlock()
}

// ---- restore (resume from Doris) -------------------------------------------

type RestoredPosition struct {
	Qty     int64
	AvgCost decimal.Decimal
}

func (e *Engine) Restore(prices map[string]decimal.Decimal, positions map[string]RestoredPosition, cash, tradeCost, realized, paidOut, forgotten decimal.Decimal, trades, maxTradeID, maxBlockID, maxCashoutID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tradeCount.Store(trades)
	for sym, p := range prices {
		if s, ok := e.stocks[sym]; ok {
			s.setPrice(p)
		}
	}
	for sym, rp := range positions {
		if rp.Qty > 0 {
			e.positions[sym] = &Position{Qty: rp.Qty, AvgCost: rp.AvgCost}
		}
	}
	e.cash = cash
	e.tradeCostTotal = tradeCost
	e.realizedTotal = realized
	e.nextTradeID = maxTradeID + 1
	e.nextBlockID = maxBlockID + 1
	e.paidOut = paidOut
	e.forgotten = forgotten
	e.nextCashoutID = maxCashoutID + 1
	e.status.Resumed = true
}

// ---- loops -----------------------------------------------------------------

// Run starts every loop and blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	loops := []func(context.Context){e.priceLoop, e.tradeLoop, e.blockLoop, e.rebalanceLoop, e.snapshotLoop, e.extremeLoop}
	for _, l := range loops {
		wg.Add(1)
		go func(l func(context.Context)) {
			defer wg.Done()
			l(ctx)
		}(l)
	}
	wg.Wait()
}

func (e *Engine) priceLoop(ctx context.Context) {
	for {
		iv := time.Duration(e.market.Load().Price.TickIntervalSeconds) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
			e.tickAll(time.Now().UTC())
		}
	}
}

func (e *Engine) tradeLoop(ctx context.Context) {
	// Trades are spread evenly across each second so the tape looks continuous.
	for {
		tps := e.strategy.Load().Trading.TradesPerSecond
		iv := time.Second / time.Duration(tps)
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
			e.oneTrade(time.Now().UTC())
		}
	}
}

func (e *Engine) blockLoop(ctx context.Context) {
	for {
		iv := time.Duration(e.strategy.Load().Blocks.BlockTradeSliceIntervalSeconds) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
			e.advanceBlocks(time.Now().UTC())
		}
	}
}

func (e *Engine) rebalanceLoop(ctx context.Context) {
	for {
		iv := time.Duration(e.strategy.Load().Capital.RebalanceIntervalSeconds) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
			e.calibrate(time.Now().UTC())
		}
	}
}

func (e *Engine) snapshotLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			e.snapshot(now.UTC())
		}
	}
}

// ---- helpers ---------------------------------------------------------------

func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.status
	st.Errors = map[string]string{}
	for k, v := range e.status.Errors {
		st.Errors[k] = v
	}
	return st
}

func (e *Engine) Strategy() *config.Strategy { return e.strategy.Load() }
func (e *Engine) Market() *config.Market     { return e.market.Load() }

// Symbols returns the listed symbols sorted.
func (e *Engine) Symbols() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.stocks))
	for sym, s := range e.stocks {
		if !s.delisted {
			out = append(out, sym)
		}
	}
	sort.Strings(out)
	return out
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (e *Engine) logf(format string, args ...any) { log.Printf("sim: "+format, args...) }

// CashoutEvent is one cash movement, kept for the dashboard: Amount is positive
// for a withdrawal and negative for a top-up.
type CashoutEvent struct {
	TS        time.Time       `json:"ts"`
	CashoutID int64           `json:"cashout_id"`
	Amount    decimal.Decimal `json:"amount"`
	CashAfter decimal.Decimal `json:"cash_after"`
	NavAfter  decimal.Decimal `json:"nav_after"`
}

const maxCashoutEvents = 20

// Cashout transfers amount out of the book's cash, like a dividend: cash and
// NAV drop, P&L does not (it counts paid-out cash as realised until the cash
// movements are forgotten, see ForgetCashouts). The row is written to Doris so
// a resume and v_pnl agree with memory.
//
// The amount may exceed the cash on hand: cash then goes negative (an
// overdraft), which blocks every buy until sells or a top-up bring it back
// above zero. Positions are never sold to fund a cashout directly. The one
// limit is NAV: the book cannot pay out more than it is worth.
func (e *Engine) Cashout(amount decimal.Decimal) (CashoutEvent, error) {
	if amount.Sign() <= 0 {
		return CashoutEvent{}, fmt.Errorf("amount must be positive")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if nav := e.cash.Add(e.equity()); amount.GreaterThan(nav) {
		return CashoutEvent{}, fmt.Errorf("at most the NAV of %s can be withdrawn (cash %s + positions)", nav.StringFixed(2), e.cash.StringFixed(2))
	}
	return e.cashFlow(amount), nil
}

// Topup adds amount to the book's cash, a capital injection: cash and NAV rise,
// P&L does not. It is always allowed. It is recorded as a negative cashout, so
// "paid out" is the net of withdrawals and top-ups everywhere.
func (e *Engine) Topup(amount decimal.Decimal) (CashoutEvent, error) {
	if amount.Sign() <= 0 {
		return CashoutEvent{}, fmt.Errorf("amount must be positive")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cashFlow(amount.Neg()), nil
}

// cashFlow moves out (positive) or in (negative) of the book. Caller holds mu.
func (e *Engine) cashFlow(out decimal.Decimal) CashoutEvent {
	now := time.Now().UTC()
	navBefore := e.cash.Add(e.equity())
	row := CashoutRow{TS: DorisTime(now), CashoutID: e.nextCashoutID, Amount: out, CashBefore: e.cash, NavBefore: navBefore}
	e.nextCashoutID++
	e.cash = e.cash.Sub(out)
	e.paidOut = e.paidOut.Add(out)
	ev := CashoutEvent{TS: now, CashoutID: row.CashoutID, Amount: out, CashAfter: e.cash, NavAfter: navBefore.Sub(out)}
	e.cashouts = append(e.cashouts, ev)
	if len(e.cashouts) > maxCashoutEvents {
		e.cashouts = e.cashouts[len(e.cashouts)-maxCashoutEvents:]
	}
	if e.emit != nil {
		e.emit.Emit(TableCashouts, row)
	}
	kind := "cashout"
	if out.Sign() < 0 {
		kind = "top-up"
	}
	e.logf("%s #%d %s: cash %s -> %s", kind, row.CashoutID, out.Abs().StringFixed(0), row.CashBefore.StringFixed(0), e.cash.StringFixed(0))
	return ev
}

// PaidOutTotal is the net cash ever moved out of the book (withdrawals minus
// top-ups), forgotten or not.
func (e *Engine) PaidOutTotal() decimal.Decimal {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paidOut
}

// ForgetCashouts writes off every cash movement up to total (a value previously
// read from PaidOutTotal and already persisted by the caller): P&L stops
// carrying them and becomes NAV + (net paid out since) − starting cash. Cash,
// NAV and positions are untouched. Returns the amount newly forgotten, which is
// by how much P&L drops (negative when top-ups outweighed withdrawals).
func (e *Engine) ForgetCashouts(total decimal.Decimal) decimal.Decimal {
	e.mu.Lock()
	defer e.mu.Unlock()
	newly := total.Sub(e.forgotten)
	if newly.IsZero() {
		return zero
	}
	e.forgotten = total
	e.cashouts = nil
	e.logf("forgot %s of net cash movements: P&L no longer counts them (total forgotten %s)", newly.StringFixed(0), total.StringFixed(0))
	return newly
}

// pnl is NAV plus the paid-out cash that is still remembered, minus starting
// cash: a dividend is not a loss, until it is forgotten.
func (e *Engine) pnl(nav decimal.Decimal) decimal.Decimal {
	return nav.Add(e.paidOut).Sub(e.forgotten).Sub(e.startingCash)
}

// AllocTradeIDs reserves n consecutive trade ids for rows written outside the
// trade loop (carry-forward rows when old partitions are folded).
func (e *Engine) AllocTradeIDs(n int64) (first int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	first = e.nextTradeID
	e.nextTradeID += n
	return first
}
