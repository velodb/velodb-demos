package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"finsim/internal/config"
)

// stockSeed is one row of the built-in universe. capB is an approximate market
// cap in $B used to derive weight (cap / 100), so NVDA ≈ 34 and the tail ≈ 0.5.
type stockSeed struct {
	symbol, name, sector string
	price, capB          float64
}

var universe = []stockSeed{
	{"NVDA", "NVIDIA Corp.", "Technology", 128.00, 3400}, {"AAPL", "Apple Inc.", "Technology", 232.00, 3300},
	{"MSFT", "Microsoft Corp.", "Technology", 415.00, 3200}, {"GOOGL", "Alphabet Inc.", "Communication", 168.00, 2100},
	{"AMZN", "Amazon.com Inc.", "Consumer Discretionary", 186.00, 2000}, {"META", "Meta Platforms Inc.", "Communication", 560.00, 1400},
	{"AVGO", "Broadcom Inc.", "Technology", 172.00, 800}, {"TSLA", "Tesla Inc.", "Consumer Discretionary", 250.00, 800},
	{"BRK-B", "Berkshire Hathaway Class B", "Financials", 455.00, 980}, {"LLY", "Eli Lilly and Co.", "Health Care", 900.00, 850},
	{"JPM", "JPMorgan Chase & Co.", "Financials", 215.00, 620}, {"V", "Visa Inc.", "Financials", 285.00, 560},
	{"UNH", "UnitedHealth Group Inc.", "Health Care", 580.00, 530}, {"XOM", "Exxon Mobil Corp.", "Energy", 118.00, 520},
	{"MA", "Mastercard Inc.", "Financials", 500.00, 460}, {"ORCL", "Oracle Corp.", "Technology", 145.00, 400},
	{"COST", "Costco Wholesale Corp.", "Consumer Staples", 900.00, 400}, {"PG", "Procter & Gamble Co.", "Consumer Staples", 172.00, 400},
	{"HD", "Home Depot Inc.", "Consumer Discretionary", 405.00, 400}, {"JNJ", "Johnson & Johnson", "Health Care", 162.00, 390},
	{"WMT", "Walmart Inc.", "Consumer Staples", 80.00, 640}, {"ABBV", "AbbVie Inc.", "Health Care", 195.00, 340},
	{"NFLX", "Netflix Inc.", "Communication", 700.00, 300}, {"BAC", "Bank of America Corp.", "Financials", 40.00, 310},
	{"CRM", "Salesforce Inc.", "Technology", 270.00, 260}, {"KO", "Coca-Cola Co.", "Consumer Staples", 70.00, 300},
	{"CVX", "Chevron Corp.", "Energy", 150.00, 270}, {"MRK", "Merck & Co. Inc.", "Health Care", 115.00, 290},
	{"AMD", "Advanced Micro Devices", "Technology", 155.00, 250}, {"PEP", "PepsiCo Inc.", "Consumer Staples", 170.00, 230},
	{"TMO", "Thermo Fisher Scientific", "Health Care", 610.00, 230}, {"LIN", "Linde plc", "Materials", 470.00, 220},
	{"ADBE", "Adobe Inc.", "Technology", 520.00, 230}, {"CSCO", "Cisco Systems Inc.", "Technology", 52.00, 210},
	{"MCD", "McDonald's Corp.", "Consumer Discretionary", 300.00, 210},
	{"ABT", "Abbott Laboratories", "Health Care", 115.00, 200}, {"WFC", "Wells Fargo & Co.", "Financials", 58.00, 200},
	{"TMUS", "T-Mobile US Inc.", "Communication", 205.00, 240}, {"GE", "GE Aerospace", "Industrials", 180.00, 200},
	{"INTU", "Intuit Inc.", "Technology", 640.00, 180}, {"QCOM", "Qualcomm Inc.", "Technology", 170.00, 190},
	{"DHR", "Danaher Corp.", "Health Care", 270.00, 200}, {"TXN", "Texas Instruments Inc.", "Technology", 205.00, 190},
	{"IBM", "International Business Machines", "Technology", 225.00, 200}, {"CAT", "Caterpillar Inc.", "Industrials", 370.00, 180},
	{"AMGN", "Amgen Inc.", "Health Care", 330.00, 180}, {"PM", "Philip Morris International", "Consumer Staples", 125.00, 190},
	{"AXP", "American Express Co.", "Financials", 265.00, 190}, {"NOW", "ServiceNow Inc.", "Technology", 880.00, 180},
	{"ISRG", "Intuitive Surgical Inc.", "Health Care", 490.00, 175}, {"GS", "Goldman Sachs Group Inc.", "Financials", 500.00, 160},
	{"MS", "Morgan Stanley", "Financials", 105.00, 170}, {"NEE", "NextEra Energy Inc.", "Utilities", 80.00, 165},
	{"AMAT", "Applied Materials Inc.", "Technology", 195.00, 160}, {"SPGI", "S&P Global Inc.", "Financials", 510.00, 160},
	{"DIS", "Walt Disney Co.", "Communication", 95.00, 170}, {"UBER", "Uber Technologies Inc.", "Industrials", 75.00, 155},
	{"CMCSA", "Comcast Corp.", "Communication", 41.00, 160}, {"RTX", "RTX Corp.", "Industrials", 120.00, 160},
	{"PFE", "Pfizer Inc.", "Health Care", 29.00, 165}, {"T", "AT&T Inc.", "Communication", 21.00, 150},
	{"LOW", "Lowe's Companies Inc.", "Consumer Discretionary", 265.00, 150}, {"HON", "Honeywell International", "Industrials", 210.00, 140},
	{"BKNG", "Booking Holdings Inc.", "Consumer Discretionary", 4200.00, 145}, {"UNP", "Union Pacific Corp.", "Industrials", 245.00, 150},
	{"BLK", "BlackRock Inc.", "Financials", 940.00, 140}, {"VZ", "Verizon Communications", "Communication", 43.00, 180},
	{"C", "Citigroup Inc.", "Financials", 63.00, 120}, {"SCHW", "Charles Schwab Corp.", "Financials", 70.00, 130},
	{"LMT", "Lockheed Martin Corp.", "Industrials", 560.00, 130}, {"ETN", "Eaton Corp. plc", "Industrials", 310.00, 125},
	{"COP", "ConocoPhillips", "Energy", 108.00, 125},
	{"VRTX", "Vertex Pharmaceuticals", "Health Care", 470.00, 120}, {"BMY", "Bristol-Myers Squibb", "Health Care", 50.00, 100},
	{"PANW", "Palo Alto Networks Inc.", "Technology", 360.00, 120}, {"ADI", "Analog Devices Inc.", "Technology", 225.00, 110},
	{"DE", "Deere & Co.", "Industrials", 410.00, 110}, {"MU", "Micron Technology Inc.", "Technology", 100.00, 110},
	{"PLD", "Prologis Inc.", "Real Estate", 125.00, 115}, {"GILD", "Gilead Sciences Inc.", "Health Care", 84.00, 105},
	{"MDT", "Medtronic plc", "Health Care", 90.00, 115}, {"REGN", "Regeneron Pharmaceuticals", "Health Care", 1050.00, 110},
	{"SBUX", "Starbucks Corp.", "Consumer Discretionary", 95.00, 105}, {"LRCX", "Lam Research Corp.", "Technology", 78.00, 100},
	{"KLAC", "KLA Corp.", "Technology", 790.00, 105}, {"PLTR", "Palantir Technologies", "Technology", 38.00, 90},
	{"AMT", "American Tower Corp.", "Real Estate", 225.00, 100}, {"INTC", "Intel Corp.", "Technology", 22.00, 95},
	{"MDLZ", "Mondelez International", "Consumer Staples", 72.00, 95}, {"SNPS", "Synopsys Inc.", "Technology", 520.00, 80},
	{"CDNS", "Cadence Design Systems", "Technology", 275.00, 75}, {"CME", "CME Group Inc.", "Financials", 220.00, 80},
	{"MO", "Altria Group Inc.", "Consumer Staples", 52.00, 90}, {"DUK", "Duke Energy Corp.", "Utilities", 112.00, 85},
	{"SO", "Southern Co.", "Utilities", 88.00, 95}, {"SHOP", "Shopify Inc.", "Technology", 80.00, 100}, {"NKE", "Nike Inc.", "Consumer Discretionary", 82.00, 120}, {"UPS", "United Parcel Service", "Industrials", 130.00, 110},
	{"BA", "Boeing Co.", "Industrials", 160.00, 120}, {"PYPL", "PayPal Holdings Inc.", "Financials", 78.00, 80}}

