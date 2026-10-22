-- Realtime finance demo schema. Applied by `finsim schema` (and on every `finsim run`).
-- Every statement is idempotent. Doris cloud mode: no replication_num / PROPERTIES needed.

CREATE TABLE IF NOT EXISTS book_meta (
    k VARCHAR(32) NOT NULL,
    v DECIMAL(27,6) NOT NULL
) ENGINE=OLAP
UNIQUE KEY(k)
DISTRIBUTED BY HASH(k) BUCKETS 1;

-- Full price history: one row per stock per second.
CREATE TABLE IF NOT EXISTS prices (
    ts            DATETIME(3)   NOT NULL,
    symbol        VARCHAR(16)   NOT NULL,
    price         DECIMAL(18,6) NOT NULL,
    fair_price    DECIMAL(18,6) NULL,          -- price clamped to the real band (own-impact removed)
    real_pos      DOUBLE,
    perceived_pos DOUBLE,
    dir           TINYINT
) ENGINE=OLAP
DUPLICATE KEY(ts, symbol)
AUTO PARTITION BY RANGE (date_trunc(ts, 'hour')) ()
DISTRIBUTED BY HASH(symbol) BUCKETS 8;

-- Live mark: upserted every tick, one row per symbol.
CREATE TABLE IF NOT EXISTS latest_price (
    symbol VARCHAR(16)   NOT NULL,
    ts     DATETIME(3)   NOT NULL,
    price  DECIMAL(18,6) NOT NULL,
    fair_price DECIMAL(18,6) NULL,
    name   VARCHAR(64),
    sector VARCHAR(32),
    weight DOUBLE
) ENGINE=OLAP
UNIQUE KEY(symbol)
DISTRIBUTED BY HASH(symbol) BUCKETS 4;

-- Every trade the desk makes. Source of truth for positions, cash and P&L.
CREATE TABLE IF NOT EXISTS trades (
    ts             DATETIME(3)   NOT NULL,
    trade_id       BIGINT        NOT NULL,
    block_id       BIGINT        NULL,
    slice_no       SMALLINT      NULL,
    symbol         VARCHAR(16)   NOT NULL,
    side           VARCHAR(4)    NOT NULL,
    qty            BIGINT        NOT NULL,
    price          DECIMAL(18,6) NOT NULL,
    notional       DECIMAL(27,6) NOT NULL,
    trade_cost     DECIMAL(27,6) NOT NULL,
    realized_pnl   DECIMAL(27,6) NOT NULL,
    avg_cost_after DECIMAL(18,6) NOT NULL,
    decision_price DECIMAL(18,6) NOT NULL,
    perceived_pos  DOUBLE,
    cash_after     DECIMAL(27,6) NOT NULL,
    buy_chance     DOUBLE,
    is_seed        BOOLEAN       NOT NULL
) ENGINE=OLAP
DUPLICATE KEY(ts, trade_id)
AUTO PARTITION BY RANGE (date_trunc(ts, 'hour')) ()
DISTRIBUTED BY HASH(symbol) BUCKETS 8;

-- Simulator's own view of the book, once per second (the "live" line on the dashboard).
CREATE TABLE IF NOT EXISTS book_snapshots (
    ts                 DATETIME(3)   NOT NULL,
    cash               DECIMAL(27,6) NOT NULL,
    equity             DECIMAL(27,6) NOT NULL,
    nav                DECIMAL(27,6) NOT NULL,
    pnl                DECIMAL(27,6) NOT NULL,
    trade_cost_total   DECIMAL(27,6) NOT NULL,
    realized_pnl_total DECIMAL(27,6) NOT NULL,
    cash_ratio         DOUBLE,
    buy_chance         DOUBLE,
    calibrated         BOOLEAN
) ENGINE=OLAP
DUPLICATE KEY(ts)
AUTO PARTITION BY RANGE (date_trunc(ts, 'day')) ()
DISTRIBUTED BY HASH(ts) BUCKETS 2;

-- Cash paid out of the book (dividends / capital returns). Reduces cash and NAV; never a loss.
CREATE TABLE IF NOT EXISTS cashouts (
    ts          DATETIME(3)   NOT NULL,
    cashout_id  BIGINT        NOT NULL,
    amount      DECIMAL(27,6) NOT NULL,
    cash_before DECIMAL(27,6) NOT NULL,
    nav_before  DECIMAL(27,6) NOT NULL
) ENGINE=OLAP
DUPLICATE KEY(ts, cashout_id)
DISTRIBUTED BY HASH(cashout_id) BUCKETS 1;

