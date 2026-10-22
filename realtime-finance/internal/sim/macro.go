package sim

import (
	"math"
	"math/rand"

	"finsim/internal/config"
)

// macro is the market-wide multiplier applied to every quoted price. Same rule
// as a stock: walk is a bounded trend walk inside [min_multiplier, max_multiplier]
// and mult = walk × (1 + noise) is what the quotes use. When market.toml moves
// the range or sets pinned_multiplier the walk moves toward the new target at
// pin_step_percent per tick; it never jumps. Guarded by Engine.mu.
type macro struct {
	walk float64 // bounded trend walk (or the pin)
	mult float64 // walk × (1 + noise): applied to every quote
	dir  int
}

func newMacro(rng *rand.Rand) macro {
	m := macro{walk: 1.0, mult: 1.0, dir: 1}
	if rng.Intn(2) == 0 {
		m.dir = -1
	}
	return m
}

// target returns where the multiplier must walk to, or 0 if it is free to roam.
func (m *macro) target(c config.MacroConfig) float64 {
	if c.PinnedMultiplier != nil {
		return *c.PinnedMultiplier
	}
	if m.walk > c.MaxMultiplier {
		return c.MaxMultiplier
	}
	if m.walk < c.MinMultiplier {
		return c.MinMultiplier
	}
	return 0
}

// Pinned reports whether the multiplier is held at a fixed level.
func (m *macro) pinned(c config.MacroConfig) bool { return c.PinnedMultiplier != nil }

func (m *macro) tick(rng *rand.Rand, c config.MacroConfig) {
	noise := (rng.Float64()*2 - 1) * c.NoisePercent / 100
	if t := m.target(c); t > 0 {
		m.walk, m.dir = stepToward(m.walk, t, m.walk*c.PinStepPercent/100, m.dir)
		m.mult = m.walk * (1 + noise)
		return
	}
	if rng.Float64() < c.DirectionFlipChance {
		m.dir = -m.dir
	}
	next := m.walk * (1 + float64(m.dir)*rng.Float64()*c.TrendStepPercent/100)
	if next >= c.MaxMultiplier {
		next, m.dir = c.MaxMultiplier, -1
	} else if next <= c.MinMultiplier {
		next, m.dir = c.MinMultiplier, 1
	}
	m.walk = next
	m.mult = m.walk * (1 + noise)
}

// stepToward moves v toward target by at most step, snapping when within one step.
// The returned direction is the sign of the move (unchanged when already there).
func stepToward(v, target, step float64, dir int) (float64, int) {
	gap := target - v
	if math.Abs(gap) <= step {
		if gap > 0 {
			dir = 1
		} else if gap < 0 {
			dir = -1
		}
		return target, dir
	}
	if gap > 0 {
		return v + step, 1
	}
	return v - step, -1
}
