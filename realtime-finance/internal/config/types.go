// Package config holds the three TOML configuration files (strategy.toml,
// market.toml, stocks/*.toml), their defaults and validation, plus the
// VeloDB connection (.env / environment).
package config

import (
	"fmt"
	"math"
	"strings"
)

// Strategy is strategy.toml: how the desk trades. Hot reloaded.
type Strategy struct {
	Capital CapitalConfig `toml:"capital"`
	Trading TradingConfig `toml:"trading"`
	Blocks  BlockConfig   `toml:"block_trades"`
	Extreme ExtremeConfig `toml:"extreme_trades"`
}

type CapitalConfig struct {
	StartingCashAmount         float64 `toml:"starting_cash_amount"`
	TargetCashRatio            float64 `toml:"target_cash_ratio"`
	RebalanceIntervalSeconds   int     `toml:"rebalance_interval_seconds"`
	InitialEquityRatio         float64 `toml:"initial_equity_ratio"`
	InitialPositionTradeAmount float64 `toml:"initial_position_trade_amount"`
}

type TradingConfig struct {
	TradesPerSecond            int     `toml:"trades_per_second"`
	TradeMinAmount             float64 `toml:"trade_min_amount"`
	TradeMaxAmount             float64 `toml:"trade_max_amount"`
	TradeAmountDistribution    string  `toml:"trade_amount_distribution"`
	BuyChance                  float64 `toml:"buy_chance"`
	BuyChanceStep              float64 `toml:"buy_chance_step"`
	BuyChanceMin               float64 `toml:"buy_chance_min"`
	BuyChanceMax               float64 `toml:"buy_chance_max"`
	OptimalTradeChance         float64 `toml:"optimal_trade_chance"`
	MaintainTargetWeight       bool    `toml:"maintain_target_weight"`
	OptimalTradeChanceWhenRich float64 `toml:"optimal_trade_chance_when_rich"`
	NavRichThresholdAmount     float64 `toml:"nav_rich_threshold_amount"`
	OptimalTradeChanceWhenPoor float64 `toml:"optimal_trade_chance_when_poor"`
	NavPoorThresholdAmount     float64 `toml:"nav_poor_threshold_amount"`
	TradeCostRate              float64 `toml:"trade_cost_rate"`
	TradeCostMinAmount         float64 `toml:"trade_cost_min_amount"`
}

type BlockConfig struct {
	BlockTradeChance               float64 `toml:"block_trade_chance"`
	BlockTradeSlices               int     `toml:"block_trade_slices"`
	BlockTradeSliceIntervalSeconds int     `toml:"block_trade_slice_interval_seconds"`
}

// Market is market.toml: the tick clock and the macro multiplier. Hot reloaded.
type Market struct {
	Price PriceConfig `toml:"price"`
	Macro MacroConfig `toml:"macro"`
}

type PriceConfig struct {
	TickIntervalSeconds int `toml:"tick_interval_seconds"`
}

// MacroConfig drives one multiplier applied to every quoted price. Same knobs
// as a stock: a bounded walk in [min, max] with trend step, noise and flip
// chance, plus a temporary pin. When the range is edited or a pin is set the
// multiplier walks to it at pin_step_percent per tick; it never jumps.
type MacroConfig struct {
	MinMultiplier       float64  `toml:"min_multiplier"`
	MaxMultiplier       float64  `toml:"max_multiplier"`
	TrendStepPercent    float64  `toml:"trend_step_percent"`
	NoisePercent        float64  `toml:"noise_percent"`
	DirectionFlipChance float64  `toml:"direction_flip_chance"`
	PinnedMultiplier    *float64 `toml:"pinned_multiplier"`
	PinStepPercent      float64  `toml:"pin_step_percent"`
}

