package sim

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// ExtremeEvent is one all-in trade decision, kept for the dashboard.
type ExtremeEvent struct {
	TS        time.Time       `json:"ts"`
	Kind      string          `json:"kind"` // sell_all_at_high | suck_all_at_low | panic_sell_at_low | fomo_buy_at_high
	Symbol    string          `json:"symbol"`
	BlockID   int64           `json:"block_id"`
	Qty       int64           `json:"qty"`
	Notional  decimal.Decimal `json:"notional"`
	BandPos   float64         `json:"band_pos"`
	CashRatio float64         `json:"cash_ratio"`
}

const maxExtremeEvents = 30

// extremeLoop checks once per second for band extremes (PLAN.md §3.4).
func (e *Engine) extremeLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			e.extremeCheck(now.UTC())
		}
	}
}

// extremeCheck fires at most one all-in order per second. The money makers:
//   - sell_all_at_high: a holding at or above sell_all_band_position of its perceived
//     band is sold entirely, as one block, provided cash is below sell_all_max_cash_ratio.
//   - suck_all_at_low: a stock at or below suck_all_band_position is bought with
//     suck_all_cash_share of cash, as one block, provided cash is at least
//     suck_all_min_cash_ratio.
//
// And their money-losing mirrors, same mechanics with their own thresholds:
//   - panic_sell_at_low: dump an entire holding at or below panic_sell_band_position
//     while cash is below panic_sell_max_cash_ratio.
//   - fomo_buy_at_high: buy fomo_buy_cash_share of cash at or above
//     fomo_buy_band_position while cash is at least fomo_buy_min_cash_ratio.
//
// Each eligible stock is taken with chance_per_second, so the timing is random.
func (e *Engine) extremeCheck(now time.Time) {
	x := e.strategy.Load().Extreme
	if !x.SellAllAtHigh && !x.SuckAllAtLow && !x.PanicSellAtLow && !x.FomoBuyAtHigh {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ratio := e.cashRatio()
	for sym, s := range e.stocks {
		if s.delisted || e.hasBlock(sym) || e.rng.Float64() >= x.ChancePerSecond {
			continue
		}
		pos := s.perceivedPos()
		if x.SellAllAtHigh && pos >= x.SellAllBandPosition && ratio < x.SellAllMaxCashRatio {
			if e.dumpAll(now, s, "sell_all_at_high", pos, ratio) {
				return
			}
		}
		if x.PanicSellAtLow && pos <= x.PanicSellBandPosition && ratio < x.PanicSellMaxCashRatio {
			if e.dumpAll(now, s, "panic_sell_at_low", pos, ratio) {
				return
			}
		}
		if x.SuckAllAtLow && pos <= x.SuckAllBandPosition && ratio >= x.SuckAllMinCashRatio {
			if e.buyShare(now, s, "suck_all_at_low", x.SuckAllCashShare, pos, ratio) {
				return
			}
		}
		if x.FomoBuyAtHigh && pos >= x.FomoBuyBandPosition && ratio >= x.FomoBuyMinCashRatio {
			if e.buyShare(now, s, "fomo_buy_at_high", x.FomoBuyCashShare, pos, ratio) {
				return
			}
		}
	}
}

// dumpAll sells the whole available holding of s as one block. Caller holds mu.
func (e *Engine) dumpAll(now time.Time, s *stock, kind string, pos, ratio float64) bool {
	p, ok := e.positions[s.cfg.Symbol]
	if !ok || p.available() <= 0 {
		return false
	}
	b := e.startBlockQty(now, s, SideSell, p.available())
	if b == nil {
		return false
	}
	e.recordExtreme(now, kind, b, pos, ratio)
	return true
}

// buyShare spends share of cash on s as one block. Caller holds mu.
func (e *Engine) buyShare(now time.Time, s *stock, kind string, share, pos, ratio float64) bool {
	qty := e.cash.Mul(decimal.NewFromFloat(share)).Div(s.quote).Floor().IntPart()
	if qty <= 0 {
		return false
	}
	b := e.startBlockQty(now, s, SideBuy, qty)
	if b == nil {
		return false
	}
	e.recordExtreme(now, kind, b, pos, ratio)
	return true
}

// startBlockQty works a fixed total quantity as one block over the configured
// tranches; the remainder goes on the last tranche. Caller holds mu.
func (e *Engine) startBlockQty(now time.Time, s *stock, side string, total int64) *block {
	st := e.strategy.Load()
	slices := st.Blocks.BlockTradeSlices
	if slices < 1 {
		slices = 1
	}
	per := total / int64(slices)
	if per < 1 {
		slices, per = 1, total
	}
	extra := total - per*int64(slices)
	if side == SideSell {
		e.position(s.cfg.Symbol).Reserved += total
	}
	b := &block{
		id: e.nextBlockID, symbol: s.cfg.Symbol, side: side, qtyPerSlice: per, extra: extra,
		slicesTotal: slices, decisionPrice: s.quote, startedAt: now,
		total: s.quote.Mul(decimal.NewFromInt(total)),
	}
	e.nextBlockID++
	e.blocks[b.id] = b
	e.executeSlice(now, b)
	return b
}

func (e *Engine) recordExtreme(now time.Time, kind string, b *block, pos, ratio float64) {
	ev := ExtremeEvent{TS: now, Kind: kind, Symbol: b.symbol, BlockID: b.id, Qty: b.qtyPerSlice*int64(b.slicesTotal) + b.extra, Notional: b.total, BandPos: pos, CashRatio: ratio}
	e.extremes = append(e.extremes, ev)
	if len(e.extremes) > maxExtremeEvents {
		e.extremes = e.extremes[len(e.extremes)-maxExtremeEvents:]
	}
	e.logf("%s %s qty=%d notional=%s band=%.0f%% cash=%.1f%% block=%d", kind, b.symbol, ev.Qty, b.total.StringFixed(0), pos*100, ratio*100, b.id)
}