// sector personality: (band %, noise %, trend step %)
func cmdGenStocks(args []string) error {
	fs := flag.NewFlagSet("gen-stocks", flag.ExitOnError)
	dir := fs.String("dir", ".", "project directory")
	force := fs.Bool("force", false, "overwrite existing files")
	fs.Parse(args)
	stockDir := filepath.Join(*dir, "stocks")
	if err := os.MkdirAll(stockDir, 0o755); err != nil {
		return err
	}
	if len(universe) != 100 {
		return fmt.Errorf("universe has %d entries, expected 100", len(universe))
	}
	written, skipped := 0, 0
	for _, s := range universe {
		path := filepath.Join(stockDir, s.symbol+".toml")
		if _, err := os.Stat(path); err == nil && !*force {
			skipped++
			continue
		}
		if err := os.WriteFile(path, []byte(stockTOML(s)), 0o644); err != nil {
			return err
		}
		written++
	}
	log.Printf("gen-stocks: %d written, %d kept (use -force to overwrite) in %s", written, skipped, stockDir)
	return nil
}

func stockTOML(s stockSeed) string {
	weight := math.Round(s.capB/100*10) / 10
	if weight < 0.5 {
		weight = 0.5
	}
	// Per-stock character, seeded by the symbol so regeneration is stable:
	// trend step log-uniform in [0.01, 0.5] % per tick (middle of a ±50% band to its
	// edge in ~3 min to ~3 h), noise = 3× the step capped at 0.5 %, and a direction
	// flip chance log-uniform in [0.0001, 0.01] (a flip every ~100–10,000 ticks).
	// The band is derived at runtime: anchor price ± band_percent (default 50%).
	h := fnv.New64a()
	h.Write([]byte(s.symbol))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	trend := math.Pow(10, -2+math.Log10(50)*rng.Float64()) // 0.01 .. 0.5
	trend = math.Round(trend*1e4) / 1e4
	noise := math.Min(math.Round(3*trend*1e4)/1e4, 0.5)
	flip := math.Pow(10, -4+2*rng.Float64())
	flip = math.Round(flip*1e6) / 1e6
	return renderStock(&config.Stock{
		Symbol: s.symbol, Name: s.name, Sector: s.sector, StartPrice: s.price, Weight: weight,
		TrendStepPercent: &trend, NoisePercent: &noise, DirectionFlipChance: &flip,
	})
}

