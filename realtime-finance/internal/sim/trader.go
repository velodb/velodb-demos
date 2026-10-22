package sim

import (
	"math"
	"time"

	"finsim/internal/config"

	"github.com/shopspring/decimal"
)

// block is a worked order: the same ticket repeated once per slice interval.
type block struct {
	id            int64
	symbol        string
	side          string
	qtyPerSlice   int64
	extra         int64 // added to the last slice (remainder of an all-in order)
	slicesTotal   int
	slicesDone    int
	decisionPrice decimal.Decimal
	total         decimal.Decimal // planned notional
	filledQty     int64
	filledNotion  decimal.Decimal
	startedAt     time.Time
}

// oneTrade is one tick of the trade loop: decide side, symbol and size, then fill.
func (e *Engine) oneTrade(now time.Time) {
	st := e.strategy.Load()
	e.mu.Lock()
	defer e.mu.Unlock()

	side := SideSell
	if e.rng.Float64() < e.buyChance {
		side = SideBuy
	}
	optimal := e.rng.Float64() < e.optimalChance
	ticket := e.ticket()

	// Overdrawn (a cashout took more than the cash on hand): no buys until sells
	// bring cash back above zero. The turn is skipped, not converted to a sell.
	if side == SideBuy && e.cash.Sign() <= 0 {
		e.skipCount.Add(1)
		return
	}

	// No eligible symbol (all locked by working blocks, none on the right side of
	// its target weight, or none in the required half of the band): chicken out.
	var s *stock
	if side == SideSell {
		s = e.pickSell(optimal)
	} else {
		s = e.pickBuy(optimal)
	}
	if s == nil {
		e.skipCount.Add(1)
		return
	}

	qty := ticket.Div(s.quote).Floor().IntPart()
	if qty < 1 {
		qty = 1
	}
	if side == SideSell {
		avail := e.position(s.cfg.Symbol).available()
		if qty > avail {
			qty = avail
		}
		if qty <= 0 {
			return
		}
	}

	// Worked block?
	if st.Blocks.BlockTradeChance > 0 && e.rng.Float64() < st.Blocks.BlockTradeChance && st.Blocks.BlockTradeSlices > 1 && !e.hasBlock(s.cfg.Symbol) {
		e.startBlock(now, s, side, qty)
		return
	}
	if row := e.fill(now, s, side, qty, fillOpts{}); row != nil {
		e.emitTrade(row)
	}
}

func (e *Engine) emitTrade(row *TradeRow) {
	e.emit.Emit(TableTrades, *row)
	e.rowCount.Add(1)
}

// ticket draws a trade notional from [min, max], log-uniform by default.
func (e *Engine) ticket() decimal.Decimal {
	t := e.strategy.Load().Trading
	var v float64
	if t.TradeAmountDistribution == "uniform" || t.TradeMinAmount == t.TradeMaxAmount {
		v = t.TradeMinAmount + e.rng.Float64()*(t.TradeMaxAmount-t.TradeMinAmount)
	} else {
		lo, hi := math.Log(t.TradeMinAmount), math.Log(t.TradeMaxAmount)
		v = math.Exp(lo + e.rng.Float64()*(hi-lo))
	}
	return decimal.NewFromFloat(math.Round(v*100) / 100)
}

// targetShares returns each listed stock's target share of equity (weight / Σweight)
// and the equity base used to turn shares into dollars. With an empty book the
// base is the target equity (NAV × (1 − target cash ratio)) so seeding-free
// starts still know what "underweight" means. Caller holds mu.
func (e *Engine) targetShares() (map[string]float64, float64) {
	sumW := 0.0
	for _, s := range e.stocks {
		if !s.delisted && s.cfg.Weight > 0 {
			sumW += s.cfg.Weight
		}
	}
	shares := make(map[string]float64, len(e.stocks))
	if sumW <= 0 {
		return shares, 0
	}
	for sym, s := range e.stocks {
		if !s.delisted && s.cfg.Weight > 0 {
			shares[sym] = s.cfg.Weight / sumW
		}
	}
	eq, _ := e.equity().Float64()
	if eq <= 0 {
		nav, _ := e.cash.Float64()
		eq = nav * (1 - e.strategy.Load().Capital.TargetCashRatio)
	}
	return shares, eq
}

