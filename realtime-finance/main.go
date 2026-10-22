// finsim: realtime finance simulator for the Doris money-market demo.
//
//	finsim schema            apply schema.sql to the configured database
//	finsim gen-stocks        write stocks/*.toml (100 tickers); -force overwrites
//	finsim run [-reset]      run the simulator + dashboard; -reset wipes data and reseeds
package main

import (
	"context"
	"database/sql"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"finsim/internal/config"
	"finsim/internal/doris"
	"finsim/internal/sim"
	"finsim/internal/sink"
	"finsim/internal/web"

	"github.com/shopspring/decimal"
)

//go:embed schema.sql
var schemaSQL string

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "schema":
		err = cmdSchema(os.Args[2:])
	case "gen-stocks":
		err = cmdGenStocks(os.Args[2:])
	case "migrate-stocks":
		err = cmdMigrateStocks(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "fold":
		err = cmdFold(os.Args[2:])
	case "reset":
		err = cmdReset(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("error: %v", err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  finsim schema      [-dir DIR]                    apply schema to Doris
  finsim gen-stocks  [-dir DIR] [-force]           generate stocks/*.toml
  finsim migrate-stocks [-dir DIR] [-drop-real]    rewrite stocks/*.toml in the current layout, keeping values (-drop-real: derive bands at runtime)
  finsim run         [-dir DIR] [-reset] [-listen :8088] [-no-seed]
  finsim fold        [-dir DIR] [-keep-hours N]              fold+drop trades partitions older than N hours (one-off; run does this daily)
  finsim reset       [-dir DIR] [-force]           drop + recreate every table/MV/view in Doris; the next run seeds a fresh book. Stop the service first
DIR holds strategy.toml, market.toml, stocks/ and .env (default ".").
Connection: VELODB_* from the environment, else DIR/.env (ENV_FILE=<path> for another file; see env.example).`)
}

type paths struct{ dir, strategy, market, env, stocks string }

func newPaths(dir string) paths {
	return paths{
		dir: dir, strategy: filepath.Join(dir, "strategy.toml"), market: filepath.Join(dir, "market.toml"),
		env: envFile(dir), stocks: filepath.Join(dir, "stocks"),
	}
}

// envFile is $ENV_FILE, else DIR/.env, else the DIR/doris.env of older installs.
func envFile(dir string) string {
	if f := os.Getenv("ENV_FILE"); f != "" {
		return f
	}
	f := filepath.Join(dir, ".env")
	if old := filepath.Join(dir, "doris.env"); !fileExists(f) && fileExists(old) {
		return old
	}
	return f
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cmdSchema(args []string) error {
	fs := flag.NewFlagSet("schema", flag.ExitOnError)
	dir := fs.String("dir", ".", "project directory")
	fs.Parse(args)
	p := newPaths(*dir)
	env, err := config.LoadDorisEnv(p.env)
	if err != nil {
		return err
	}
	st, err := config.LoadStrategy(p.strategy)
	if err != nil {
		return err
	}
	db, err := doris.Open(env)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := doris.EnsureSchema(db, schemaSQL); err != nil {
		return err
	}
	cur, err := doris.StartingCash(db)
	if err != nil {
		return err
	}
	if cur.IsZero() {
		if err := doris.SetStartingCash(db, decimal.NewFromFloat(st.Capital.StartingCashAmount)); err != nil {
			return err
		}
	}
	log.Printf("schema applied to %s.%s", env.Host, env.Database)
	return nil
}

// cmdReset wipes the book: every view, materialized view and table is dropped
// and recreated from schema.sql, then starting cash is recorded from
// strategy.toml. The simulator's memory is the process, so it must be stopped
// first; a live simulator would keep trading its old positions against an empty
// database. Refuses when latest_price was written recently.
func cmdReset(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	dir := fs.String("dir", ".", "project directory")
	force := fs.Bool("force", false, "reset even if a simulator appears to be running")
	fs.Parse(args)
	p := newPaths(*dir)
	env, err := config.LoadDorisEnv(p.env)
	if err != nil {
		return err
	}
	st, err := config.LoadStrategy(p.strategy)
	if err != nil {
		return err
	}
	db, err := doris.Open(env)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := doris.EnsureSchema(db, schemaSQL); err != nil {
		return err
	}
	age, err := doris.LastTickAge(db)
	if err != nil {
		return err
	}
	const quiet = 15 * time.Second
	if age >= 0 && age < quiet && !*force {
		return fmt.Errorf("latest_price was written %s ago: a simulator looks to be running. Stop the service first (or use -force)", age.Round(time.Second))
	}
	log.Printf("reset: dropping every table, view and materialized view in %s.%s", env.Host, env.Database)
	if err := doris.DropAll(db); err != nil {
		return err
	}
	log.Printf("reset: recreating the schema")
	if err := doris.EnsureSchema(db, schemaSQL); err != nil {
		return err
	}
	cash := decimal.NewFromFloat(st.Capital.StartingCashAmount)
	if err := doris.SetStartingCash(db, cash); err != nil {
		return err
	}
	log.Printf("reset done: book is empty, starting cash %s; start the service to seed a fresh book", cash.StringFixed(0))
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dir := fs.String("dir", ".", "project directory")
	reset := fs.Bool("reset", false, "truncate all data in Doris and reseed the book")
	listen := fs.String("listen", ":8088", "dashboard listen address")
	noSeed := fs.Bool("no-seed", false, "do not seed an empty book")
	fs.Parse(args)
	p := newPaths(*dir)

	env, err := config.LoadDorisEnv(p.env)
	if err != nil {
		return err
	}
	db, err := doris.Open(env)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := doris.EnsureSchema(db, schemaSQL); err != nil {
		return err
	}
	if *reset {
		strategy, err := config.LoadStrategy(p.strategy)
		if err != nil {
			return err
		}
		log.Printf("reset: truncating data tables in %s", env.Database)
		if err := doris.Reset(db); err != nil {
			return err
		}
		if err := doris.SetStartingCash(db, decimal.NewFromFloat(strategy.Capital.StartingCashAmount)); err != nil {
			return err
		}
	}

	// The web server, the config watcher and the signal handler live for the
	// whole process. The book (engine + loader) is a "generation": the admin
	// reset tears the current one down, wipes Doris and starts a fresh one.
	srv := web.New(nil, db, nil, env.Database, p.dir, p.stocks)
	go func() {
		displayAddr := *listen
		if strings.HasPrefix(displayAddr, ":") {
			displayAddr = "localhost" + displayAddr
		}
		log.Printf("dashboard on http://%s%s/", displayAddr, web.BasePath)
		if err := srv.ListenAndServe(*listen); err != nil {
			log.Fatalf("web server: %v", err)
		}
	}()
	withEngine := func(f func(*sim.Engine)) {
		if e := srv.Engine(); e != nil {
			f(e)
		}
	}
	watcher := &config.Watcher{
		Dir: p.dir, StockDir: p.stocks,
		OnStrategy: func(c *config.Strategy) { withEngine(func(e *sim.Engine) { e.ReloadStrategy(c) }) },
		OnMarket:   func(c *config.Market) { withEngine(func(e *sim.Engine) { e.ReloadMarket(c) }) },
		OnStock:    func(c *config.Stock) { withEngine(func(e *sim.Engine) { e.ReloadStock(c) }) },
		OnDelete:   func(sym string) { withEngine(func(e *sim.Engine) { e.DelistStock(sym) }) },
		OnError:    func(path string, err error) { withEngine(func(e *sim.Engine) { e.ReloadError(path, err) }) },
	}
	if err := watcher.Start(); err != nil {
		return fmt.Errorf("config watcher: %w", err)
	}

	var (
		mu        sync.Mutex
		genCancel context.CancelFunc
		resetting bool
		exiting   bool
	)
	stopGeneration := func(forReset bool) error {
		mu.Lock()
		defer mu.Unlock()
		if resetting {
			return fmt.Errorf("a reset is already in progress")
		}
		if forReset {
			resetting = true
		} else {
			exiting = true
		}
		if genCancel != nil {
			genCancel()
		}
		return nil
	}
	srv.SetResetFunc(func() error { return stopGeneration(true) })
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sig
		log.Printf("%s: stopping simulator and flushing Doris loads...", s)
		_ = stopGeneration(false)
	}()

	for {
		ctx, cancel := context.WithCancel(context.Background())
		mu.Lock()
		genCancel = cancel
		mu.Unlock()
		err := runGeneration(ctx, p, env, db, srv, *noSeed)
		cancel()
		if err != nil {
			return err
		}
		mu.Lock()
		doReset, done := resetting, exiting
		mu.Unlock()
		if done || !doReset {
			break
		}
		log.Printf("reset: dropping every table, view and materialized view in %s.%s", env.Host, env.Database)
		if err := doris.DropAll(db); err != nil {
			return fmt.Errorf("reset: %w", err)
		}
		if err := doris.EnsureSchema(db, schemaSQL); err != nil {
			return fmt.Errorf("reset: recreate schema: %w", err)
		}
		strategy, err := config.LoadStrategy(p.strategy)
		if err != nil {
			return fmt.Errorf("reset: %w", err)
		}
		if err := doris.SetStartingCash(db, decimal.NewFromFloat(strategy.Capital.StartingCashAmount)); err != nil {
			return fmt.Errorf("reset: %w", err)
		}
		log.Printf("reset: schema recreated, starting cash %s; seeding a fresh book", decimal.NewFromFloat(strategy.Capital.StartingCashAmount).StringFixed(0))
		mu.Lock()
		resetting = false
		mu.Unlock()
	}
	log.Printf("stopped")
	return nil
}

// runGeneration loads the configuration, builds the loader and the engine,
// resumes from Doris (or seeds an empty book), serves it until ctx is
// cancelled, then flushes and closes the loader.
func runGeneration(ctx context.Context, p paths, env *config.DorisEnv, db *sql.DB, srv *web.Server, noSeed bool) error {
	strategy, err := config.LoadStrategy(p.strategy)
	if err != nil {
		return err
	}
	market, err := config.LoadMarket(p.market)
	if err != nil {
		return err
	}
	stocks, errs := config.LoadStocks(p.stocks)
	for _, e := range errs {
		log.Printf("stock config skipped: %v", e)
	}
	if len(stocks) == 0 {
		return fmt.Errorf("no valid stock files in %s (run `finsim gen-stocks`)", p.stocks)
	}

	// Starting cash: the recorded value wins on resume so v_pnl and the simulator agree.
	recorded, err := doris.StartingCash(db)
	if err != nil {
		return err
	}
	fromToml := decimal.NewFromFloat(strategy.Capital.StartingCashAmount)
	if recorded.IsZero() {
		recorded = fromToml
		if err := doris.SetStartingCash(db, recorded); err != nil {
			return err
		}
	} else if !recorded.Equal(fromToml) {
		log.Printf("WARNING: strategy.toml starting_cash_amount=%s but the book was started with %s; using the recorded value (reset to start over)", fromToml, recorded)
		strategy.Capital.StartingCashAmount, _ = recorded.Float64()
	}

	snk, err := sink.New(env, sim.TableColumns)
	if err != nil {
		return err
	}
	defer snk.Close()
	engine := sim.New(strategy, market, stocks, snk)

	state, err := doris.LoadState(ctx, db)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if state.Trades > 0 {
		positions := map[string]sim.RestoredPosition{}
		for sym, ps := range state.Positions {
			positions[sym] = sim.RestoredPosition{Qty: ps.Qty, AvgCost: ps.AvgCost}
		}
		cash := recorded.Sub(state.Bought).Add(state.Sold).Sub(state.TradeCost).Sub(state.PaidOut)
		engine.Restore(state.Prices, positions, cash, state.TradeCost, state.Realized, state.PaidOut, state.PaidOutForgotten, state.Trades, state.MaxTradeID, state.MaxBlockID, state.MaxCashoutID)
		log.Printf("resumed from Doris: %d trades, %d positions, %d prices, cash %s (paid out %s, of which forgotten %s), next trade_id %d",
			state.Trades, len(positions), len(state.Prices), cash.StringFixed(2), state.PaidOut.StringFixed(0), state.PaidOutForgotten.StringFixed(0), state.MaxTradeID+1)
		srv.Attach(engine, snk)
	} else {
		srv.Attach(engine, snk)
		if !noSeed {
			n := engine.Seed(time.Now().UTC())
			log.Printf("seeded the book with %d opening trades; waiting for them to land in Doris...", n)
			snk.Flush(3 * time.Minute)
			doris.RefreshMVs(db)
		}
	}
	defer srv.Detach()

	go engine.Run(ctx)
	go func() { // housekeeping: trim price and snapshot history so the demo can run forever
		ret := doris.Retention{Prices: time.Duration(env.PricesRetentionHours) * time.Hour, BookSnapshots: time.Duration(env.SnapshotsRetentionDays) * 24 * time.Hour}
		for {
			doris.DropExpiredPartitions(db, ret)
			if env.TradesRetentionDays > 0 {
				foldTrades(db, engine.AllocTradeIDs, snk, time.Duration(env.TradesRetentionDays)*24*time.Hour)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Minute):
			}
		}
	}()
	log.Printf("simulator running: %d stocks, %d trades/s, tick every %ds", len(stocks), strategy.Trading.TradesPerSecond, market.Price.TickIntervalSeconds)

	<-ctx.Done()
	srv.Detach()
	snk.Flush(30 * time.Second)
	return nil
}

// foldTrades folds trades partitions older than the retention window into
// carry-forward rows at the start of the oldest kept partition, waits for them
// to land, then drops the old partitions and refreshes the MVs. Nothing is
// dropped unless the carry rows are confirmed visible in Doris.
func foldTrades(db *sql.DB, alloc func(n int64) int64, snk *sink.Sink, keep time.Duration) {
	parts, err := doris.Partitions(db, "trades", time.Hour)
	if err != nil {
		log.Printf("fold: list partitions: %v", err)
		return
	}
	cutoff := time.Now().UTC().Add(-keep)
	var expired []string
	var boundary time.Time
	for _, p := range parts {
		if p.End.Before(cutoff) {
			expired = append(expired, p.Name)
		} else if boundary.IsZero() {
			boundary = p.Start
		}
	}
	if len(expired) == 0 || boundary.IsZero() {
		return
	}
	carries, err := doris.SummarizeTrades(db, expired)
	if err != nil {
		log.Printf("fold: summarize: %v", err)
		return
	}
	n := int64(0)
	for _, c := range carries {
		if c.BuyQty > 0 {
			n++
		}
		if c.SellQty > 0 {
			n++
		}
	}
	first := alloc(n)
	id := first
	ts := sim.DorisTime(boundary)
	for _, c := range carries {
		if c.BuyQty > 0 {
			snk.Emit(sim.TableTrades, sim.TradeRow{TS: ts, TradeID: id, Symbol: c.Symbol, Side: sim.SideBuy, Qty: c.BuyQty, Price: c.BuyNotional.Div(decimal.NewFromInt(c.BuyQty)).Round(6),
				Notional: c.BuyNotional, TradeCost: c.BuyCost, AvgCostAfter: c.AvgCostAfter, DecisionPrice: c.BuyNotional.Div(decimal.NewFromInt(c.BuyQty)).Round(6), CashAfter: c.CashAfter, IsSeed: true})
			id++
		}
		if c.SellQty > 0 {
			snk.Emit(sim.TableTrades, sim.TradeRow{TS: ts, TradeID: id, Symbol: c.Symbol, Side: sim.SideSell, Qty: c.SellQty, Price: c.SellNotional.Div(decimal.NewFromInt(c.SellQty)).Round(6),
				Notional: c.SellNotional, TradeCost: c.SellCost, RealizedPnl: c.Realized, AvgCostAfter: c.AvgCostAfter, DecisionPrice: c.SellNotional.Div(decimal.NewFromInt(c.SellQty)).Round(6), CashAfter: c.CashAfter, IsSeed: true})
			id++
		}
	}
	snk.Flush(2 * time.Minute)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		got, err := doris.CountTradesFrom(db, first)
		if err == nil && got >= n {
			break
		}
		if time.Now().After(deadline) {
			log.Printf("fold: carry rows not visible after 3 min (%d/%d); partitions kept, will retry later", got, n)
			return
		}
		time.Sleep(5 * time.Second)
	}
	if err := doris.DropPartitions(db, "trades", expired); err != nil {
		log.Printf("fold: %v", err)
		return
	}
	doris.RefreshMVs(db)
	log.Printf("fold: %d trades partitions before %s folded into %d carry rows at %s (trade_id %d..%d) and dropped",
		len(expired), cutoff.Format("2006-01-02 15:04"), n, boundary.Format("2006-01-02 15:04"), first, id-1)
}

// cmdFold runs one fold pass by hand (the simulator must not be running against
// the same database at the same time, or trade ids could collide).
func cmdFold(args []string) error {
	fs := flag.NewFlagSet("fold", flag.ExitOnError)
	dir := fs.String("dir", ".", "project directory")
	keepHours := fs.Int("keep-hours", 7*24, "keep partitions younger than this")
	fs.Parse(args)
	p := newPaths(*dir)
	env, err := config.LoadDorisEnv(p.env)
	if err != nil {
		return err
	}
	db, err := doris.Open(env)
	if err != nil {
		return err
	}
	defer db.Close()
	state, err := doris.LoadState(context.Background(), db)
	if err != nil {
		return err
	}
	next := state.MaxTradeID + 1
	alloc := func(n int64) int64 { first := next; next += n; return first }
	snk, err := sink.New(env, sim.TableColumns)
	if err != nil {
		return err
	}
	defer snk.Close()
	foldTrades(db, alloc, snk, time.Duration(*keepHours)*time.Hour)
	return nil
}
