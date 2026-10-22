# Realtime Finance Demo (ADIA-style book on Apache Doris) — Design v3

Status: IMPLEMENTED and verified against VeloDB Cloud (2026-09-08). See README.md to run. Language: Go (single binary `finsim`). Target: Doris on VeloDB Cloud.

## 1. Goal

Simulate a sovereign-fund trading book and show it live in Doris:

* 100 stocks, one price tick per stock per second: a directional walk with noise inside a
  **real** band; the trading app judges "cheap / expensive" against a **perceived** band that
  by default equals the real one and can be changed per stock to make the app misjudge.
* Firm trades at spot with $10K–$1M tickets, pays a clearing fee of 0.01% (min $0.01),
  never sells more than it holds.
* Book starts with $1T cash, targets 30% cash / 70% equity, calibrated every 30s.
* Prices and positions are kept in memory for the simulator; Doris computes positions,
  cash and itemised P&L from the real trade rows, with async materialized views (1 min).
* Full price history and full trade history are kept, so position per ticker over time can be shown.
* Per-stock config files (all price behaviour, incl. a temporary `pinned_price`), `strategy.toml`
  and `market.toml` (macro multiplier) are **hot reloaded**; nothing ever jumps, it walks.
* All money is decimal, 6 decimal places, in Go and in Doris. Never float for money.
* A live dashboard on top (served by the same Go binary) — this is a showcase demo.

## 2. Issue tracker

| # | Issue | Status | Resolution |
|---|-------|--------|------------|
| W1 | Money printer | **Fixed, tunable** | Default: perceived band = real band, so the rule genuinely buys low / sells high. Against it: `optimal_trade_chance` (0.60, the rest trade the wrong half), the 0.01% clearing fee on every trade, and block-trade impact. Edit a stock's `[perceived]` band to make the app misjudge that name; hot reload applies it immediately. |
| W2 | Ticket size vs $1T book | **Open, accepted with numbers** | Log-uniform $10K–$1M tickets average ≈ $215K. Seeding is mandatory. At 50 trades/s the flow is ≈ 4% of the book per hour, so the calibrator visibly moves `buy_chance` but the cash ratio mostly follows prices (§3.7). |
| W3 | Sell-size percentage rule | **Fixed** | Dropped. Sell qty = min(ticket / price, available holding). If nothing is eligible, that scheduling turn is skipped; over-selling is capped, never an error. |
| W4 | Synthetic-looking prices | **Fixed** | Trend step + ±noise per tick, 2% flip chance per tick (global), 100% flip at the band edge, cent rounding on output only (§3.1). |
| W5 | Equal-weight boredom | **Fixed** | `weight` per stock drives seed allocation, buy selection and volume (§3.5). |
| W6 | No candidates | Rule | Preserve the rolled side and optimality. Select randomly from eligible unlocked symbols in the requested half; if there are none, skip that scheduling turn. |
| W7 | "Cash fluctuates" | Assumption | Cash changes only via trades and fees. |
| W8 | One-way calibration | Assumption | Symmetric ±`buy_chance_step`, clamped. |
| W9 | Live vs 1-minute MV | By design | Simulator state is in memory and instant; Doris MVs lag ≤ 60s; dashboard shows both. |
| W10 | Restart | **Verified** | Default start **resumes**: last price per symbol from Doris, positions and cash rebuilt from `trades`, direction re-drawn 50/50. `--reset` truncates and reseeds. |
| W11 | Integer shares | Fine | Recorded notional = qty × price. |
| N1 | Hot-reload edge cases | Rule | §3.8. |
| N2 | Connectivity | **Verified 2026-09-08** | MySQL on 9030 and Stream Load on 8080 through the load balancer both work. Cluster is VeloDB Cloud `cloud-26.1.2` (Doris cloud mode); `CREATE MATERIALIZED VIEW ... REFRESH COMPLETE ON SCHEDULE EVERY 1 MINUTE` accepted and tested. Do not set `replication_num` (cloud mode manages it). |

## 3. Simulation model

### 3.1 Price engine (per stock, every second)

Every price parameter is per stock, in `stocks/<SYMBOL>.toml`, with a built-in default when a
key is absent. `market.toml` only holds the tick clock and the **macro multiplier** (§3.1a).

State per stock: `walk` (own price, decimal, 6 dp), `dir ∈ {+1, −1}`, real band `[min, max]`,
optional `pinned_price`.