// Stock is one stocks/<SYMBOL>.toml. Hot reloaded per file.
type Stock struct {
	Symbol              string   `toml:"symbol"`
	Name                string   `toml:"name"`
	Sector              string   `toml:"sector"`
	StartPrice          float64  `toml:"start_price"`
	Weight              float64  `toml:"weight"`
	TrendStepPercent    *float64 `toml:"trend_step_percent"`
	NoisePercent        *float64 `toml:"noise_percent"`
	DirectionFlipChance *float64 `toml:"direction_flip_chance"`
	PinnedPrice         *float64 `toml:"pinned_price"`     // temporary override: walk to this quote and wiggle there
	PinStepPercent      *float64 `toml:"pin_step_percent"` // per tick, toward the pin or back into the band
	BandPercent         *float64 `toml:"band_percent"`     // real band = anchor price ± this, when [real] is absent
	Real                *Band    `toml:"real"`             // explicit band; overrides band_percent
	Perceived           *Band    `toml:"perceived"`
}

// Per-stock defaults used when a key is absent from the file.
const (
	DefaultTrendStepPercent    = 0.01
	DefaultNoisePercent        = 0.30
	DefaultDirectionFlipChance = 0.02
	DefaultPinStepPercent      = 0.5
	DefaultBandPercent         = 0.50
)

type Band struct {
	MinPrice float64 `toml:"min_price"`
	MaxPrice float64 `toml:"max_price"`
}

// ResolvedStock is a Stock with every optional field filled from the defaults.
type ResolvedStock struct {
	Symbol, Name, Sector string
	StartPrice, Weight   float64
	TrendStepPercent     float64
	NoisePercent         float64
	DirectionFlipChance  float64
	PinnedPrice          float64 // 0 = not pinned
	PinStepPercent       float64
	BandPercent          float64
	Real, Perceived      Band // Real from start_price ± band_percent unless [real]; the sim re-anchors it (see RealIsDerived)
	RealIsDerived        bool // [real] absent: the sim anchors the band on the price the run starts from
	PerceivedIsReal      bool
}

func DefaultStrategy() Strategy {
	return Strategy{
		Capital: CapitalConfig{
			StartingCashAmount:         1_000_000_000_000,
			TargetCashRatio:            0.30,
			RebalanceIntervalSeconds:   30,
			InitialEquityRatio:         0.65,
			InitialPositionTradeAmount: 10_000_000,
		},
		Trading: TradingConfig{
			TradesPerSecond:            50,
			TradeMinAmount:             10_000,
			TradeMaxAmount:             1_000_000,
			TradeAmountDistribution:    "log-uniform",
			BuyChance:                  0.50,
			BuyChanceStep:              0.05,
			BuyChanceMin:               0.05,
			BuyChanceMax:               0.95,
			OptimalTradeChance:         0.60,
			MaintainTargetWeight:       true,
			OptimalTradeChanceWhenRich: 0.30,
			NavRichThresholdAmount:     2_000_000_000_000,
			OptimalTradeChanceWhenPoor: 0.70,
			NavPoorThresholdAmount:     500_000_000_000,
			TradeCostRate:              0.0001,
			TradeCostMinAmount:         0.01,
		},
		Blocks: BlockConfig{
			BlockTradeChance:               0.003,
			BlockTradeSlices:               5,
			BlockTradeSliceIntervalSeconds: 10,
		},
		Extreme: DefaultExtreme(),
	}
}

func DefaultMarket() Market {
	return Market{
		Price: PriceConfig{TickIntervalSeconds: 1},
		Macro: MacroConfig{
			MinMultiplier: 0.95, MaxMultiplier: 1.05,
			TrendStepPercent: 0.02, NoisePercent: 0.05, DirectionFlipChance: 0.02,
			PinStepPercent: 0.5,
		},
	}
}

