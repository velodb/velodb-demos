# Configuring the demo timezone

The observability demo (Grafana → Doris App plugin) is **timezone-sensitive**.
This doc explains why, what to configure, and how to deploy correctly.

## The problem

| Component | Behavior |
|-----------|----------|
| Grafana **Doris App** plugin | Uses `dayjs(...).format(...)` — **browser-local timezone**. "Last 1 hour" sends datetime *string literals* (e.g. `2026-04-27 21:30:00`) without zone info. |
| OTEL collector → Doris exporter | `timezone:` setting controls how datetimes are *formatted as literals* before stream load. Default UTC. |
| Doris `DATETIME` columns | Store the literal string. **No timezone awareness.** |
| Doris `dynamic_partition.time_zone` | Decides which UTC date the next partition covers. Hardcoded to UTC by the OTEL exporter at table creation. |

When OTEL writes UTC literals (`2026-04-28 04:30:00`) but the plugin queries PT
literals (`2026-04-27 21:30:00`), the lexical comparison misses every row.

## The cloud production demo works because…

```yaml
# infra OTEL collector config:
exporters:
  doris/velodb:
    timezone: America/Los_Angeles   # ← writes PT-formatted literals
    history_days: 3
```

Combined with Doris `dynamic_partition.create_history_partition=true` and 7
days of historical partitions pre-created, PT-formatted writes land in the
right UTC-named partitions (string comparison `2026-04-27 23:30` ∈ partition
covering `[2026-04-27 00:00, 2026-04-28 00:00)`), and the Doris App plugin's
PT-formatted "Last 1 hour" matches.

## Local-deployment recipe

### 1. Pick a timezone that matches the *browser* you'll use

| Browser locale | Set in `.env` |
|----|----|
| Pacific (US/Canada) | `OTEL_TIMEZONE=America/Los_Angeles` |
| Eastern (US/Canada) | `OTEL_TIMEZONE=America/New_York` |
| London | `OTEL_TIMEZONE=Europe/London` |
| Berlin/Paris | `OTEL_TIMEZONE=Europe/Berlin` |
| Beijing/Singapore | `OTEL_TIMEZONE=Asia/Shanghai` |
| Tokyo | `OTEL_TIMEZONE=Asia/Tokyo` |
| UTC (or want strict UTC) | `OTEL_TIMEZONE=UTC` |

Also fine: leave `OTEL_TIMEZONE=UTC` and **set the laptop OS clock to UTC**.

### 2. Edit `dist/.env`

```bash
cp .env.example .env
sed -i '' 's|OTEL_TIMEZONE=.*|OTEL_TIMEZONE=America/Los_Angeles|' .env
```

### 3. Bring up the stack

```bash
docker compose --profile observability up -d
```

### 4. Backfill historical partitions (one-time, after first OTEL write)

The Doris OTEL exporter creates `dynamic_partition.history_partition_num=0`
by default — so on first boot you only get *today + tomorrow*. PT-formatted
data for "earlier today UTC" needs the previous-day partition. Add 7 days
of history (mirroring the cloud setup):

```bash
docker compose exec doris-fe \
  mysql -h127.0.0.1 -P9030 -uroot -p'VeloDB@demo123' -D demo_observability <<'SQL'
-- Disable dynamic_partition while backfilling
ALTER TABLE otel_logs                          SET ('dynamic_partition.enable'='false');
ALTER TABLE otel_traces                        SET ('dynamic_partition.enable'='false');
ALTER TABLE otel_metrics_sum                   SET ('dynamic_partition.enable'='false');
ALTER TABLE otel_metrics_gauge                 SET ('dynamic_partition.enable'='false');
ALTER TABLE otel_metrics_histogram             SET ('dynamic_partition.enable'='false');
ALTER TABLE otel_metrics_summary               SET ('dynamic_partition.enable'='false');

-- Add 7 days of historical partitions (replace dates with `today-7..today-1` UTC)
ALTER TABLE otel_logs ADD PARTITION p20260421 VALUES [('2026-04-21 00:00:00'), ('2026-04-22 00:00:00'));
ALTER TABLE otel_logs ADD PARTITION p20260422 VALUES [('2026-04-22 00:00:00'), ('2026-04-23 00:00:00'));
ALTER TABLE otel_logs ADD PARTITION p20260423 VALUES [('2026-04-23 00:00:00'), ('2026-04-24 00:00:00'));
ALTER TABLE otel_logs ADD PARTITION p20260424 VALUES [('2026-04-24 00:00:00'), ('2026-04-25 00:00:00'));
ALTER TABLE otel_logs ADD PARTITION p20260425 VALUES [('2026-04-25 00:00:00'), ('2026-04-26 00:00:00'));
ALTER TABLE otel_logs ADD PARTITION p20260426 VALUES [('2026-04-26 00:00:00'), ('2026-04-27 00:00:00'));
ALTER TABLE otel_logs ADD PARTITION p20260427 VALUES [('2026-04-27 00:00:00'), ('2026-04-28 00:00:00'));
-- Repeat for every otel_* table

-- Re-enable dynamic_partition
ALTER TABLE otel_logs SET ('dynamic_partition.enable'='true', 'dynamic_partition.history_partition_num'='7');
-- Repeat for every otel_* table
SQL
```