-- Calibrator decisions, every rebalance interval.
CREATE TABLE IF NOT EXISTS calibrations (
    ts                DATETIME(3)   NOT NULL,
    cash              DECIMAL(27,6) NOT NULL,
    equity            DECIMAL(27,6) NOT NULL,
    cash_ratio        DOUBLE,
    target_cash_ratio DOUBLE,
    buy_chance_before DOUBLE,
    buy_chance_after  DOUBLE
) ENGINE=OLAP
DUPLICATE KEY(ts)
DISTRIBUTED BY HASH(ts) BUCKETS 1;

-- ---------------------------------------------------------------------------
-- Async materialized views, refreshed every minute. These are the demo.
-- ---------------------------------------------------------------------------

-- Positions now, from real trades.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_positions
BUILD IMMEDIATE REFRESH COMPLETE ON SCHEDULE EVERY 1 MINUTE
DISTRIBUTED BY HASH(symbol) BUCKETS 4
AS SELECT
    symbol,
    SUM(CASE WHEN side = 'BUY' THEN qty ELSE -qty END)           AS qty,
    SUM(CASE WHEN side = 'BUY' THEN notional ELSE -notional END) AS net_invested,
    SUM(CASE WHEN side = 'BUY' THEN notional ELSE 0 END)         AS bought,
    SUM(CASE WHEN side = 'SELL' THEN notional ELSE 0 END)        AS sold,
    SUM(trade_cost)                                              AS trade_cost,
    SUM(realized_pnl)                                            AS realized_pnl,
    COUNT(*)                                                     AS trades,
    MAX(ts)                                                      AS last_trade
FROM trades
GROUP BY symbol;

-- Whole-book cash flows.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_cash
BUILD IMMEDIATE REFRESH COMPLETE ON SCHEDULE EVERY 1 MINUTE
DISTRIBUTED BY HASH(trades) BUCKETS 1
AS SELECT
    COUNT(*)                                                     AS trades,
    SUM(CASE WHEN side = 'BUY' THEN notional ELSE 0 END)         AS bought,
    SUM(CASE WHEN side = 'SELL' THEN notional ELSE 0 END)        AS sold,
    SUM(trade_cost)                                              AS trade_cost,
    SUM(realized_pnl)                                            AS realized_pnl,
    MAX(ts)                                                      AS last_trade
FROM trades;

-- Position change per symbol per minute (cumulated in v_position_history).
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_position_minute
BUILD IMMEDIATE REFRESH COMPLETE ON SCHEDULE EVERY 1 MINUTE
DISTRIBUTED BY HASH(symbol) BUCKETS 4
AS SELECT
    symbol,
    date_trunc(ts, 'minute')                                     AS minute,
    SUM(CASE WHEN side = 'BUY' THEN qty ELSE -qty END)           AS qty_delta,
    SUM(CASE WHEN side = 'BUY' THEN notional ELSE -notional END) AS invested_delta,
    SUM(trade_cost)                                              AS trade_cost,
    COUNT(*)                                                     AS trades
FROM trades
GROUP BY symbol, date_trunc(ts, 'minute');

-- One-minute price bars, for charts and for valuing position history.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_price_minute
BUILD IMMEDIATE REFRESH COMPLETE ON SCHEDULE EVERY 1 MINUTE
DISTRIBUTED BY HASH(symbol) BUCKETS 4
AS SELECT
    symbol,
    date_trunc(ts, 'minute') AS minute,
    AVG(price)               AS avg_price,
    MIN(price)               AS low,
    MAX(price)               AS high,
    COUNT(*)                 AS ticks
FROM prices
GROUP BY symbol, date_trunc(ts, 'minute');

-- ---------------------------------------------------------------------------
-- Views on top of the MVs.
-- ---------------------------------------------------------------------------

-- Mark-to-market positions: 1-minute MV quantities × live prices.
-- market_value/gross_pnl use the last price; fair_value/fair_pnl use the price clamped to the
-- stock's real band, i.e. with the overshoot caused by our own block trades removed.
CREATE OR REPLACE VIEW v_positions AS
SELECT
    p.symbol,
    lp.name,
    lp.sector,
    lp.weight,
    p.qty,
    lp.price,
    COALESCE(lp.fair_price, lp.price)                  AS fair_price,
    p.qty * lp.price                                   AS market_value,
    p.qty * COALESCE(lp.fair_price, lp.price)          AS fair_value,
    p.net_invested,
    p.bought,
    p.sold,
    p.qty * lp.price - p.net_invested                  AS gross_pnl,
    p.qty * COALESCE(lp.fair_price, lp.price) - p.net_invested AS fair_pnl,
    p.realized_pnl,
    p.qty * lp.price - p.net_invested - p.realized_pnl AS unrealized_pnl,
    p.qty * COALESCE(lp.fair_price, lp.price) - p.net_invested - p.realized_pnl AS fair_unrealized_pnl,
    p.trade_cost,
    p.trades,
    p.last_trade
