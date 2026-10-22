package sim

import (
	"time"

	"github.com/shopspring/decimal"
)

var (
	zero = decimal.Zero
	one  = decimal.NewFromInt(1)
)

func (e *Engine) position(sym string) *Position {
	p, ok := e.positions[sym]
	if !ok {
		p = &Position{}
		e.positions[sym] = p
	}
	return p
}

// equity is Σ qty × quote over all positions. Caller holds mu.
func (e *Engine) equity() decimal.Decimal {
	eq := zero
	for sym, p := range e.positions {
		if p.Qty <= 0 {
			continue
		}
		if s, ok := e.stocks[sym]; ok {
			eq = eq.Add(s.quote.Mul(decimal.NewFromInt(p.Qty)))
		}
	}
	return eq
}

func (e *Engine) cashRatio() float64 {
	eq := e.equity()
	nav := e.cash.Add(eq)
	if nav.Sign() <= 0 {
		if e.cash.Sign() <= 0 {
			return 0 // overdrawn and nothing left: certainly not "all cash"
		}
		return 1
	}
	r, _ := e.cash.Div(nav).Float64()
	return r
}

type fillOpts struct {
	blockID       *int64
	sliceNo       *int
	decisionPrice decimal.Decimal
	isSeed        bool
}

// fill executes one trade at the current quote and mutates the book.
// Caller holds mu. Returns nil when nothing could be traded (no cash, no holding).
func (e *Engine) fill(now time.Time, s *stock, side string, qty int64, o fillOpts) *TradeRow {
	st := e.strategy.Load()
	if qty <= 0 {
		return nil
	}
	price := s.quote
	pos := e.position(s.cfg.Symbol)
	rate := decimal.NewFromFloat(st.Trading.TradeCostRate)
	minFee := decimal.NewFromFloat(st.Trading.TradeCostMinAmount)

	var notional, fee, realized decimal.Decimal
	switch side {
	case SideBuy:
		notional = price.Mul(decimal.NewFromInt(qty))
		fee = maxDec(notional.Mul(rate), minFee).Round(6)
		if e.cash.LessThan(notional.Add(fee)) {
			// cannot afford: shrink to what cash allows
			afford := e.cash.Sub(minFee).Div(price.Mul(one.Add(rate))).Floor()
			qty = afford.IntPart()
			if qty <= 0 {
				return nil
			}
			notional = price.Mul(decimal.NewFromInt(qty))
			fee = maxDec(notional.Mul(rate), minFee).Round(6)
		}
		e.cash = e.cash.Sub(notional).Sub(fee)
		total := pos.AvgCost.Mul(decimal.NewFromInt(pos.Qty)).Add(notional)
		pos.Qty += qty
		pos.AvgCost = total.Div(decimal.NewFromInt(pos.Qty)).Round(6)
	case SideSell:
		if qty > pos.Qty {
			qty = pos.Qty
		}
		if qty <= 0 {
			return nil
		}
		notional = price.Mul(decimal.NewFromInt(qty))
		fee = maxDec(notional.Mul(rate), minFee).Round(6)
		realized = price.Sub(pos.AvgCost).Mul(decimal.NewFromInt(qty)).Round(6)
		e.cash = e.cash.Add(notional).Sub(fee)
		pos.Qty -= qty
		if pos.Qty == 0 {
			pos.AvgCost = zero
		}
	default:
		return nil
	}
	e.tradeCostTotal = e.tradeCostTotal.Add(fee)
	e.realizedTotal = e.realizedTotal.Add(realized)

	decision := o.decisionPrice
	if decision.IsZero() {
		decision = price
	}
	row := &TradeRow{
		TS: DorisTime(now), TradeID: e.nextTradeID, BlockID: o.blockID, SliceNo: o.sliceNo,
		Symbol: s.cfg.Symbol, Side: side, Qty: qty, Price: price, Notional: notional,
		TradeCost: fee, RealizedPnl: realized, AvgCostAfter: pos.AvgCost, DecisionPrice: decision,
		PerceivedPos: s.perceivedPos(), CashAfter: e.cash, BuyChance: e.buyChance, IsSeed: o.isSeed,
	}
	e.nextTradeID++
	e.tradeCount.Add(1)
	return row
}

func maxDec(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}
	return b
}
