package sim

import (
	"testing"

	"finsim/internal/config"
)

func newTraderTestEngine(t *testing.T, prices map[string]float64) *Engine {
	t.Helper()
	strategy := config.DefaultStrategy()
	strategy.Trading.MaintainTargetWeight = false // these tests exercise band halves and locks, not weights
	market := config.DefaultMarket()
	stocks := make([]*config.Stock, 0, len(prices))
	for symbol, price := range prices {
		stocks = append(stocks, &config.Stock{
			Symbol: symbol, Name: symbol, Sector: "test", StartPrice: price, Weight: 1,
			Real:      &config.Band{MinPrice: 100, MaxPrice: 200},
			Perceived: &config.Band{MinPrice: 100, MaxPrice: 200},
		})
	}
	return New(&strategy, &market, stocks, nil)
}

func TestPickersDoNotFallBackAcrossPerceivedBand(t *testing.T) {
	e := newTraderTestEngine(t, map[string]float64{"LOW": 125, "HIGH": 175})

	if got := e.pickBuy(true); got == nil || got.cfg.Symbol != "LOW" {
		t.Fatalf("optimal buy = %v, want LOW", got)
	}
	if got := e.pickBuy(false); got == nil || got.cfg.Symbol != "HIGH" {
		t.Fatalf("non-optimal buy = %v, want HIGH", got)
	}

	e.positions["LOW"] = &Position{Qty: 10}
	e.positions["HIGH"] = &Position{Qty: 10}
	if got := e.pickSell(true); got == nil || got.cfg.Symbol != "HIGH" {
		t.Fatalf("optimal sell = %v, want HIGH", got)
	}
	if got := e.pickSell(false); got == nil || got.cfg.Symbol != "LOW" {
		t.Fatalf("non-optimal sell = %v, want LOW", got)
	}

	delete(e.stocks, "LOW")
	delete(e.positions, "HIGH")
	if got := e.pickBuy(true); got != nil {
		t.Fatalf("optimal buy fell back to %s, want no trade", got.cfg.Symbol)
	}
	if got := e.pickSell(true); got != nil {
		t.Fatalf("optimal sell fell back to %s, want no trade", got.cfg.Symbol)
	}
}

func TestActiveBlockLocksSymbolForBothSidesUntilFinished(t *testing.T) {
	e := newTraderTestEngine(t, map[string]float64{"NVDA": 125})
	e.positions["NVDA"] = &Position{Qty: 10}
	b := &block{id: 1, symbol: "NVDA", side: SideSell, slicesTotal: 5}
	e.blocks[b.id] = b

	if !e.hasBlock("NVDA") {
		t.Fatal("NVDA should be locked while its block is active")
	}
	if got := e.pickBuy(true); got != nil {
		t.Fatalf("buy selected locked symbol %s", got.cfg.Symbol)
	}
	if got := e.pickSell(false); got != nil {
		t.Fatalf("sell selected locked symbol %s", got.cfg.Symbol)
	}

	e.finishBlock(b)
	if e.hasBlock("NVDA") {
		t.Fatal("NVDA should be unlocked after its block finishes")
	}
	if got := e.pickBuy(true); got == nil || got.cfg.Symbol != "NVDA" {
		t.Fatalf("unlocked buy = %v, want NVDA", got)
	}
}
