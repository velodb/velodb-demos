# Realtime Finance Demo — a $1T book on Apache Doris (VeloDB Cloud)

`finsim` simulates a sovereign-fund trading desk and streams everything into Doris:
100 stocks ticking every second, 50 trades a second, a 30% cash target, and P&L computed
in Doris from real trade rows with async materialized views refreshed every minute.
A live dashboard sits on top: a Book tab (P&L, positions, tape, blocks, Doris vs live) and a
Prices tab (market map and all 100 tickers live). Design and rationale: [PLAN.md](PLAN.md).

## Configure

All connection settings are `VELODB_*` variables (see [`env.example`](env.example)). Pick one of:

```sh
cp env.example .env && $EDITOR .env          # a) a file (git-ignored)

export VELODB_HOST=<fe-or-lb-host>           # b) environment only: nothing on disk
export VELODB_USER=admin VELODB_PASSWORD='...'
```

**Environment variables take precedence over `.env`, and `.env` over the defaults**, so any setting can change for a
single run, e.g. `VELODB_DATABASE=money_market_test ./run.sh -reset`. `ENV_FILE=/path/other.env` loads another file.

| variable | default | |
|---|---|---|
| `VELODB_HOST` | (required) | FE / load balancer host |
| `VELODB_MYSQL_PORT` | `9030` | MySQL protocol port |
| `VELODB_STREAM_LOAD_PORT` | `8080` | FE HTTP port for stream load |
| `VELODB_STREAM_LOAD_URL` | `http://VELODB_HOST:VELODB_STREAM_LOAD_PORT` | set for https or a different endpoint |
| `VELODB_USER` | (required) | |
| `VELODB_PASSWORD` | empty | |
| `VELODB_DATABASE` | `money_market_demo` | created on first run |
| `PRICES_RETENTION_HOURS` / `SNAPSHOTS_RETENTION_DAYS` / `TRADES_RETENTION_DAYS` | `48` / `7` / `7` | see Retention |

Never commit `.env`; only `env.example` is tracked. An older `doris.env` (`MYSQL_HOST`, `DATABASE`, ...) is still
read when there is no `.env`.

## Run

```sh
# build, generate stocks/*.toml if missing, apply schema, seed the book, start
./run.sh -reset            # fresh start: wipe data, seed to 65% equity
./run.sh                   # resume from what is in Doris (default)
open http://localhost:8088/finsim/
```

`finsim run` flags: `-reset` (truncate + reseed), `-listen :8088`, `-no-seed`, `-dir DIR`.
Other commands: `finsim schema` (apply schema only), `finsim gen-stocks [-force]`,
`finsim migrate-stocks`, `finsim reset [-force]` (drop and recreate every table, MV and view from
schema.sql; the next `run` seeds a fresh book; refuses while a simulator is still writing unless
`-force`).

## Deploy (processmaster on a Linux host)

The demo can run on a Linux host under processmaster as the `finsim` drop-in, listening on
`127.0.0.1:19073` behind a TLS reverse proxy that publishes it at `https://<your-host>/finsim/`.
Do not run it locally against the shared database at the same time: use another database for local runs (`VELODB_DATABASE=money_market_test ./run.sh -reset`).
The drop-in directory holds its connection in `.env` (or the older `doris.env`), mode 0600.

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o finsim-linux-arm64 .
scp finsim-linux-arm64 market.toml <user>@<deploy-host>:/tmp/
ssh <user>@<deploy-host>
  D=/opt/processmaster/dropin/finsim; export PMCTL_SOCK=/opt/processmaster/processmaster.sock
  sudo install -o service -g service -m 0755 /tmp/finsim-linux-arm64 $D/finsim.new && sudo mv $D/finsim.new $D/finsim
  sudo install -o service -g service -m 0644 /tmp/market.toml $D/market.toml     # only if it changed
  sudo -u service $D/finsim migrate-stocks -dir $D    # only after a stock-file layout change; keeps every value
  sudo -E /opt/processmaster/pmctl restart finsim && sudo -E /opt/processmaster/pmctl status finsim
  sudo tail -f $D/logs/stderr.log
