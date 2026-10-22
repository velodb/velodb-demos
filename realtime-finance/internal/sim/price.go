package sim

import (
	"math/rand"
	"time"

	"finsim/internal/config"

	"github.com/shopspring/decimal"
)

const historyLen = 300 // 5 minutes of quotes at 1 tick/s, for sparklines

// stock is the per-symbol price state. Guarded by Engine.mu.
//
// price is the stock's own walk (pre-macro, 6 dp); quote is what the tape and
// the desk see: price × macro multiplier, in cents.
type stock struct {
	raw *config.Stock
	cfg config.ResolvedStock

	price  decimal.Decimal // own walk, internal, 6 dp
	quote  decimal.Decimal // emitted / traded, 2 dp (price × macro)
	anchor float64         // price the run started from (Doris latest, else start_price); centres a derived band
	dir    int             // +1 up, -1 down

	real, perceived Band64
	delisted        bool

	history []float64 // ring of recent quotes
	histPos int
	histLen int
	lastTS  time.Time
}

type Band64 struct{ Min, Max float64 }

func newStock(c *config.Stock, rng *rand.Rand) *stock {
	s := &stock{history: make([]float64, historyLen), anchor: c.StartPrice}
	s.applyConfig(c, rng)
	s.setPrice(decimal.NewFromFloat(c.StartPrice))
	return s
}

// applyConfig swaps in a (re)loaded configuration. Price is kept; direction re-drawn 50/50.
// Without a [real] section the band is anchor ± band_percent, where anchor is the
// price this run started from (the Doris latest price on resume, else start_price).
func (s *stock) applyConfig(c *config.Stock, rng *rand.Rand) {
	s.raw = c
	s.cfg = c.Resolve()
	s.setBands()
	if rng.Intn(2) == 0 {
		s.dir = 1
	} else {
		s.dir = -1
	}
}

// setBands derives the live bands from the config and, for a derived real band,
// the anchor price. Perceived follows real unless the file sets it.
func (s *stock) setBands() {
	if s.cfg.RealIsDerived {
		s.cfg.Real = config.DerivedBand(s.anchor, s.cfg.BandPercent)
		if s.cfg.PerceivedIsReal {
			s.cfg.Perceived = s.cfg.Real
		}
	}
	s.real = Band64{s.cfg.Real.MinPrice, s.cfg.Real.MaxPrice}
	s.perceived = Band64{s.cfg.Perceived.MinPrice, s.cfg.Perceived.MaxPrice}
}

// setPrice replaces the price and restarts the sparkline history from it
// (used at start-up and on resume, so a restored price never shows as a jump).
// The price becomes the anchor, so a derived band is re-centred on it.
// The macro multiplier is 1.0 at boot, so a restored quote is also the walk.
func (s *stock) setPrice(p decimal.Decimal) {
	s.price = p.Round(6)
	s.anchor, _ = s.price.Float64()
	s.setBands()
	s.setQuote(1.0)
	s.histPos, s.histLen = 0, 0
	s.pushHistory()
}

func (s *stock) setQuote(macro float64) {
	s.quote = s.price.Mul(decimal.NewFromFloat(macro)).RoundBank(2)
	if s.quote.Sign() <= 0 {
		s.quote = decimal.NewFromFloat(0.01)
	}
}

func (s *stock) pinned() bool { return s.cfg.PinnedPrice > 0 }

func (s *stock) pushHistory() {
	q, _ := s.quote.Float64()
	s.history[s.histPos] = q
	s.histPos = (s.histPos + 1) % historyLen
	if s.histLen < historyLen {
		s.histLen++
	}
}

// History returns the last n quotes oldest-first.
func (s *stock) History(n int) []float64 {
	if n > s.histLen {
		n = s.histLen
	}
	out := make([]float64, n)
	start := (s.histPos - n + historyLen) % historyLen
	for i := 0; i < n; i++ {
		out[i] = s.history[(start+i)%historyLen]
	}
	return out
}