// deviation is target value − current value for one symbol (positive = underweight).
func (e *Engine) deviation(sym string, shares map[string]float64, base float64) float64 {
	cur := 0.0
	if p, ok := e.positions[sym]; ok && p.Qty > 0 {
		cur = e.stocks[sym].quoteF() * float64(p.Qty)
	}
	return shares[sym]*base - cur
}

// pickBuy chooses an unlocked stock to buy in the half of the perceived band the
// dice chose (optimal = lower half). With maintain_target_weight the candidates
// are further limited to underweight names and the pick is proportional to the
// dollars below target; without it the pick is uniform over the bucket.
// nil = no candidate: this turn is skipped.
func (e *Engine) pickBuy(optimal bool) *stock {
	maintain := e.strategy.Load().Trading.MaintainTargetWeight
	shares, base := e.targetShares()
	var cands []*stock
	var weights []float64
	for sym, s := range e.stocks {
		if s.delisted || s.cfg.Weight <= 0 || e.hasBlock(sym) {
			continue
		}
		pos := s.perceivedPos()
		if !((optimal && pos < 0.5) || (!optimal && pos >= 0.5)) {
			continue
		}
		if !maintain {
			cands = append(cands, s)
			weights = append(weights, 1)
		} else if dev := e.deviation(sym, shares, base); dev > 0 {
			cands = append(cands, s)
			weights = append(weights, dev)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	return cands[e.weightedIndex(weights)]
}

// pickSell chooses an unlocked holding to sell in the half of the perceived band
// the dice chose (optimal = upper half). With maintain_target_weight only
// overweight holdings qualify, proportional to the dollars above target; without
// it the pick is uniform over the bucket. nil = no candidate: this turn is skipped.
func (e *Engine) pickSell(optimal bool) *stock {
	maintain := e.strategy.Load().Trading.MaintainTargetWeight
	shares, base := e.targetShares()
	var cands []*stock
	var weights []float64
	for sym, p := range e.positions {
		if p.available() <= 0 || e.hasBlock(sym) {
			continue
		}
		s, ok := e.stocks[sym]
		if !ok {
			continue
		}
		pos := s.perceivedPos()
		if !((optimal && pos > 0.5) || (!optimal && pos <= 0.5)) {
			continue
		}
		if !maintain {
			cands = append(cands, s)
			weights = append(weights, 1)
		} else if dev := e.deviation(sym, shares, base); dev < 0 {
			cands = append(cands, s)
			weights = append(weights, -dev)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	return cands[e.weightedIndex(weights)]
}

func (e *Engine) weightedIndex(w []float64) int {
	total := 0.0
	for _, x := range w {
		total += x
	}
	if total <= 0 {
		return e.rng.Intn(len(w))
	}
	r := e.rng.Float64() * total
	for i, x := range w {
		r -= x
		if r <= 0 {
			return i
		}
	}
	return len(w) - 1
}

// ---- blocks ----------------------------------------------------------------

// hasBlock is the per-symbol strategy lock. While any worked order is active,
// no ordinary trade or second block may be started on that symbol, regardless
// of side.
func (e *Engine) hasBlock(symbol string) bool {
	for _, b := range e.blocks {
		if b.symbol == symbol {
			return true
		}
	}
	return false
}

func (e *Engine) startBlock(now time.Time, s *stock, side string, qtyPerSlice int64) {
	st := e.strategy.Load()
	slices := st.Blocks.BlockTradeSlices
	if side == SideSell {
		pos := e.position(s.cfg.Symbol)
		maxSlices := int(pos.available() / qtyPerSlice)
		if maxSlices < slices {
			slices = maxSlices
		}
		if slices < 1 {
			return
		}
		pos.Reserved += qtyPerSlice * int64(slices)
	}
	b := &block{
		id: e.nextBlockID, symbol: s.cfg.Symbol, side: side, qtyPerSlice: qtyPerSlice,
		slicesTotal: slices, decisionPrice: s.quote, startedAt: now,
		total: s.quote.Mul(decimal.NewFromInt(qtyPerSlice * int64(slices))),
	}
	e.nextBlockID++
	e.blocks[b.id] = b

	e.executeSlice(now, b)
}

// executeSlice fills one slice of a block. Caller holds mu.
func (e *Engine) executeSlice(now time.Time, b *block) {
	s, ok := e.stocks[b.symbol]
	if !ok || s.delisted && b.side == SideBuy {
		e.finishBlock(b)
		return
	}
	qty := b.qtyPerSlice
	if b.slicesDone+1 == b.slicesTotal {
		qty += b.extra
	}
	if b.side == SideSell {
		pos := e.position(b.symbol)
		pos.Reserved -= qty // release this slice's reservation before filling
		if pos.Reserved < 0 {
			pos.Reserved = 0
		}
		if qty > pos.available() {
			qty = pos.available()
		}
	}
	n := b.slicesDone + 1
	id := b.id
	row := e.fill(now, s, b.side, qty, fillOpts{blockID: &id, sliceNo: &n, decisionPrice: b.decisionPrice})
	b.slicesDone++
	if row != nil {
		b.filledQty += row.Qty
		b.filledNotion = b.filledNotion.Add(row.Notional)
		e.emitTrade(row)
	}
	if row == nil || b.slicesDone >= b.slicesTotal {
		e.finishBlock(b)
	}
}

func (e *Engine) finishBlock(b *block) {
	if b.side == SideSell {
		pos := e.position(b.symbol)
		remaining := b.qtyPerSlice * int64(b.slicesTotal-b.slicesDone)
		if b.slicesDone < b.slicesTotal {
			remaining += b.extra
		}
		pos.Reserved -= remaining
		if pos.Reserved < 0 {
			pos.Reserved = 0
		}
	}
	delete(e.blocks, b.id)
}

func (e *Engine) advanceBlocks(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]int64, 0, len(e.blocks))
	for id := range e.blocks {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if b, ok := e.blocks[id]; ok {
			e.executeSlice(now, b)
		}
	}
}

// ---- calibrator and snapshots ---------------------------------------------

func (e *Engine) calibrate(now time.Time) {
	st := e.strategy.Load()
	e.mu.Lock()
	defer e.mu.Unlock()
	eq := e.equity()
	ratio := e.cashRatio()
	before := e.buyChance
	t := st.Trading
	if ratio > st.Capital.TargetCashRatio {
		e.buyChance = clamp(e.buyChance+t.BuyChanceStep, t.BuyChanceMin, t.BuyChanceMax)
	} else if ratio < st.Capital.TargetCashRatio {
		e.buyChance = clamp(e.buyChance-t.BuyChanceStep, t.BuyChanceMin, t.BuyChanceMax)
	}
	e.optimalChance = e.regulatedOptimal(st, e.cash.Add(eq))
	row := CalibrationRow{
		TS: DorisTime(now), Cash: e.cash, Equity: eq, CashRatio: ratio,
		TargetCashRatio: st.Capital.TargetCashRatio, BuyChanceBefore: before, BuyChanceAfter: e.buyChance,
	}
	e.lastCalibration = &row
	e.calibratedFlag = true
	e.emit.Emit(TableCalibrations, row)
	e.rowCount.Add(1)
}

func (e *Engine) snapshot(now time.Time) {
	e.mu.Lock()
	eq := e.equity()
	nav := e.cash.Add(eq)
	row := SnapshotRow{
		TS: DorisTime(now), Cash: e.cash, Equity: eq, Nav: nav, Pnl: e.pnl(nav),
		TradeCostTotal: e.tradeCostTotal, RealizedTotal: e.realizedTotal,
		CashRatio: e.cashRatio(), BuyChance: e.buyChance, Calibrated: e.calibratedFlag,
	}
	e.calibratedFlag = false
	e.mu.Unlock()
	e.emit.Emit(TableBookSnapshots, row)
	e.rowCount.Add(1)
}

// regulatedOptimal returns the optimal-trade chance for the current NAV and
// latches the regime: crossing nav_rich_threshold_amount switches to when_rich,
// crossing nav_poor_threshold_amount switches to when_poor, and the switch
// stays in force until the opposite threshold is crossed. The file value is
// used only from start (or a strategy.toml reload) until the first crossing,
// so a rich book keeps trading badly all the way down, not just above $2T.
func (e *Engine) regulatedOptimal(st *config.Strategy, nav decimal.Decimal) float64 {
	t := st.Trading
	navF, _ := nav.Float64()
	switch {
	case navF > t.NavRichThresholdAmount:
		e.navRegime = "rich"
	case navF < t.NavPoorThresholdAmount:
		e.navRegime = "poor"
	}
	switch e.navRegime {
	case "rich":
		return t.OptimalTradeChanceWhenRich
	case "poor":
		return t.OptimalTradeChanceWhenPoor
	default:
		e.navRegime = "normal"
		return t.OptimalTradeChance
	}
}