A scripted version is shipped:

```bash
./scripts/backfill-otel-partitions.sh
```

(see `dist/scripts/backfill-otel-partitions.sh` — it computes today's UTC
date and emits the right ALTER statements for all 8 OTEL tables.)

### 5. Verify

```bash
# Check the partitions exist
docker compose exec doris-fe \
  mysql -h127.0.0.1 -P9030 -uroot -p'VeloDB@demo123' -D demo_observability \
  -e "SHOW PARTITIONS FROM otel_logs" | awk -F'\t' 'NR>1 {print $2}'

# Spot-check a recent log row — its timestamp should match your PT clock
docker compose exec doris-fe \
  mysql -h127.0.0.1 -P9030 -uroot -p'VeloDB@demo123' -D demo_observability \
  -e "SELECT MAX(timestamp), NOW() AS doris_utc FROM otel_logs"
```

For PT users, `MAX(timestamp)` should be ~7h *behind* `NOW()` (Doris always
prints `NOW()` in its session timezone, default UTC).

### 6. Open Doris App on Grafana

`http://localhost:33000/a/doris-app` → Datasource: Doris → Database:
`demo_observability` → Table: `otel_logs` → Time Field: `timestamp` →
**Last 1 hour** → Query.

You should see hits.

## What if the laptop is in a different timezone than expected?

The `OTEL_TIMEZONE` value just needs to match what the browser uses. The
plugin reads `Date.prototype.getTimezoneOffset()` indirectly via dayjs. If
you switch laptops or fly across timezones, update `OTEL_TIMEZONE`,
recreate `otel-collector`, and add the new historical partitions:

```bash
sed -i '' 's|OTEL_TIMEZONE=.*|OTEL_TIMEZONE=Europe/London|' .env
docker compose up -d --force-recreate otel-collector
./scripts/backfill-otel-partitions.sh
```

## What if the dashboards already work but Doris App doesn't?

The four bundled dashboards (`1 Metrics`, `2 Logs`, `3 Trace`, `4 Trace
Detail`) use Grafana's `$__timeFilter()` macro, which Grafana templates
in **UTC milliseconds**. They're zone-agnostic regardless of OTEL
timezone, so they always work.

The Doris App plugin is the only consumer that ships browser-local
literals.

## Why doesn't Doris's `time_zone` setting fix this?

Doris `DATETIME` columns store **literal strings**. Setting
`SET GLOBAL time_zone='America/Los_Angeles'` only changes how `NOW()` and
`CURRENT_TIMESTAMP` are formatted — it doesn't reinterpret existing data
or apply offsets to inserts. The conversion happens **client-side** in
the OTEL exporter (when it formats the datetime to a string before
stream-load).

## Reference: what each setting controls

```
       browser
          │  dayjs(...).format()  →  literal string in browser-local TZ
          ▼
   doris-app plugin SQL  ─►  Doris stream-load
                                   │
                                   ▼
                          ┌────────────────────┐
                          │   rag_unified      │
                          │   otel_logs        │  DATETIME columns
                          │   …                │  store literals as-is
                          └────────────────────┘
                                   ▲
                                   │  literals formatted in OTEL_TIMEZONE
                                   │
                          OTEL collector  ←  exporter `timezone:` config
                                   ▲
                                   │  raw nanos
                                   │
                          telemetry-generator → otlp/4317
```

Both ends of the dataflow must format datetimes in the **same** zone for
literal comparison to find rows.