func (s *stock) quoteF() float64 { f, _ := s.quote.Float64(); return f }

func (s *stock) realPos() float64      { return bandPos(s.quoteF(), s.real) }
func (s *stock) perceivedPos() float64 { return bandPos(s.quoteF(), s.perceived) }

func bandPos(p float64, b Band64) float64 {
	if b.Max <= b.Min {
		return 0.5
	}
	return (p - b.Min) / (b.Max - b.Min)
}

// tick advances the price by one step. See PLAN.md §3.1.
//
// The own walk is bounded: a random trend step in the current direction plus
// independent noise. Inside the real band the direction flips with
// direction_flip_chance per tick and always at the band edge. A price outside
// the band (after a reload narrowed it) walks back in at pin_step_percent per
// tick; it never jumps. A pinned stock walks to pinned_price / macro at
// pin_step_percent per tick and then wiggles there with the noise. The quote
// is the walk × macro. Our own trading never moves the price.
func (s *stock) tick(rng *rand.Rand, now time.Time, macro float64) {
	pf, _ := s.price.Float64()
	noise := (rng.Float64()*2 - 1) * s.cfg.NoisePercent / 100
	var np decimal.Decimal
	switch {
	case s.pinned():
		target := s.cfg.PinnedPrice / macro
		pf, s.dir = stepToward(pf, target, pf*s.cfg.PinStepPercent/100, s.dir)
		np = decimal.NewFromFloat(pf * (1 + noise))
	case pf > s.real.Max || pf < s.real.Min:
		edge := s.real.Max
		if pf < s.real.Min {
			edge = s.real.Min
		}
		pf, s.dir = stepToward(pf, edge, pf*s.cfg.PinStepPercent/100, s.dir)
		np = decimal.NewFromFloat(pf * (1 + noise))
	default:
		if rng.Float64() < s.cfg.DirectionFlipChance {
			s.dir = -s.dir
		}
		trend := float64(s.dir) * rng.Float64() * s.cfg.TrendStepPercent / 100
		np = s.price.Mul(decimal.NewFromFloat((1 + trend) * (1 + noise)))
		nf, _ := np.Float64()
		if nf >= s.real.Max {
			np = decimal.NewFromFloat(s.real.Max)
			s.dir = -1
		} else if nf <= s.real.Min {
			np = decimal.NewFromFloat(s.real.Min)
			s.dir = 1
		}
	}
	s.price = np.Round(6)
	s.setQuote(macro)
	s.lastTS = now
	s.pushHistory()
}

// tickAll advances every listed stock and emits one batch of price rows.
func (e *Engine) tickAll(now time.Time) {
	e.mu.Lock()
	mc := e.market.Load().Macro
	e.macro.tick(e.rng, mc)
	mult := e.macro.mult
	rows := make([]Row, 0, len(e.stocks)*2)
	for sym, s := range e.stocks {
		if s.delisted {
			continue
		}
		s.tick(e.rng, now, mult)
		rows = append(rows,
			PriceRow{TS: DorisTime(now), Symbol: sym, Price: s.quote, FairPrice: s.quote, RealPos: s.realPos(), PerceivedPos: s.perceivedPos(), Dir: s.dir},
			LatestPriceRow{Symbol: sym, TS: DorisTime(now), Price: s.quote, FairPrice: s.quote, Name: s.cfg.Name, Sector: s.cfg.Sector, Weight: s.cfg.Weight},
		)
	}
	e.tickCount.Add(int64(len(rows) / 2))
	e.mu.Unlock()
	for _, r := range rows {
		switch r.(type) {
		case PriceRow:
			e.emit.Emit(TablePrices, r)
		case LatestPriceRow:
			e.emit.Emit(TableLatestPrice, r)
		}
	}
	e.rowCount.Add(int64(len(rows)))
}
