package sim

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// Seed builds the opening portfolio: initial_equity_ratio of starting cash,
// allocated by weight, executed as BUY rows flagged is_seed at the opening quote.
// Returns the number of rows emitted.
func (e *Engine) Seed(now time.Time) int {
	st := e.strategy.Load()
	if st.Capital.InitialEquityRatio <= 0 {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	total := e.startingCash.Mul(decimal.NewFromFloat(st.Capital.InitialEquityRatio))
	ticket := decimal.NewFromFloat(st.Capital.InitialPositionTradeAmount)

	syms := make([]string, 0, len(e.stocks))
	sumW := 0.0
	for sym, s := range e.stocks {
		if !s.delisted && s.cfg.Weight > 0 {
			syms = append(syms, sym)
			sumW += s.cfg.Weight
		}
	}
	sort.Strings(syms)
	if sumW <= 0 {
		return 0
	}
	rows := 0
	for _, sym := range syms {
		s := e.stocks[sym]
		target := total.Mul(decimal.NewFromFloat(s.cfg.Weight / sumW))
		spent := zero
		for spent.Add(ticket).LessThanOrEqual(target) {
			qty := ticket.Div(s.quote).Floor().IntPart()
			if qty < 1 {
				break
			}
			row := e.fill(now, s, SideBuy, qty, fillOpts{isSeed: true})
			if row == nil {
				break
			}
			e.emitTrade(row)
			rows++
			spent = spent.Add(row.Notional)
		}
		if rem := target.Sub(spent); rem.GreaterThan(s.quote) {
			qty := rem.Div(s.quote).Floor().IntPart()
			if row := e.fill(now, s, SideBuy, qty, fillOpts{isSeed: true}); row != nil {
				e.emitTrade(row)
				rows++
			}
		}
	}
	e.status.Seeded = true
	return rows
}