func (s *Strategy) Validate() error {
	c, t, b := s.Capital, s.Trading, s.Blocks
	var errs []string
	chk := func(ok bool, msg string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Sprintf(msg, args...))
		}
	}
	chk(c.StartingCashAmount > 0, "capital.starting_cash_amount must be > 0")
	chk(c.TargetCashRatio > 0 && c.TargetCashRatio < 1, "capital.target_cash_ratio must be in (0,1)")
	chk(c.RebalanceIntervalSeconds >= 1, "capital.rebalance_interval_seconds must be >= 1")
	chk(c.InitialEquityRatio >= 0 && c.InitialEquityRatio < 1, "capital.initial_equity_ratio must be in [0,1)")
	chk(c.InitialPositionTradeAmount > 0, "capital.initial_position_trade_amount must be > 0")
	chk(t.TradesPerSecond >= 1 && t.TradesPerSecond <= 5000, "trading.trades_per_second must be in [1,5000]")
	chk(t.TradeMinAmount > 0 && t.TradeMaxAmount >= t.TradeMinAmount, "trading.trade_min_amount/trade_max_amount invalid")
	chk(t.TradeAmountDistribution == "log-uniform" || t.TradeAmountDistribution == "uniform", "trading.trade_amount_distribution must be log-uniform or uniform")
	chk(t.BuyChanceMin >= 0 && t.BuyChanceMax <= 1 && t.BuyChanceMin <= t.BuyChanceMax, "trading.buy_chance_min/max invalid")
	chk(t.BuyChance >= t.BuyChanceMin && t.BuyChance <= t.BuyChanceMax, "trading.buy_chance must be within [buy_chance_min, buy_chance_max]")
	chk(t.BuyChanceStep > 0 && t.BuyChanceStep < 1, "trading.buy_chance_step must be in (0,1)")
	chk(t.OptimalTradeChance >= 0 && t.OptimalTradeChance <= 1, "trading.optimal_trade_chance must be in [0,1]")
	chk(t.OptimalTradeChanceWhenRich >= 0 && t.OptimalTradeChanceWhenRich <= 1, "trading.optimal_trade_chance_when_rich must be in [0,1]")
	chk(t.OptimalTradeChanceWhenPoor >= 0 && t.OptimalTradeChanceWhenPoor <= 1, "trading.optimal_trade_chance_when_poor must be in [0,1]")
	chk(t.NavRichThresholdAmount > t.NavPoorThresholdAmount && t.NavPoorThresholdAmount >= 0, "trading.nav_rich_threshold_amount must be > nav_poor_threshold_amount >= 0")
	chk(t.TradeCostRate >= 0 && t.TradeCostRate < 0.1, "trading.trade_cost_rate must be in [0,0.1)")
	chk(t.TradeCostMinAmount >= 0, "trading.trade_cost_min_amount must be >= 0")
	chk(b.BlockTradeChance >= 0 && b.BlockTradeChance <= 1, "block_trades.block_trade_chance must be in [0,1]")
	chk(b.BlockTradeSlices >= 1 && b.BlockTradeSlices <= 3600, "block_trades.block_trade_slices must be in [1,3600]")
	chk(b.BlockTradeSliceIntervalSeconds >= 1, "block_trades.block_trade_slice_interval_seconds must be >= 1")
	errs = append(errs, s.Extreme.validate()...)
	if len(errs) > 0 {
		return fmt.Errorf("strategy.toml: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (m *Market) Validate() error {
	p := m.Price
	var errs []string
	chk := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, msg)
		}
	}
	chk(p.TickIntervalSeconds >= 1, "price.tick_interval_seconds must be >= 1")
	mc := m.Macro
	chk(mc.MinMultiplier > 0 && mc.MaxMultiplier >= mc.MinMultiplier, "macro.min_multiplier/max_multiplier must satisfy 0 < min <= max")
	chk(mc.TrendStepPercent >= 0 && mc.TrendStepPercent < 10, "macro.trend_step_percent must be in [0,10)")
	chk(mc.NoisePercent >= 0 && mc.NoisePercent < 10, "macro.noise_percent must be in [0,10)")
	chk(mc.DirectionFlipChance >= 0 && mc.DirectionFlipChance <= 1, "macro.direction_flip_chance must be in [0,1]")
	chk(mc.PinStepPercent > 0 && mc.PinStepPercent < 50, "macro.pin_step_percent must be in (0,50)")
	if mc.PinnedMultiplier != nil {
		chk(*mc.PinnedMultiplier > 0, "macro.pinned_multiplier must be > 0")
	}
	if len(errs) > 0 {
		return fmt.Errorf("market.toml: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Validate checks a stock file on its own (before resolution).
func (s *Stock) Validate() error {
	var errs []string
	chk := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, msg)
		}
	}
	chk(s.Symbol != "" && len(s.Symbol) <= 16 && !strings.ContainsAny(s.Symbol, " \t/\\"), "symbol must be 1-16 chars without spaces or slashes")
	chk(s.StartPrice > 0, "start_price must be > 0")
	chk(s.Weight >= 0, "weight must be >= 0")
	if s.TrendStepPercent != nil {
		chk(*s.TrendStepPercent >= 0 && *s.TrendStepPercent < 10, "trend_step_percent must be in [0,10)")
	}
	if s.NoisePercent != nil {
		chk(*s.NoisePercent >= 0 && *s.NoisePercent < 10, "noise_percent must be in [0,10)")
	}
	if s.DirectionFlipChance != nil {
		chk(*s.DirectionFlipChance >= 0 && *s.DirectionFlipChance <= 1, "direction_flip_chance must be in [0,1]")
	}
	if s.PinnedPrice != nil {
		chk(*s.PinnedPrice > 0, "pinned_price must be > 0")
	}
	if s.PinStepPercent != nil {
		chk(*s.PinStepPercent > 0 && *s.PinStepPercent < 50, "pin_step_percent must be in (0,50)")
	}
	if s.BandPercent != nil {
		chk(*s.BandPercent > 0 && *s.BandPercent < 1, "band_percent must be in (0,1)")
	}
	for name, b := range map[string]*Band{"real": s.Real, "perceived": s.Perceived} {
		if b != nil {
			chk(b.MinPrice > 0 && b.MaxPrice > b.MinPrice, name+".min_price/max_price must satisfy 0 < min < max")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", s.Symbol, strings.Join(errs, "; "))
	}
	return nil
}