**Band.** Unless the file has an explicit `[real]` section, the band is derived at start-up as
`anchor ± band_percent` (default 50%), where anchor is the latest price in Doris for the symbol,
else `start_price`. So every restart re-centres the band on where the stock actually is, and the
direction is drawn 50/50. `[perceived]` defaults to the derived band too.

**Character.** The generator gives each stock a trend step log-uniform in [0.01, 0.5] % per tick
(seeded by the symbol; the average move is half the setting, so the middle of a ±50% band reaches an
edge in ~3 min at 0.5 and ~3 h at 0.01), noise = 3 × step capped at 0.5 %, and `direction_flip_chance` log-uniform in
[0.0001, 0.01], i.e. a random reversal every ~100–10,000 ticks on top of the guaranteed turn at the
band edge. Identical settings make every stock sit in the same half at the same time; the spread is
what keeps the map mixed. Every value stays editable per file.

```
start:   walk = last price in Doris for the symbol, else start_price from the file
         dir  = +1 or −1 with 50% each (also re-drawn on hot reload of that stock)
tick:    noise = rand(-noise_percent, +noise_percent)             # ±0.3% wiggle, always applied
         if pinned_price set:                                       # temporary override
             target = pinned_price / macro
             walk  += ±min(|target − walk|, walk × pin_step_percent)   # 0.5%/tick toward the pin
             dir    = sign of the move; walk = walk × (1 + noise)     # then wiggles around the pin
         elif walk outside [min, max]:                              # band was narrowed by a reload
             walk moves toward the nearest edge at pin_step_percent per tick, then noise
         else:
             if rand < direction_flip_chance (2%):  dir = -dir
             trend = dir * rand(0, trend_step_percent)             # 0..0.01% in the trend direction
             walk  = walk * (1 + trend) * (1 + noise)
             if walk >= max: walk = max; dir = -1                  # 100% flip at the edge
             if walk <= min: walk = min; dir = +1
         quote = round_half_even(walk × macro, 2)                  # cents on the wire
```

* Seconds wiggle, minutes trend: with the direction held for ~50 ticks (2% flip) the trend
  shows through the noise on a 5-minute chart.
* **Pin** (`pinned_price`): the quote walks to the pin at `pin_step_percent` per tick (a 2×
  move takes ~2 min at 0.5%) and then sits there plus noise. Above the pin it falls, below it
  rises; the band is ignored while pinned. Band positions are still computed on the quote, so a
  stock pinned above its band reads "above the top" and the desk sells it; pinned below, it
  buys. Remove the key and the band walk resumes (walking back in if outside, never jumping).
* **Why not round internally:** for a $22 stock a 0.01% step is $0.002 and would round to zero
  every tick, killing the trend. Internal walk keeps 6 dp; the emitted/traded price is cents.
* Feed: one batch per second containing all 100 mutations, one Stream Load to `prices`
  (history, kept) plus an upsert batch to `latest_price` (live mark). The macro multiplier is
  in-memory only (shown on the dashboard); it is already baked into every quote.

### 3.1a Macro multiplier (`market.toml [macro]`)

One multiplier applied to every quote, with the same knobs as a stock: a bounded walk in
`[min_multiplier, max_multiplier]` (default 0.95–1.05) with `trend_step_percent` (0.02),
`noise_percent` (0.05, a market-wide wiggle that hits every stock together) and
`direction_flip_chance` (0.02); starts at 1.0 on boot. `multiplier = walk × (1 + noise)`.

* Bands do **not** move with it: a rally pushes every stock toward its band top (the desk sells
  into it), a slump toward the bottom (the desk buys). NAV shows the macro cycle.
* **Never jumps.** When the range is edited and the multiplier is outside it, it walks to the
  nearest edge at `pin_step_percent` (0.5) per tick. A flat market is
  `0.99999–1.00001`; a crash is `0.70–0.75` (reached in ~1 min); `pinned_multiplier = 1.03`
  walks there and holds until removed.
* A stock pin targets the *quote*, so `pinned_price = 423` shows 423 whatever the multiplier.
* Dashboard header shows `×1.0123 ▲` with the range, the target it is walking to, or "pinned".

### 3.2 Order flow — tickets, blocks, sides

* `trades_per_second` (50). Each trade: side, symbol, ticket, executed at the in-memory price.
* **Ticket** notional is log-uniform in `[trade_min_amount, trade_max_amount]` = [$10K, $1M]:
  $10K–$100K as likely as $100K–$1M, so $1M is rare. Qty = floor(ticket / price), ≥ 1.