FROM mv_positions p
JOIN latest_price lp ON lp.symbol = p.symbol
WHERE p.qty > 0;

-- Itemised P&L of the whole book since inception, marked and fair-valued.
CREATE OR REPLACE VIEW v_pnl AS
SELECT
    m.v                                    AS starting_cash,
    p.paid_out - f.forgotten               AS paid_out,            -- still counted as profit
    p.paid_out                             AS paid_out_total,      -- every cashout ever (drives cash)
    f.forgotten                            AS paid_out_forgotten,  -- written off by "forget withdrawals"
    m.v - c.bought + c.sold - c.trade_cost - p.paid_out AS cash,   -- may be negative (overdraft)
    e.equity                               AS equity,
    e.fair_equity                          AS fair_equity,
    m.v - c.bought + c.sold - c.trade_cost - p.paid_out + e.equity      AS nav,
    m.v - c.bought + c.sold - c.trade_cost - p.paid_out + e.fair_equity AS fair_nav,
    e.gross_pnl                            AS trading_pnl,
    c.realized_pnl                         AS realized_pnl,
    e.gross_pnl - c.realized_pnl           AS unrealized_pnl,
    e.fair_pnl - c.realized_pnl            AS fair_unrealized_pnl,
    e.gross_pnl - e.fair_pnl               AS impact_distortion,   -- unrealisable part of the mark
    c.trade_cost                           AS trade_cost,
    e.gross_pnl - c.trade_cost - f.forgotten AS net_pnl,           -- = nav + paid_out - starting_cash
    e.fair_pnl - c.trade_cost - f.forgotten  AS fair_net_pnl,
    (m.v - c.bought + c.sold - c.trade_cost - p.paid_out) / (m.v - c.bought + c.sold - c.trade_cost - p.paid_out + e.equity) AS cash_ratio,
    c.trades                               AS trades,
    c.last_trade                           AS last_trade,
    e.positions                            AS positions
FROM mv_cash c
CROSS JOIN (SELECT v FROM book_meta WHERE k = 'starting_cash') m
CROSS JOIN (SELECT COALESCE(SUM(v), 0) AS forgotten FROM book_meta WHERE k = 'paid_out_forgotten') f
CROSS JOIN (SELECT COALESCE(SUM(amount), 0) AS paid_out FROM cashouts) p
CROSS JOIN (SELECT COALESCE(SUM(market_value), 0) AS equity, COALESCE(SUM(fair_value), 0) AS fair_equity,
                   COALESCE(SUM(gross_pnl), 0) AS gross_pnl, COALESCE(SUM(fair_pnl), 0) AS fair_pnl, COUNT(*) AS positions
            FROM v_positions) e;

-- Position quantity and value per symbol over time (minute resolution).
CREATE OR REPLACE VIEW v_position_history AS
SELECT
    h.symbol,
    h.minute,
    h.qty,
    h.net_invested,
    b.avg_price,
    h.qty * b.avg_price AS market_value
FROM (
    SELECT symbol, minute,
           SUM(qty_delta)      OVER (PARTITION BY symbol ORDER BY minute) AS qty,
           SUM(invested_delta) OVER (PARTITION BY symbol ORDER BY minute) AS net_invested
    FROM mv_position_minute
) h
LEFT JOIN mv_price_minute b ON b.symbol = h.symbol AND b.minute = h.minute;

-- Market impact of worked blocks.
CREATE OR REPLACE VIEW v_block_shortfall AS
SELECT
    block_id,
    symbol,
    side,
    MIN(ts)                      AS started,
    MAX(ts)                      AS ended,
    COUNT(*)                     AS slices,
    SUM(qty)                     AS qty,
    SUM(notional)                AS notional,
    MAX(decision_price)          AS decision_price,
    SUM(notional) / SUM(qty)     AS avg_fill,
    SUM(qty * (price - decision_price) * (CASE side WHEN 'BUY' THEN 1 ELSE -1 END)) / SUM(qty * decision_price) * 10000 AS shortfall_bps
FROM trades
WHERE block_id IS NOT NULL
GROUP BY block_id, symbol, side;

-- Sector allocation.
CREATE OR REPLACE VIEW v_sectors AS
SELECT sector, COUNT(*) AS positions, SUM(market_value) AS market_value, SUM(gross_pnl) AS gross_pnl
FROM v_positions
GROUP BY sector;