// Resolve fills every optional field from the per-stock defaults.
func (s *Stock) Resolve() ResolvedStock {
	r := ResolvedStock{
		Symbol: s.Symbol, Name: s.Name, Sector: s.Sector,
		StartPrice: s.StartPrice, Weight: s.Weight,
		TrendStepPercent:    DefaultTrendStepPercent,
		NoisePercent:        DefaultNoisePercent,
		DirectionFlipChance: DefaultDirectionFlipChance,
		PinStepPercent:      DefaultPinStepPercent,
		BandPercent:         DefaultBandPercent,
	}
	if r.Name == "" {
		r.Name = s.Symbol
	}
	if r.Sector == "" {
		r.Sector = "Other"
	}
	if s.TrendStepPercent != nil {
		r.TrendStepPercent = *s.TrendStepPercent
	}
	if s.NoisePercent != nil {
		r.NoisePercent = *s.NoisePercent
	}
	if s.DirectionFlipChance != nil {
		r.DirectionFlipChance = *s.DirectionFlipChance
	}
	if s.PinnedPrice != nil {
		r.PinnedPrice = *s.PinnedPrice
	}
	if s.PinStepPercent != nil {
		r.PinStepPercent = *s.PinStepPercent
	}
	if s.BandPercent != nil {
		r.BandPercent = *s.BandPercent
	}
	if s.Real != nil {
		r.Real = *s.Real
	} else {
		r.Real = DerivedBand(s.StartPrice, r.BandPercent)
		r.RealIsDerived = true
	}
	if s.Perceived != nil {
		r.Perceived = *s.Perceived
	} else {
		r.Perceived = r.Real
		r.PerceivedIsReal = true
	}
	return r
}