* **Side**: BUY with probability `buy_chance` (calibrator-controlled), else SELL.
* **Clearing fee**: `trade_cost = max(notional × trade_cost_rate, trade_cost_min_amount)`
  = 0.01% of notional, at least $0.01, deducted from cash on every trade and stored on the row.
* **Symbol** selection uses the perceived band (§3.3) and the deviation from target weight (§3.4).
* **Blocks (worked orders).** With probability `block_trade_chance` (0.02) a trade becomes
  a block: the same ticket is executed in `block_trade_slices` (5) tranches,
  `block_trade_slice_interval_seconds` (1) apart. An active block locks its symbol against all
  other BUY and SELL decisions; sells reserve quantity up front. Blocks do not move prices (§3.4).
* **Position bookkeeping** is in memory (map symbol → qty, avg_cost) under one mutex, mutated
  by every trade. Sells are capped at the holding. Each SELL row carries `realized_pnl =
  qty × (price − avg_cost)`, so Doris can itemise realised vs unrealised from real rows.
* Every trade is one row in `trades` (see §5) — trades are the source of truth for Doris.

### 3.3 Perceived vs real band — the profitability dial

`perceived_pos = (price − perceived.min) / (perceived.max − perceived.min)`; the strategy
buys `perceived_pos < 0.5` and sells holdings with `perceived_pos > 0.5`, with probability
`optimal_trade_chance`; otherwise it picks from the other half.

The side and optimal/non-optimal choice are rolled once per scheduling turn and are not changed.
The trader picks among eligible unlocked symbols in that exact half of the perceived band,
proportionally to their dollar deviation from target weight (§3.4). If the candidate set is
empty, it skips the turn.

| Perceived band vs real | App behaviour | Outcome |
|---|---|---|
| Equal (default) | Buys truly low, sells truly high | Wins most round-trips, minus fees, impact and the 40% "wrong half" trades |
| Shifted up | Thinks mid-band is "low" | Buys near the real top, sells rarely: holds losers |
| Shifted down | Sells at what it thinks is high but is mid | Small wins, leaves money on the table |
| Wider than real | Never sees extremes | Trades little, neutral |
| Narrower than real | Trades constantly both ways | Churn, fees dominate |

`finsim gen-stocks` writes `[real]` only. Mis-modelling is something you do by hand,
live, in a stock file: "risk team updated the AAPL model" is a demo moment.

### 3.4 Blocks, symbol locks, skipped turns and the NAV regulator

* **Blocks** are worked in `block_trade_slices` (5) tranches, `block_trade_slice_interval_seconds`
  (1) apart. `block_trade_chance` (2%) of trades become blocks. **Blocks do not move prices**:
  our own trading never affects the market, so the desk cannot pump its own book.
* **Symbol lock.** While a block is working a symbol, no other trade of either side and no
  second block may touch it (a desk never crosses its own order).
* **Selection by weight deviation** (`maintain_target_weight = true`, the default; set false to
  pick uniformly at random within the chosen half of the band instead). Target share of equity per stock = weight / Σweight.
  A buy considers unlocked stocks that are *underweight* (target value − market value > 0)
  and in the half of the perceived band the dice chose (lower half when the roll is
  "optimal", upper half otherwise); the pick is proportional to the dollar deviation. A sell
  considers unlocked *overweight* holdings in the other half, proportional to dollars above
  target. Proportional rather than argmax, so one name does not get thousands of consecutive
  tickets.
* **Extreme trades** (`[extreme_trades]`, all off by default).
  Checked every second, at most one per second, each eligible stock with `chance_per_second`:
  `sell_all_at_high` dumps an entire holding as one block when it sits at or above
  `sell_all_band_position` (95%) of its perceived band, provided cash is below
  `sell_all_max_cash_ratio` (50%); `suck_all_at_low` buys `suck_all_cash_share` (10%) of cash as
  one block when a stock is at or below `suck_all_band_position` (5%), provided cash is at least
  `suck_all_min_cash_ratio` (28%). Both use the normal tranche settings and symbol lock; the
  remainder rides on the last tranche. Events are listed on the dashboard's blocks panel.
  **Money-losing mirrors**, same mechanics with their own thresholds, for a demo that bleeds on
  demand: `panic_sell_at_low` dumps a holding at or below `panic_sell_band_position` (5%) while
  cash is below `panic_sell_max_cash_ratio` (50%); `fomo_buy_at_high` buys `fomo_buy_cash_share`
  (10%) of cash at or above `fomo_buy_band_position` (95%) while cash is at least
  `fomo_buy_min_cash_ratio` (28%). Tagged PANIC and FOMO on the dashboard.