```

**Full reset on the host** (drops and recreates everything in Doris, then restarts with a fresh
book; the in-memory state is the process, so the service is stopped first):

```sh
sudo /opt/processmaster/dropin/finsim/reset.sh      # asks for confirmation; -y to skip
```

`strategy.toml` and `stocks/*.toml` on the host are edited in place (hot reloaded); do not overwrite
them from the repo without checking. Removing a config key from the code means removing it from the
host files too, or the reload is rejected and a restart fails to start. `finsim migrate-stocks`
rewrites every stock file in the current layout while keeping its values (new keys appear
commented with their defaults).

## Cashout (dividend)

The **cashout** button on the Cash-ratio card asks for an amount (`1.5T`, `200B`, `50M`, or dollars)
and transfers it out of the book. Cash and NAV drop, P&L does not, and the desk rebalances back
toward its cash target. Only cash on hand can go out; positions are never sold. Recorded in the
`cashouts` table so restarts and `v_pnl` agree. Same via
`curl -X POST -d '{"amount":"200B"}' .../finsim/api/cashout`. Note the endpoint has no auth beyond
whatever fronts the dashboard.

## Files you edit during the demo (hot reloaded, no restart)

| File | What it controls |
|---|---|
| `stocks/AAPL.toml` … | one file per stock: weight, noise, trend step, flip chance, `band_percent` (real band = latest Doris price ± this, re-centred on every start; or an explicit `[real]`), optional `[perceived]` band, and a temporary `pinned_price` (the quote walks there at `pin_step_percent`/tick and wiggles; above it falls, below it rises) |
| `strategy.toml` | ticket size, trades/s, buy chance and its calibration, optimal-trade chance, fee, blocks |
| `market.toml` | tick clock and the macro multiplier on every quote: same knobs as a stock (trend step, noise, flip chance, pin) on a bounded walk in `[min_multiplier, max_multiplier]`; narrow it to 0.99999–1.00001 for a flat market, move it to 0.70–0.75 for a crash, or `pinned_multiplier = 1.03` to hold a level. It always walks to a new range, never jumps |

Save a file and the change applies within a second; a bad file is rejected and shown in
the header. Try: add a `[perceived]` band to a stock that is shifted above its `[real]`
band and watch the desk buy it near its real top. Or watch a block in the Prices tab: while its
tranches execute, that symbol is locked against every other strategy trade until the last tranche fills.
Blocks never move prices: the desk cannot pump its own book.

## Admin page: cash top-up, withdrawal, forget

`/finsim/admin` (linked from the Cash ratio tile) is the only place cash is moved in or out of the book.

* **Top up** adds cash, always allowed. Cash and NAV rise, P&L does not.
* **Withdraw** removes cash, up to the NAV. It may exceed the cash on hand: cash then goes negative and every
  buy is blocked (turns are skipped) until sells or a top-up bring cash back above zero.
* Both are carried in P&L as net cash moved: P&L = NAV + withdrawals − top-ups − starting cash, so neither
  changes the P&L figure when it is made, and withdrawn profit stays counted as profit.
* **Forget all cash withdrawals** discards every movement made so far from P&L, permanently: P&L becomes
  NAV − starting cash. Cash, NAV and positions do not change. Stored as `paid_out_forgotten` in `book_meta`,
  so `v_pnl` and a restart agree.

* **Reset everything** types `RESET` to enable: trading stops, queued loads drain, every demo table, materialized
  view and view is dropped and recreated, and a fresh book is seeded from strategy.toml (starting cash as configured,
  no positions, no history, nothing withdrawn). The process stays up; `/api/live` answers 503 for about a minute while
  it happens. Same effect as `deploy/reset.sh`, without a service restart.

API: `POST api/topup` and `POST api/cashout` with `{"amount":"200B"}`, `POST api/cashout/forget`. `POST api/reset` with `{"confirm":"RESET"}`. Movements are rows in
the `cashouts` table (top-ups are negative amounts). The page has no login of its own; it is as open as the dashboard.

## Retention

Nothing is kept forever. `.env` / environment: `PRICES_RETENTION_HOURS=48`, `SNAPSHOTS_RETENTION_DAYS=7`,
`TRADES_RETENTION_DAYS=7`. Old price and snapshot partitions are dropped; old trades partitions are
first folded into carry-forward rows (net bought / sold per symbol, fees, realised P&L) at the start of
the oldest kept partition, so positions, cash and P&L are unchanged, then dropped. `finsim fold
-keep-hours N` does one pass by hand; stop the simulator first.

## Restart safety

Doris is the source of truth. On start the simulator loads the last price per symbol,
rebuilds positions, average cost and cash from the `trades` table, continues trade ids
from the maximum in Doris, and re-draws each stock's direction. On SIGINT it flushes the
Stream Load queues before exiting. Only `starting_cash_amount` needs `-reset` to change.

## What is where

```
main.go, gen.go            commands; schema.sql is embedded from the repo root
internal/config            TOML types, defaults, validation, fsnotify hot reload
internal/sim               price engine, book, trader, blocks, calibrator, seeding
internal/sink              Stream Load (CSV, 4 uploaders, 100 ms linger, 30 MiB batches)
internal/doris             schema apply, reset, state restore
internal/web               dashboard (ECharts, vendored) and /finsim/api/*
schema.sql                 tables, async MVs (mv_positions, mv_cash, mv_position_minute, mv_price_minute), views
```