// DerivedBand is anchor ± pct, in cents.
func DerivedBand(anchor, pct float64) Band {
	return Band{MinPrice: round2(anchor * (1 - pct)), MaxPrice: round2(anchor * (1 + pct))}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ExtremeConfig is [extreme_trades]: opportunistic all-in trades at band extremes.
type ExtremeConfig struct {
	SellAllAtHigh       bool    `toml:"sell_all_at_high"`
	SellAllBandPosition float64 `toml:"sell_all_band_position"`
	SellAllMaxCashRatio float64 `toml:"sell_all_max_cash_ratio"`
	SuckAllAtLow        bool    `toml:"suck_all_at_low"`
	SuckAllBandPosition float64 `toml:"suck_all_band_position"`
	SuckAllCashShare    float64 `toml:"suck_all_cash_share"`
	SuckAllMinCashRatio float64 `toml:"suck_all_min_cash_ratio"`

	// The money-losing mirrors: dump at the bottom, pile in at the top.
	PanicSellAtLow        bool    `toml:"panic_sell_at_low"`
	PanicSellBandPosition float64 `toml:"panic_sell_band_position"`
	PanicSellMaxCashRatio float64 `toml:"panic_sell_max_cash_ratio"`
	FomoBuyAtHigh         bool    `toml:"fomo_buy_at_high"`
	FomoBuyBandPosition   float64 `toml:"fomo_buy_band_position"`
	FomoBuyCashShare      float64 `toml:"fomo_buy_cash_share"`
	FomoBuyMinCashRatio   float64 `toml:"fomo_buy_min_cash_ratio"`
	ChancePerSecond       float64 `toml:"chance_per_second"`
}

func DefaultExtreme() ExtremeConfig {
	return ExtremeConfig{
		SellAllAtHigh: false, SellAllBandPosition: 0.95, SellAllMaxCashRatio: 0.50,
		SuckAllAtLow: false, SuckAllBandPosition: 0.05, SuckAllCashShare: 0.10, SuckAllMinCashRatio: 0.28,
		PanicSellAtLow: false, PanicSellBandPosition: 0.05, PanicSellMaxCashRatio: 0.50,
		FomoBuyAtHigh: false, FomoBuyBandPosition: 0.95, FomoBuyCashShare: 0.10, FomoBuyMinCashRatio: 0.28,
		ChancePerSecond: 0.10,
	}
}

func (x *ExtremeConfig) validate() []string {
	var errs []string
	chk := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, msg)
		}
	}
	chk(x.SellAllBandPosition > 0 && x.SellAllBandPosition <= 1.5, "extreme_trades.sell_all_band_position must be in (0,1.5]")
	chk(x.SellAllMaxCashRatio > 0 && x.SellAllMaxCashRatio <= 1, "extreme_trades.sell_all_max_cash_ratio must be in (0,1]")
	chk(x.SuckAllBandPosition >= -0.5 && x.SuckAllBandPosition < 1, "extreme_trades.suck_all_band_position must be in [-0.5,1)")
	chk(x.SuckAllCashShare > 0 && x.SuckAllCashShare <= 1, "extreme_trades.suck_all_cash_share must be in (0,1]")
	chk(x.SuckAllMinCashRatio >= 0 && x.SuckAllMinCashRatio < 1, "extreme_trades.suck_all_min_cash_ratio must be in [0,1)")
	chk(x.PanicSellBandPosition >= -0.5 && x.PanicSellBandPosition < 1, "extreme_trades.panic_sell_band_position must be in [-0.5,1)")
	chk(x.PanicSellMaxCashRatio > 0 && x.PanicSellMaxCashRatio <= 1, "extreme_trades.panic_sell_max_cash_ratio must be in (0,1]")
	chk(x.FomoBuyBandPosition > 0 && x.FomoBuyBandPosition <= 1.5, "extreme_trades.fomo_buy_band_position must be in (0,1.5]")
	chk(x.FomoBuyCashShare > 0 && x.FomoBuyCashShare <= 1, "extreme_trades.fomo_buy_cash_share must be in (0,1]")
	chk(x.FomoBuyMinCashRatio >= 0 && x.FomoBuyMinCashRatio < 1, "extreme_trades.fomo_buy_min_cash_ratio must be in [0,1)")
	chk(x.ChancePerSecond >= 0 && x.ChancePerSecond <= 1, "extreme_trades.chance_per_second must be in [0,1]")
	return errs
}