* **Chicken out.** If no candidate exists for that roll the turn is skipped: no trade, no
  side change, no fallback to the wrong half. Skips are counted and shown on the dashboard.
* **NAV regulator (runs forever).** The calibrator (every 30 s) sets the effective optimal-trade
  chance from NAV with a latch: crossing `nav_rich_threshold_amount` ($2T) switches to
  `optimal_trade_chance_when_rich` (30%) and stays there until NAV crosses
  `nav_poor_threshold_amount` ($0.5T), which switches to `optimal_trade_chance_when_poor` (70%)
  and stays there until $2T is crossed again. `optimal_trade_chance` applies only from start or a
  strategy.toml reload until the first crossing. NAV therefore sweeps the whole corridor instead
  of hovering at a threshold. Prices are bounded by their bands, so
  equity can neither vanish nor explode, and NAV stays in the corridor indefinitely.
* **History retention (nothing is kept forever).** Every 30 minutes: `prices` partitions older
  than `PRICES_RETENTION_HOURS` (48) and `book_snapshots` partitions older than
  `SNAPSHOTS_RETENTION_DAYS` (7) are dropped. `trades` partitions older than
  `TRADES_RETENTION_DAYS` (7) are **folded, then dropped**: the net effect of the expiring
  partitions per symbol is written as two carry-forward rows (BUY = total bought, SELL = total
  sold, with fees, realised P&L and the last average cost) timestamped at the start of the
  oldest partition that is kept, flagged `is_seed` so the tape hides them. Every SUM the MVs,
  `v_pnl` and the resume logic use is unchanged by the fold, so positions, cash and P&L stay
  exact (verified: 757,709 rows → 360,994, all per-symbol sums identical). Nothing is dropped
  until the carry rows are confirmed visible. The only transient is a scheduled MV refresh
  landing in the few seconds between carry rows landing and the drop. Snapshot rows carry
  absolute totals, so dropping old snapshot days loses history, never correctness. Dashboard
  queries carry a time predicate and touch the newest one or two partitions; the MVs do a
  complete refresh every minute over what is kept. `finsim fold -keep-hours N` runs one pass by
  hand (with the simulator stopped).

### 3.4a Cash top-up, withdrawal and forgetting (admin page)

All cash movements happen on `/finsim/admin` (the dashboard only links to it).

* **Top-up** (`POST api/topup`): cash += amount, always allowed. **Withdrawal** (`POST api/cashout`): cash −= amount,
  allowed up to the NAV even when it exceeds the cash on hand. Both are rows in `cashouts` (top-ups negative), so
  `paid_out` everywhere is withdrawals minus top-ups, and Doris cash = starting − bought + sold − fees − paid_out.
* **Negative cash blocks buys.** While cash ≤ 0 every BUY turn is skipped (counted as a skip), block buys stop and
  all-in buys cannot fire; sells continue, the calibrator sees a negative cash ratio and walks `buy_chance` down.
* **P&L carries the movements**: P&L = NAV + paid_out − forgotten − starting cash. Neither a top-up nor a
  withdrawal changes P&L when made.
* **Forget** (`POST api/cashout/forget`) sets `book_meta.paid_out_forgotten` to the net paid out so far, then the
  in-memory value: P&L drops to NAV − starting cash. Cash, NAV and positions are untouched; it cannot be undone.
  `v_pnl` exposes `paid_out` (still counted), `paid_out_total`, `paid_out_forgotten`; the dashboard nets forgotten
  movements out of realised P&L so the bars stay on the scale of net P&L.
* **Reset** (`POST api/reset`, body `{"confirm":"RESET"}`): the run loop is a sequence of "generations" (loader +
  engine). A reset cancels the current one, which detaches from the web server (503s), flushes and closes the loader,
  then `DropAll` + `EnsureSchema` + starting cash from strategy.toml, and the next generation seeds an empty book.
  The web server, config watcher and signal handler live across generations.

### 3.5 Weights — making the book look real

`weight` per stock (fake market cap: NVDA/AAPL/MSFT ≈ 30–35, tail ≈ 0.5) is used in:

1. **Seed allocation**: initial portfolio ∝ weight ⇒ index-like top holdings.
2. **Buy selection** ∝ `weight^stock_weight_exponent` (0.8); **sell selection** ∝ position value.
3. **Volume**: trade count per stock therefore ∝ weight — NVDA on the tape constantly, LIN rarely.

