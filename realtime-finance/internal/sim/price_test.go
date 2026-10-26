package sim

import (
	"math/rand"
	"testing"
	"time"

	"finsim/internal/config"

	"github.com/shopspring/decimal"
)

func testStock(t *testing.T, mutate func(*config.Stock)) *stock {
	t.Helper()
	c := &config.Stock{Symbol: "T", StartPrice: 100, Weight: 1, Real: &config.Band{MinPrice: 90, MaxPrice: 110}}
	if mutate != nil {
		mutate(c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return newStock(c, rand.New(rand.NewSource(1)))
}

func TestPinnedStockWalksToPinThenWigglesThere(t *testing.T) {
	pin, zero := 150.0, 0.0
	s := testStock(t, func(c *config.Stock) { c.PinnedPrice = &pin; c.NoisePercent = &zero })
	rng := rand.New(rand.NewSource(2))
	now := time.Now()
	prev := s.quoteF()
	ticks := 0
	for s.quoteF() < pin && ticks < 1000 {
		s.tick(rng, now, 1.0)
		if s.quoteF() < prev {
			t.Fatalf("pinned stock moved away from the pin: %v -> %v", prev, s.quoteF())
		}
		if s.dir != 1 {
			t.Fatalf("direction while walking up to the pin = %d", s.dir)
		}
		prev = s.quoteF()
		ticks++
	}
	// 100 -> 150 at 0.5 %/tick compounding is ~82 ticks
	if ticks < 60 || ticks > 120 {
		t.Fatalf("reached the pin in %d ticks, want ~82", ticks)
	}
	for i := 0; i < 50; i++ {
		s.tick(rng, now, 1.0)
		if s.quoteF() != pin {
			t.Fatalf("noise-free pinned stock left the pin: %v", s.quoteF())
		}
	}
	// pin is on the quote: with macro 1.25 the walk sits at 120 and the quote at 150
	for i := 0; i < 200; i++ {
		s.tick(rng, now, 1.25)
	}
	if got := s.quoteF(); got != pin {
		t.Fatalf("quote under macro 1.25 = %v, want %v", got, pin)
	}
}

func TestQuoteIsWalkTimesMacroAndBandsStayFixed(t *testing.T) {
	s := testStock(t, nil)
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 500; i++ {
		s.tick(rng, time.Now(), 1.05)
		pf, _ := s.price.Float64()
		if pf < 90-1e-9 || pf > 110+1e-9 {
			t.Fatalf("own walk left the band: %v", pf)
		}
	}
	if s.realPos() <= 0 && s.realPos() >= 1 {
		t.Fatalf("real_pos should be computed on the quote")
	}
	// under a 1.05 rally the quote may sit above the band top
	seenAbove := false
	for i := 0; i < 2000 && !seenAbove; i++ {
		s.tick(rng, time.Now(), 1.05)
		seenAbove = s.quoteF() > 110
	}
	if !seenAbove {
		t.Fatal("a 1.05 macro never pushed the quote above the band top")
	}
}

func TestMacroWalksIntoNewRangeAndToPin(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	m := newMacro(rng)
	c := config.DefaultMarket().Macro
	c.NoisePercent = 0 // walk == mult without noise
	for i := 0; i < 300; i++ {
		m.tick(rng, c)
		if m.mult < c.MinMultiplier || m.mult > c.MaxMultiplier {
			t.Fatalf("macro left its range: %v", m.mult)
		}
	}
	// sell-off: new range below the current value; must walk, not jump
	c.MinMultiplier, c.MaxMultiplier = 0.70, 0.75
	prev := m.mult
	ticks := 0
	for m.mult > 0.75 && ticks < 1000 {
		m.tick(rng, c)
		if m.mult > prev || prev-m.mult > prev*c.PinStepPercent/100+1e-12 {
			t.Fatalf("macro jumped: %v -> %v", prev, m.mult)
		}
		prev = m.mult
		ticks++
	}
	if ticks < 40 || ticks > 80 {
		t.Fatalf("walked into the new range in %d ticks, want ~55", ticks)
	}
	// pin: walks there and stays exactly
	pin := 1.03
	c.PinnedMultiplier = &pin
	for i := 0; i < 200; i++ {
		m.tick(rng, c)
	}
	if m.mult != pin {
		t.Fatalf("pinned macro = %v, want %v", m.mult, pin)
	}
	// flat market: tiny range, the multiplier walks back into it
	c.PinnedMultiplier = nil
	c.MinMultiplier, c.MaxMultiplier = 0.99999, 1.00001
	for i := 0; i < 200; i++ {
		m.tick(rng, c)
	}
	if m.mult < c.MinMultiplier || m.mult > c.MaxMultiplier {
		t.Fatalf("macro did not settle into the flat range: %v", m.mult)
	}
	// noise wiggles the applied multiplier around the walk without moving the walk
	c.NoisePercent = 0.5
	c.MinMultiplier, c.MaxMultiplier = 1.0, 1.0
	seenOff := false
	for i := 0; i < 100; i++ {
		m.tick(rng, c)
		if m.walk != 1.0 {
			t.Fatalf("noise moved the walk: %v", m.walk)
		}
		seenOff = seenOff || m.mult != 1.0
	}
	if !seenOff {
		t.Fatal("noise never moved the applied multiplier")
	}
}

func TestStockOutsideNarrowedBandWalksBackAtPinStep(t *testing.T) {
	zero := 0.0
	s := testStock(t, func(c *config.Stock) { c.NoisePercent = &zero })
	rng := rand.New(rand.NewSource(5))
	s.applyConfig(&config.Stock{Symbol: "T", StartPrice: 100, Weight: 1, NoisePercent: &zero, Real: &config.Band{MinPrice: 50, MaxPrice: 60}}, rng)
	prev := s.quoteF()
	ticks := 0
	for s.quoteF() > 60 && ticks < 1000 {
		s.tick(rng, time.Now(), 1.0)
		if s.quoteF() > prev || s.dir != -1 {
			t.Fatalf("did not walk down toward the band: %v -> %v dir %d", prev, s.quoteF(), s.dir)
		}
		prev = s.quoteF()
		ticks++
	}
	if ticks < 80 || ticks > 130 {
		t.Fatalf("walked back into the band in %d ticks, want ~102", ticks)
	}
}

func TestDerivedBandAnchorsOnThePriceTheRunStartsFrom(t *testing.T) {
	half := 0.5
	c := &config.Stock{Symbol: "T", StartPrice: 100, Weight: 1, BandPercent: &half}
	s := newStock(c, rand.New(rand.NewSource(1)))
	if s.real != (Band64{50, 150}) {
		t.Fatalf("band from start_price = %+v, want 50–150", s.real)
	}
	s.setPrice(decimal.NewFromFloat(200)) // resume from Doris
	if s.real != (Band64{100, 300}) {
		t.Fatalf("band after resume = %+v, want 100–300", s.real)
	}
	if s.perceived != s.real {
		t.Fatalf("perceived should follow a derived real band")
	}
	// a reload without [real] keeps the anchor; with [real] the file wins
	s.applyConfig(c, rand.New(rand.NewSource(2)))
	if s.real != (Band64{100, 300}) {
		t.Fatalf("band after reload = %+v, want 100–300", s.real)
	}
	s.applyConfig(&config.Stock{Symbol: "T", StartPrice: 100, Weight: 1, Real: &config.Band{MinPrice: 10, MaxPrice: 20}}, rand.New(rand.NewSource(3)))
	if s.real != (Band64{10, 20}) {
		t.Fatalf("explicit band = %+v, want 10–20", s.real)
	}
}

func TestCashoutReducesCashAndNavButNotPnl(t *testing.T) {
	e := newTraderTestEngine(t, map[string]float64{"A": 150})
	e.positions["A"] = &Position{Qty: 10, AvgCost: decimal.NewFromInt(100)}
	before := e.Live()
	ev, err := e.Cashout(decimal.NewFromInt(1000))
	if err != nil {
		t.Fatal(err)
	}
	after := e.Live()
	if !after.Cash.Equal(before.Cash.Sub(decimal.NewFromInt(1000))) || !after.Nav.Equal(before.Nav.Sub(decimal.NewFromInt(1000))) {
		t.Fatalf("cash/nav not reduced: %v -> %v, %v -> %v", before.Cash, after.Cash, before.Nav, after.Nav)
	}
	if !after.Pnl.Equal(before.Pnl) {
		t.Fatalf("pnl changed by a cashout: %v -> %v", before.Pnl, after.Pnl)
	}
	if !after.PaidOut.Equal(decimal.NewFromInt(1000)) || ev.CashoutID != 1 || len(after.Cashouts) != 1 {
		t.Fatalf("bookkeeping: paid_out %v id %d events %d", after.PaidOut, ev.CashoutID, len(after.Cashouts))
	}
	if _, err := e.Cashout(decimal.NewFromInt(-1)); err == nil {
		t.Fatal("negative cashout should fail")
	}
}

func TestCashoutMayOverdrawCashUpToNavAndBlocksBuys(t *testing.T) {
	e := newTraderTestEngine(t, map[string]float64{"A": 150})
	e.positions["A"] = &Position{Qty: 10, AvgCost: decimal.NewFromInt(100)} // equity 1500
	start := e.Live()

	if _, err := e.Cashout(start.Nav.Add(decimal.NewFromInt(1))); err == nil {
		t.Fatal("cashout above NAV should fail")
	}
	if _, err := e.Cashout(start.Cash.Add(decimal.NewFromInt(500))); err != nil {
		t.Fatalf("cashout above cash but below NAV should succeed: %v", err)
	}
	over := e.Live()
	if !over.Cash.Equal(decimal.NewFromInt(-500)) || !over.BuysBlocked {
		t.Fatalf("cash = %v, buys_blocked = %v; want -500, true", over.Cash, over.BuysBlocked)
	}
	if !over.Pnl.Equal(start.Pnl) {
		t.Fatalf("pnl changed by an overdraft: %v -> %v", start.Pnl, over.Pnl)
	}
	if over.CashRatio >= 0 {
		t.Fatalf("cash ratio = %v, want negative so the calibrator leans to selling", over.CashRatio)
	}

	// every turn rolls BUY: all of them must be skipped while cash is not positive
	e.buyChance = 1
	skips, trades := e.skipCount.Load(), e.tradeCount.Load()
	for i := 0; i < 50; i++ {
		e.oneTrade(time.Now())
	}
	if e.tradeCount.Load() != trades || e.skipCount.Load() != skips+50 {
		t.Fatalf("buys while overdrawn: %d trades, %d skips; want 0 and 50", e.tradeCount.Load()-trades, e.skipCount.Load()-skips)
	}
	if got := e.fill(time.Now(), e.stocks["A"], SideBuy, 1, fillOpts{}); got != nil {
		t.Fatal("a direct buy fill must fail while cash is negative")
	}
	// selling is still possible and is what brings cash back
	if got := e.fill(time.Now(), e.stocks["A"], SideSell, 5, fillOpts{}); got == nil {
		t.Fatal("sell should work while overdrawn")
	}
	if e.Live().BuysBlocked {
		t.Fatalf("cash = %v after selling 5 @150; buys should be unblocked", e.Live().Cash)
	}
}

func TestTopupAddsCashAndNavButNotPnl(t *testing.T) {
	e := newTraderTestEngine(t, map[string]float64{"A": 150})
	before := e.Live()
	if _, err := e.Cashout(before.Cash); err != nil { // cash 0: buys blocked
		t.Fatal(err)
	}
	if !e.Live().BuysBlocked {
		t.Fatal("zero cash should block buys")
	}
	ev, err := e.Topup(decimal.NewFromInt(700))
	if err != nil {
		t.Fatal(err)
	}
	after := e.Live()
	if !after.Cash.Equal(decimal.NewFromInt(700)) || after.BuysBlocked || !ev.Amount.Equal(decimal.NewFromInt(-700)) {
		t.Fatalf("after top-up: cash %v blocked %v event amount %v", after.Cash, after.BuysBlocked, ev.Amount)
	}
	if !after.Pnl.Equal(before.Pnl) {
		t.Fatalf("pnl changed by a top-up: %v -> %v", before.Pnl, after.Pnl)
	}
	if want := before.Cash.Sub(decimal.NewFromInt(700)); !after.PaidOut.Equal(want) {
		t.Fatalf("net paid out = %v, want %v (withdrawals minus top-ups)", after.PaidOut, want)
	}
	if _, err := e.Topup(decimal.Zero); err == nil {
		t.Fatal("zero top-up should fail")
	}
}

func TestForgetCashoutsDropsThemFromPnlOnly(t *testing.T) {
	e := newTraderTestEngine(t, map[string]float64{"A": 150})
	e.positions["A"] = &Position{Qty: 10, AvgCost: decimal.NewFromInt(100)}
	if _, err := e.Cashout(decimal.NewFromInt(1000)); err != nil {
		t.Fatal(err)
	}
	before := e.Live()
	newly := e.ForgetCashouts(e.PaidOutTotal())
	after := e.Live()
	if !newly.Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("newly forgotten = %v, want 1000", newly)
	}
	if !after.Cash.Equal(before.Cash) || !after.Nav.Equal(before.Nav) {
		t.Fatalf("forget moved cash or nav: %v -> %v, %v -> %v", before.Cash, after.Cash, before.Nav, after.Nav)
	}
	if want := before.Pnl.Sub(decimal.NewFromInt(1000)); !after.Pnl.Equal(want) || !after.Pnl.Equal(after.Nav.Sub(after.StartingCash)) {
		t.Fatalf("pnl after forget = %v, want %v = nav - starting cash", after.Pnl, want)
	}
	if !after.PaidOut.IsZero() || !after.PaidOutForgot.Equal(decimal.NewFromInt(1000)) || !after.PaidOutTotal.Equal(decimal.NewFromInt(1000)) || len(after.Cashouts) != 0 {
		t.Fatalf("bookkeeping after forget: paid_out %v forgotten %v total %v events %d", after.PaidOut, after.PaidOutForgot, after.PaidOutTotal, len(after.Cashouts))
	}
	if again := e.ForgetCashouts(e.PaidOutTotal()); !again.IsZero() {
		t.Fatalf("second forget = %v, want 0", again)
	}
	// a later cashout is remembered again
	if _, err := e.Cashout(decimal.NewFromInt(200)); err != nil {
		t.Fatal(err)
	}
	if got := e.Live(); !got.PaidOut.Equal(decimal.NewFromInt(200)) || !got.Pnl.Equal(after.Pnl) {
		t.Fatalf("after a new cashout: paid_out %v pnl %v, want 200 and %v", got.PaidOut, got.Pnl, after.Pnl)
	}
}