// renderStock writes a stock file in the canonical layout. Every price parameter
// is spelled out so the file is self-contained; a pin is written only when set.
func renderStock(c *config.Stock) string {
	r := c.Resolve()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s (%s). Hot reloaded: save this file and the change applies within a second.\n", c.Symbol, r.Name, r.Sector)
	fmt.Fprintf(&b, "symbol      = %q\n", c.Symbol)
	fmt.Fprintf(&b, "name        = %q\n", r.Name)
	fmt.Fprintf(&b, "sector      = %q\n", r.Sector)
	fmt.Fprintf(&b, "start_price = %.2f      # used only when Doris has no price for this symbol yet\n", c.StartPrice)
	fmt.Fprintf(&b, "weight      = %.1f      # relative chance of being traded; also the seed allocation\n", c.Weight)
	b.WriteString("\n# Price engine for this stock (delete a line to use the default in the comment).\n")
	fmt.Fprintf(&b, "trend_step_percent    = %s   # per tick, 0..this %% in the trend direction (default %g)\n", trimF(r.TrendStepPercent), config.DefaultTrendStepPercent)
	fmt.Fprintf(&b, "noise_percent         = %s   # ± per tick, independent wiggle (default %g)\n", trimF(r.NoisePercent), config.DefaultNoisePercent)
	fmt.Fprintf(&b, "direction_flip_chance = %s   # per tick; the band edge always flips (default %g)\n", trimF(r.DirectionFlipChance), config.DefaultDirectionFlipChance)
	b.WriteString("\n# Temporary override: the quote walks to pinned_price at pin_step_percent per tick and wiggles there.\n")
	b.WriteString("# Above the pin it falls, below it rises; the [real] band is ignored while pinned. Remove to resume.\n")
	if c.PinnedPrice != nil {
		fmt.Fprintf(&b, "pinned_price     = %.2f\n", *c.PinnedPrice)
	} else {
		fmt.Fprintf(&b, "# pinned_price     = %.2f\n", math.Round(c.StartPrice*1.3*100)/100)
	}
	if c.PinStepPercent != nil {
		fmt.Fprintf(&b, "pin_step_percent = %s   # default %g; also the speed a price walks back into a narrowed band\n", trimF(*c.PinStepPercent), config.DefaultPinStepPercent)
	} else {
		fmt.Fprintf(&b, "# pin_step_percent = %g   # also the speed a price walks back into a narrowed band\n", config.DefaultPinStepPercent)
	}
	b.WriteString("\n# Real band: what the market does, the price bounces inside it. By default it is derived at start-up\n")
	b.WriteString("# as anchor ± band_percent, where anchor is the latest price in Doris (else start_price).\n")
	if c.BandPercent != nil {
		fmt.Fprintf(&b, "band_percent = %s\n", trimF(*c.BandPercent))
	} else {
		fmt.Fprintf(&b, "band_percent = %g\n", config.DefaultBandPercent)
	}
	if c.Real != nil {
		b.WriteString("\n[real]                     # explicit band; overrides band_percent\n")
		fmt.Fprintf(&b, "min_price = %.2f\n", c.Real.MinPrice)
		fmt.Fprintf(&b, "max_price = %.2f\n", c.Real.MaxPrice)
	} else {
		b.WriteString("\n# [real]                   # explicit band instead of band_percent\n")
		fmt.Fprintf(&b, "# min_price = %.2f\n", r.Real.MinPrice)
		fmt.Fprintf(&b, "# max_price = %.2f\n", r.Real.MaxPrice)
	}
	b.WriteString("\n")
	if c.Perceived != nil {
		b.WriteString("[perceived]                # what the trading app believes. Delete the section = same as [real].\n")
		fmt.Fprintf(&b, "min_price = %.2f\n", c.Perceived.MinPrice)
		fmt.Fprintf(&b, "max_price = %.2f\n", c.Perceived.MaxPrice)
	} else {
		b.WriteString("# [perceived]              # what the trading app believes. Omitted = same as [real].\n")
		fmt.Fprintf(&b, "# min_price = %.2f         # Shift it up and the app buys near the real top;\n", math.Round(r.Real.MinPrice*1.1*100)/100)
		fmt.Fprintf(&b, "# max_price = %.2f         # shift it down and it sells too early.\n", math.Round(r.Real.MaxPrice*1.1*100)/100)
	}
	return b.String()
}

func trimF(f float64) string { return fmt.Sprintf("%.4g", f) }

// cmdMigrateStocks rewrites every stocks/*.toml in the canonical layout, keeping
// every value the file already has. Used after a config-format change.
// -drop-real removes explicit [real] sections so the band is derived at runtime.
func cmdMigrateStocks(args []string) error {
	fs := flag.NewFlagSet("migrate-stocks", flag.ExitOnError)
	dir := fs.String("dir", ".", "project directory")
	dropReal := fs.Bool("drop-real", false, "remove [real] sections: the band becomes anchor ± band_percent")
	fs.Parse(args)
	stockDir := filepath.Join(*dir, "stocks")
	stocks, errs := config.LoadStocks(stockDir)
	for _, e := range errs {
		log.Printf("migrate-stocks: skipped: %v", e)
	}
	for _, c := range stocks {
		if *dropReal {
			c.Real = nil
		}
		path := filepath.Join(stockDir, c.Symbol+".toml")
		if err := os.WriteFile(path, []byte(renderStock(c)), 0o644); err != nil {
			return err
		}
	}
	log.Printf("migrate-stocks: %d rewritten, %d skipped in %s", len(stocks), len(errs), stockDir)
	return nil
}