### 3.6 Seeding (`--reset` or empty database)

Seed to `initial_equity_ratio` (0.65) of starting cash, allocation ∝ weight, executed as
BUY rows with `is_seed = true` in tickets of `initial_position_trade_amount` ($10M) at the
opening price, fee included. ≈ 65k rows. Seed rows count in positions and cash.

### 3.7 Calibrator and book snapshots

Every `rebalance_interval_seconds`: `ratio = cash / (cash + Σ qty × price)` from memory (no
Doris query); above target ⇒ `buy_chance += step`, below ⇒ `−= step`, clamped. Writes a
`calibrations` row. Every second a `book_snapshots` row (cash, equity, nav, pnl, ratio, buy_chance).

| trades/s | flow per hour | share of $1T per hour | time to fix a 5-point cash miss |
|---|---|---|---|
| 20 | $15B | 1.5% | ≈ 3.7 h |
| 50 (default) | $39B | 3.9% | ≈ 1.4 h |
| 200 | $155B | 15% | ≈ 20 min |

### 3.8 Hot reload

* `fsnotify` on `stocks/`, `strategy.toml`, `market.toml`. On write: parse, validate, swap
  atomically (`atomic.Pointer` per stock and per file; loops read it on every tick).
* Stock reload: price is kept, direction re-drawn 50/50. If the price is outside the new real
  band it walks back in at `pin_step_percent` per tick — no jumps. Adding/removing
  `pinned_price` takes effect on the next tick.
* Market reload: the macro config is swapped; the multiplier walks to the new range or pin
  (§3.1a) — no jumps.
* New file ⇒ new stock starts ticking (from `start_price`) and appears in `latest_price`.
  Deleted file ⇒ no ticks, no buys; the position stays and can still be sold at the last price.
* Bad file ⇒ keep previous config, log, and show "config error: AAPL.toml line 12" on the dashboard.
* Not reloadable: `starting_cash_amount`, Doris connection.

### 3.9 Cashout (dividend)

The Cash-ratio card has a **cashout** button: enter an amount (`1.5T`, `200B`, `50M`, `10K` or
dollars) and that much cash leaves the book. `POST /api/cashout {"amount": "1.5T"}` does the same.
Cash and NAV drop; **P&L does not** — it is defined as `nav + paid_out − starting_cash`, so a dividend
is never a loss. Only cash on hand can go out: a larger amount is refused and positions are never
sold to fund it. Every cashout is a row in `cashouts` (id, amount, cash and NAV before) so `v_pnl`
and a resume subtract `paid_out` exactly as memory does. The calibrator sees the lower cash ratio
and buys less until the book is back on target.

### 3.10 Money

Go: `github.com/shopspring/decimal` for price, notional, fee, cash, P&L; quantities are
`int64`. Prices rounded half-even to cents when emitted; everything else kept at 6 dp.
Doris: `DECIMAL(18,6)` for prices, `DECIMAL(27,6)` for amounts (max $10^21, fine for $1T
books and their sums). No DOUBLE column ever holds money; ratios and band positions are DOUBLE.

## 4. Architecture

```
strategy.toml, market.toml, stocks/*.toml (watched) ─┐            ┌──> Doris / VeloDB Cloud (Stream Load, 1 batch/s/table)
.env ──────────────────────────────────────────────┤            │      prices, latest_price, trades,
                                                     ▼            │      book_snapshots, calibrations
        ┌─────────── finsim (Go) ─────────────┐                   │
        │ config   : load + fsnotify hot reload│                   │
        │ market   : price engine, 1s ticks    │───────────────────┤
        │ book     : cash, positions, avg cost │                   │
        │ trader   : trades/s, blocks, impact  │                   │
        │ calibrator: every 30s adjust buyChance                   │
        │ sink     : per-table batch writer    │                   │
        │ web      : dashboard + /finsim/api/* │◄──────────────────┘  MySQL protocol queries against MVs/views
        └──────────────────────────────────────┘
   start-up: SELECT last price per symbol, positions/cash from trades  ──> memory
```

Go modules: `BurntSushi/toml`, `fsnotify/fsnotify`, `go-sql-driver/mysql`,
`shopspring/decimal`, `wushilin/doris_go_stream_load` (as in observability/).
Stream Load settings (per table): CSV with tab separator, 4 upload workers, 100 ms linger,
upload queue depth 1, 30 MiB batches. In practice Doris cloud commits take ~1.4 s, so the
SDK coalesces to about one load per second per table.

