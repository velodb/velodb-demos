package sim

import (
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// Live is the memory-side view served at /api/live.
type Live struct {
	TS              time.Time       `json:"ts"`
	Cash            decimal.Decimal `json:"cash"`
	Equity          decimal.Decimal `json:"equity"`
	Nav             decimal.Decimal `json:"nav"`
	Pnl             decimal.Decimal `json:"pnl"` // nav + paid_out - starting_cash
	StartingCash    decimal.Decimal `json:"starting_cash"`
	PaidOut         decimal.Decimal `json:"paid_out"`           // still counted in P&L (total minus forgotten)
	PaidOutTotal    decimal.Decimal `json:"paid_out_total"`     // every cashout ever
	PaidOutForgot   decimal.Decimal `json:"paid_out_forgotten"` // written off by "forget withdrawals"
	BuysBlocked     bool            `json:"buys_blocked"`       // cash <= 0
	Cashouts        []CashoutEvent  `json:"cashouts"`
	TradeCostTotal  decimal.Decimal `json:"trade_cost_total"`
	RealizedTotal   decimal.Decimal `json:"realized_pnl_total"`
	CashRatio       float64         `json:"cash_ratio"`
	TargetCashRatio float64         `json:"target_cash_ratio"`
	BuyChance       float64         `json:"buy_chance"`
	OptimalChance   float64         `json:"optimal_trade_chance"`
	NavRegime       string          `json:"nav_regime"` // "poor", "normal", "rich"
	LastCalibration *CalibrationRow `json:"last_calibration"`
	Rates           Rates           `json:"rates"`
	PositionCount   int             `json:"position_count"`
	StockCount      int             `json:"stock_count"`
	ActiveBlocks    []LiveBlock     `json:"active_blocks"`
	ExtremeEvents   []ExtremeEvent  `json:"extreme_events"`
	Status          Status          `json:"status"`
	Strategy        any             `json:"strategy"`
	Market          any             `json:"market"`
	Macro           LiveMacro       `json:"macro"`
}

type Rates struct {
	TradesPerSec float64 `json:"trades_per_sec"`
	SkipsPerSec  float64 `json:"skips_per_sec"`
	TicksPerSec  float64 `json:"ticks_per_sec"`
	RowsPerSec   float64 `json:"rows_per_sec"`
	TradesTotal  int64   `json:"trades_total"`
	SkipsTotal   int64   `json:"skips_total"`
	RowsTotal    int64   `json:"rows_total"`
}

type LiveBlock struct {
	ID            int64           `json:"id"`
	Symbol        string          `json:"symbol"`
	Side          string          `json:"side"`
	SlicesDone    int             `json:"slices_done"`
	SlicesTotal   int             `json:"slices_total"`
	DecisionPrice decimal.Decimal `json:"decision_price"`
	CurrentPrice  decimal.Decimal `json:"current_price"`
	FilledQty     int64           `json:"filled_qty"`
	FilledNotion  decimal.Decimal `json:"filled_notional"`
	PlannedNotion decimal.Decimal `json:"planned_notional"`
	ShortfallBps  float64         `json:"shortfall_bps"`
	StartedAt     time.Time       `json:"started_at"`
}

// LiveMacro is the market-wide multiplier right now.
type LiveMacro struct {
	Multiplier float64 `json:"multiplier"`
	Dir        int     `json:"dir"`
	Pinned     bool    `json:"pinned"`
	Target     float64 `json:"target"` // where it is walking to; 0 when roaming inside the range
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
}

// LiveStock is one symbol as the simulator sees it right now.
type LiveStock struct {
	Symbol          string          `json:"symbol"`
	Name            string          `json:"name"`
	Sector          string          `json:"sector"`
	Weight          float64         `json:"weight"`
	Price           decimal.Decimal `json:"price"`
	Dir             int             `json:"dir"`
	RealMin         float64         `json:"real_min"`
	RealMax         float64         `json:"real_max"`
	PerceivedMin    float64         `json:"perceived_min"`
	PerceivedMax    float64         `json:"perceived_max"`
	PerceivedIsReal bool            `json:"perceived_is_real"`
	RealPos         float64         `json:"real_pos"`
	PerceivedPos    float64         `json:"perceived_pos"`
	Qty             int64           `json:"qty"`
	AvgCost         decimal.Decimal `json:"avg_cost"`
	MarketValue     decimal.Decimal `json:"market_value"`
	UnrealizedPnl   decimal.Decimal `json:"unrealized_pnl"`
	TargetWeightPct float64         `json:"target_weight_pct"` // weight / Σweight
	WeightPct       float64         `json:"weight_pct"`        // market value / equity
	Deviation       float64         `json:"deviation"`         // target value − market value, dollars
	Locked          bool            `json:"locked"`            // a block is working this symbol
	Delisted        bool            `json:"delisted"`
	History         []float64       `json:"history,omitempty"`
	TrendStep       float64         `json:"trend_step_percent"`
	Noise           float64         `json:"noise_percent"`
	FlipChance      float64         `json:"direction_flip_chance"`
	PinnedPrice     float64         `json:"pinned_price"` // 0 = not pinned
	BandPercent     float64         `json:"band_percent"` // real band = anchor ± this when derived
	RealIsDerived   bool            `json:"real_is_derived"`
}

// rate tracker: sampled once per second by the caller of Rates()
type rateTracker struct {
	mu                         sync.Mutex
	lastAt                     time.Time
	lastTrades, lastTicks      int64
	lastRows, lastSkips        int64
	trades, ticks, rows, skips float64
}

var rates rateTracker

func (e *Engine) sampleRates() Rates {
	rates.mu.Lock()
	defer rates.mu.Unlock()
	now := time.Now()
	t, k, r, sk := e.tradeCount.Load(), e.tickCount.Load(), e.rowCount.Load(), e.skipCount.Load()
	if !rates.lastAt.IsZero() {
		dt := now.Sub(rates.lastAt).Seconds()
		if dt >= 0.9 {
			rates.trades = float64(t-rates.lastTrades) / dt
			rates.ticks = float64(k-rates.lastTicks) / dt
			rates.rows = float64(r-rates.lastRows) / dt
			rates.skips = float64(sk-rates.lastSkips) / dt
			rates.lastAt, rates.lastTrades, rates.lastTicks, rates.lastRows, rates.lastSkips = now, t, k, r, sk
		}
	} else {
		rates.lastAt, rates.lastTrades, rates.lastTicks, rates.lastRows, rates.lastSkips = now, t, k, r, sk
	}
	return Rates{TradesPerSec: rates.trades, SkipsPerSec: rates.skips, TicksPerSec: rates.ticks, RowsPerSec: rates.rows, TradesTotal: t, SkipsTotal: sk, RowsTotal: r}
}

func (e *Engine) Live() Live {
	st := e.strategy.Load()
	rt := e.sampleRates()
	e.mu.Lock()
	defer e.mu.Unlock()
	eq := e.equity()
	nav := e.cash.Add(eq)
	l := Live{
		TS: time.Now().UTC(), Cash: e.cash, Equity: eq, Nav: nav, Pnl: e.pnl(nav),
		StartingCash: e.startingCash, PaidOut: e.paidOut.Sub(e.forgotten), PaidOutTotal: e.paidOut, PaidOutForgot: e.forgotten, BuysBlocked: e.cash.Sign() <= 0, Cashouts: append([]CashoutEvent(nil), e.cashouts...),
		TradeCostTotal: e.tradeCostTotal, RealizedTotal: e.realizedTotal,
		CashRatio: e.cashRatio(), TargetCashRatio: st.Capital.TargetCashRatio, BuyChance: e.buyChance, OptimalChance: e.optimalChance, NavRegime: e.navRegime,
		LastCalibration: e.lastCalibration, Rates: rt, StockCount: 0, Status: e.status,
		Strategy: st, Market: e.market.Load(),
	}
	mc := e.market.Load().Macro
	l.Macro = LiveMacro{Multiplier: e.macro.mult, Dir: e.macro.dir, Pinned: e.macro.pinned(mc), Target: e.macro.target(mc), Min: mc.MinMultiplier, Max: mc.MaxMultiplier}
	for _, s := range e.stocks {
		if !s.delisted {
			l.StockCount++
		}
	}
	for _, p := range e.positions {
		if p.Qty > 0 {
			l.PositionCount++
		}
	}
	l.ActiveBlocks = make([]LiveBlock, 0, len(e.blocks))
	for _, b := range e.blocks {
		s := e.stocks[b.symbol]
		lb := LiveBlock{
			ID: b.id, Symbol: b.symbol, Side: b.side, SlicesDone: b.slicesDone, SlicesTotal: b.slicesTotal,
			DecisionPrice: b.decisionPrice, FilledQty: b.filledQty, FilledNotion: b.filledNotion,
			PlannedNotion: b.total, StartedAt: b.startedAt,
		}
		if s != nil {
			lb.CurrentPrice = s.quote
		}
		if b.filledQty > 0 {
			avg := b.filledNotion.Div(decimal.NewFromInt(b.filledQty))
			sf, _ := avg.Sub(b.decisionPrice).Div(b.decisionPrice).Float64()
			if b.side == SideSell {
				sf = -sf
			}
			lb.ShortfallBps = sf * 10000
		}
		l.ActiveBlocks = append(l.ActiveBlocks, lb)
	}
	sort.Slice(l.ActiveBlocks, func(i, j int) bool { return l.ActiveBlocks[i].ID < l.ActiveBlocks[j].ID })
	l.ExtremeEvents = append([]ExtremeEvent(nil), e.extremes...)
	return l
}

// Stocks returns every symbol with live price, bands and position. history=n adds n recent quotes.
func (e *Engine) Stocks(history int) []LiveStock {
	e.mu.Lock()
	defer e.mu.Unlock()
	shares, base := e.targetShares()
	out := make([]LiveStock, 0, len(e.stocks))
	for sym, s := range e.stocks {
		ls := LiveStock{
			Symbol: sym, Name: s.cfg.Name, Sector: s.cfg.Sector, Weight: s.cfg.Weight, Price: s.quote, Dir: s.dir,
			RealMin: s.real.Min, RealMax: s.real.Max, PerceivedMin: s.perceived.Min, PerceivedMax: s.perceived.Max,
			PerceivedIsReal: s.cfg.PerceivedIsReal, RealPos: s.realPos(), PerceivedPos: s.perceivedPos(), Delisted: s.delisted,
			TrendStep: s.cfg.TrendStepPercent, Noise: s.cfg.NoisePercent, FlipChance: s.cfg.DirectionFlipChance, PinnedPrice: s.cfg.PinnedPrice, BandPercent: s.cfg.BandPercent, RealIsDerived: s.cfg.RealIsDerived,
		}
		ls.Locked = e.hasBlock(sym)
		ls.TargetWeightPct = shares[sym]
		ls.Deviation = e.deviation(sym, shares, base)
		if base > 0 {
			ls.WeightPct = (shares[sym]*base - ls.Deviation) / base
		}
		if p, ok := e.positions[sym]; ok && p.Qty > 0 {
			ls.Qty = p.Qty
			ls.AvgCost = p.AvgCost
			ls.MarketValue = s.quote.Mul(decimal.NewFromInt(p.Qty))
			ls.UnrealizedPnl = s.quote.Sub(p.AvgCost).Mul(decimal.NewFromInt(p.Qty)).Round(6)
		}
		if history > 0 {
			ls.History = s.History(history)
		}
		out = append(out, ls)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// Stock returns one symbol, or nil.
func (e *Engine) Stock(symbol string, history int) *LiveStock {
	for _, s := range e.Stocks(history) {
		if s.Symbol == symbol {
			return &s
		}
	}
	return nil
}