```
realtime-finance/
  main.go                 finsim schema | run [--reset] | gen-stocks
  strategy.toml           how the desk trades (hot reloaded)
  market.toml             price-engine defaults and impact (hot reloaded)
  stocks/AAPL.toml ...    100 per-stock files (generated, then hand-editable, hot reloaded)
  .env                    VeloDB Cloud connection (git-ignored; template env.example)
  schema.sql              tables, MVs, views (embedded into the binary from the repo root)
  internal/config/  market/  book/  trader/  sink/  web/
  run.sh
```

## 5. Doris schema (sketch)

```sql
CREATE TABLE prices (ts DATETIME(3), symbol VARCHAR(16), price DECIMAL(18,6), real_pos DOUBLE, perceived_pos DOUBLE, dir TINYINT)
  DUPLICATE KEY(ts, symbol) AUTO PARTITION BY RANGE(date_trunc(ts,'hour')) () DISTRIBUTED BY HASH(symbol) BUCKETS 8;

CREATE TABLE latest_price (symbol VARCHAR(16), ts DATETIME(3), price DECIMAL(18,6), name VARCHAR(64), sector VARCHAR(32), weight DOUBLE)
  UNIQUE KEY(symbol) DISTRIBUTED BY HASH(symbol) BUCKETS 4;

CREATE TABLE trades (
  ts DATETIME(3), trade_id BIGINT, block_id BIGINT NULL, slice_no SMALLINT NULL,
  symbol VARCHAR(16), side VARCHAR(4), qty BIGINT,
  price DECIMAL(18,6), notional DECIMAL(27,6), trade_cost DECIMAL(27,6),
  realized_pnl DECIMAL(27,6),          -- SELL rows only: qty × (price − avg_cost)
  avg_cost_after DECIMAL(18,6),        -- position average cost after this trade
  decision_price DECIMAL(18,6), perceived_pos DOUBLE,
  cash_after DECIMAL(27,6), buy_chance DOUBLE, is_seed BOOLEAN)
  DUPLICATE KEY(ts, trade_id) AUTO PARTITION BY RANGE(date_trunc(ts,'hour')) () DISTRIBUTED BY HASH(symbol) BUCKETS 8;

CREATE TABLE book_snapshots (ts DATETIME(3), cash DECIMAL(27,6), equity DECIMAL(27,6), nav DECIMAL(27,6),
  pnl DECIMAL(27,6), trade_cost_total DECIMAL(27,6), cash_ratio DOUBLE, buy_chance DOUBLE, calibrated BOOLEAN)
  DUPLICATE KEY(ts) ...;

-- Positions now: async MV, 1 minute
CREATE MATERIALIZED VIEW mv_positions
BUILD IMMEDIATE REFRESH COMPLETE ON SCHEDULE EVERY 1 MINUTE
DISTRIBUTED BY HASH(symbol) BUCKETS 4
AS SELECT symbol,
          SUM(CASE WHEN side='BUY' THEN qty ELSE -qty END)           AS qty,
          SUM(CASE WHEN side='BUY' THEN notional ELSE -notional END) AS net_invested,
          SUM(CASE WHEN side='BUY' THEN notional ELSE 0 END)         AS bought,
          SUM(CASE WHEN side='SELL' THEN notional ELSE 0 END)        AS sold,
          SUM(trade_cost) AS trade_cost, SUM(realized_pnl) AS realized_pnl,
          COUNT(*) AS trades, MAX(ts) AS last_trade
   FROM trades GROUP BY symbol;

-- Positions over time: net change per symbol per minute (MV), cumulated in the view
CREATE MATERIALIZED VIEW mv_position_minute
BUILD IMMEDIATE REFRESH COMPLETE ON SCHEDULE EVERY 1 MINUTE ... 
AS SELECT symbol, date_trunc(ts,'minute') AS minute,
          SUM(CASE WHEN side='BUY' THEN qty ELSE -qty END) AS qty_delta,
          SUM(CASE WHEN side='BUY' THEN notional ELSE -notional END) AS invested_delta,
          SUM(trade_cost) AS trade_cost
   FROM trades GROUP BY symbol, date_trunc(ts,'minute');

CREATE VIEW v_position_history AS
  SELECT m.symbol, m.minute,
         SUM(qty_delta)      OVER (PARTITION BY symbol ORDER BY minute) AS qty,
         SUM(invested_delta) OVER (PARTITION BY symbol ORDER BY minute) AS net_invested,
         p.avg_price
  FROM mv_position_minute m
  LEFT JOIN (SELECT symbol, date_trunc(ts,'minute') AS minute, AVG(price) AS avg_price FROM prices GROUP BY 1,2) p
    USING (symbol, minute);
-- position value over time = qty × avg_price; the dashboard draws qty and value per ticker

CREATE MATERIALIZED VIEW mv_cash ... EVERY 1 MINUTE AS
  SELECT 1000000000000 - SUM(CASE WHEN side='BUY' THEN notional ELSE -notional END) - SUM(trade_cost) AS cash,
         SUM(trade_cost) AS trade_cost, SUM(realized_pnl) AS realized_pnl, COUNT(*) AS trades FROM trades;

CREATE VIEW v_positions AS   -- mark-to-market with live prices
  SELECT p.symbol, lp.name, lp.sector, p.qty, lp.price, p.qty*lp.price AS market_value, p.net_invested,
         p.qty*lp.price - p.net_invested AS gross_pnl, p.realized_pnl,
         p.qty*lp.price - p.net_invested - p.realized_pnl AS unrealized_pnl, p.trade_cost
  FROM mv_positions p JOIN latest_price lp USING (symbol) WHERE p.qty > 0;

CREATE VIEW v_pnl AS         -- itemised P&L since inception ($1T)
  SELECT c.cash, e.equity, c.cash + e.equity AS nav,
         e.gross_pnl                        AS trading_pnl,      -- realised + unrealised, before costs
         c.realized_pnl                     AS realized_pnl,
         e.gross_pnl - c.realized_pnl       AS unrealized_pnl,
         -c.trade_cost                      AS trade_cost,       -- cost category 1 (more can be added)
         e.gross_pnl - c.trade_cost         AS net_pnl,          -- = nav − 1T
         c.cash / (c.cash + e.equity)       AS cash_ratio
  FROM mv_cash c, (SELECT SUM(market_value) AS equity, SUM(gross_pnl) AS gross_pnl FROM v_positions) e;

CREATE VIEW v_block_shortfall AS ... (as before)
```

Identity check the dashboard can assert: `net_pnl = nav − 1,000,000,000,000`, and
`Σ symbol gross_pnl − Σ trade_cost = net_pnl`. Verified on the cluster (cloud-26.1.2): async MVs with `ON SCHEDULE EVERY 1 MINUTE` work.
Omit `replication_num` and `PROPERTIES` on cloud mode; the MV DDL above is written without them.

## 6. Dashboard — professional, intuitive, one screen

Served by the Go binary at `/`, single HTML page, ECharts 5, no build step. Design rules:

* **Trading-desk look, not a toy.** Dark navy ground, one accent colour (Doris blue) for
  interactive elements, green/red reserved strictly for gains/losses and buy/sell. Tabular
  numerals, right-aligned numbers, thousands separators, compact units ($1.23B, 4.5 bps).
* **Hierarchy by importance.** Row 1 is what a CIO asks first; drill-down lives one click
  away, never on the main grid. No panel needs a legend to be understood.
* **Everything live, nothing flickers.** Charts update in place (ECharts `setOption` merge),
  tables diff rows, new tape rows slide in, changed numbers flash briefly.
* **Freshness is visible.** Every Doris-backed panel carries a small "MV · refreshed 23s ago"
  tag; memory-backed panels carry "live". That difference is the story of the demo.
* **Responsive**: 12-column grid at ≥1440px, collapses to 6 then 1; charts resize on window change.

Layout (top to bottom):

1. **Header bar**: "Money Market Demo · Realtime Book", connection state (Doris, ingest
   rows/s), MV freshness, last config reload (or the parse error in amber), clock (UTC).
2. **KPI strip** (6 tiles): NAV · Net P&L (value and bps of $1T, colour-coded, with 1-min
   delta) · Cash ratio vs 30% target (mini bullet bar) · Equity value · Buy chance (gauge
   with the calibrator's last move) · Activity (trades/s, price ticks/s, active blocks).
3. **NAV & P&L over time** (wide, 1s resolution from `book_snapshots`, 15-min window with
   range zoom; calibration events as small markers; hover shows itemised P&L at that second).
   Beside it the **P&L waterfall**: trading P&L → realised / unrealised → − trade cost → net.
4. **Book composition**: cash vs equity donut with the 30% target ring · **Top 10 holdings**
   horizontal bar (market value, P&L colour) · **Sector allocation** treemap.
5. **Live trade tape** (last 50, seed hidden, buy/sell colour, block slices tagged) ·
   **Positions table** (all holdings: qty, price, value, weight %, unrealised, realised,
   cost, 5-min sparkline; sortable; click a row to drill down).
6. **Execution & Doris panel**: worked blocks with live shortfall · "Doris MV vs live"
   comparison (equity and position count from the MV vs from memory, with lag) · ingest
   stats from the Stream Load SDK (rows/s, p99 load time, errors).
7. **Prices tab** (second tab in the header): a market map treemap grouped by sector (size =
   weight, colour = 5-minute change, amber border = block impact) and a sortable table of all
   100 tickers with price, 1- and 5-minute change, trend/impact, real and perceived band
   position, position, unrealised P&L and a sparkline. Click anything to open the drill-down.

**Symbol drill-down** (slide-over panel, keeps the dashboard visible): price history with
the real band shaded and the perceived band outlined, buy/sell markers; position quantity
and value over time (`v_position_history`); the stock's trades; its config file contents,
so editing `stocks/AAPL.toml` and watching the panel change is a one-screen demo.

Polling: `/finsim/api/live` (memory) every 1s; Doris-backed endpoints every 2–5s; MV status every 10s.

## 7. Config files

`strategy.toml` — how the desk trades (hot reloaded):

```toml
[capital]
starting_cash_amount            = 1_000_000_000_000   # $1T; not hot reloadable
target_cash_ratio               = 0.30
rebalance_interval_seconds      = 30
initial_equity_ratio            = 0.65                # seed on --reset / empty DB; 0 = start all cash
initial_position_trade_amount   = 10_000_000          # ticket size for seed trades only

[trading]
trades_per_second               = 50
trade_min_amount                = 10_000
trade_max_amount                = 1_000_000
trade_amount_distribution       = "log-uniform"       # $10K most likely, $1M rare; "uniform" also allowed
buy_chance                      = 0.50                # starting probability a trade is a BUY
buy_chance_step                 = 0.05                # per rebalance while off target
buy_chance_min                  = 0.05
buy_chance_max                  = 0.95
optimal_trade_chance            = 0.60                # follow buy-low / sell-high on the perceived band; else the other half
stock_weight_exponent           = 0.80                # selection ∝ weight^exponent
trade_cost_rate                 = 0.0001              # clearing fee, 0.01% of notional
trade_cost_min_amount           = 0.01

[block_trades]
block_trade_chance              = 0.02
block_trade_slices              = 5
block_trade_slice_interval_seconds = 10
```

`market.toml` — tick clock and macro multiplier (hot reloaded):

```toml
[price]
tick_interval_seconds           = 1

[macro]                                               # quote = stock walk × multiplier; same knobs as a stock
min_multiplier                  = 0.95                # 0.99999–1.00001 = flat market; 0.70–0.75 = crash
max_multiplier                  = 1.05
trend_step_percent              = 0.02                # per tick, in the current direction
noise_percent                   = 0.05                # ± per tick, market-wide wiggle
direction_flip_chance           = 0.02                # per tick; min/max always flip
pin_step_percent                = 0.5                 # per tick, walking to a new range or the pin
# pinned_multiplier             = 1.03                # temporary: walk there and hold
```

`stocks/AAPL.toml` (hot reloaded; every key below `weight` is optional with the default shown):

```toml
symbol      = "AAPL"
name        = "Apple Inc."
sector      = "Technology"
start_price = 232.00          # used only when Doris has no price for the symbol
weight      = 32.0

trend_step_percent    = 0.012 # default 0.01
noise_percent         = 0.35  # default 0.30
direction_flip_chance = 0.02  # default 0.02

# pinned_price     = 423.00   # temporary: walk to this quote and wiggle there; remove to resume
# pin_step_percent = 0.5      # default 0.5; also the speed back into a narrowed band

band_percent          = 0.5   # real band = anchor ± this; anchor = Doris latest price, else start_price

# [real]                      # explicit band instead of band_percent
# min_price = 195.00
# max_price = 270.00

[perceived]                   # optional; omitted by the generator ⇒ equals [real]
min_price = 205.00
max_price = 285.00
```

## 8. Open items

1. Connectivity: add this machine's IP to the VeloDB allowlist (or tell me the route), and
   confirm the Stream Load URL/port.
2. `trades_per_second = 50` and seeding to 65% equity (§3.7 table) — OK?
3. Everything else in §2 marked Assumption — OK?
