# Observability demo: log search in VeloDB

Generates synthetic log events (`event_time`, `host_id`, a typed `VARIANT` payload with city, country, severity and a
lorem-ipsum message), stream-loads them into a weekly-partitioned Doris table with inverted indexes on every payload
field, and serves a small log-search web UI on top of it. See [`create.sql`](create.sql) for the table and
[`queries.sql`](queries.sql) for sample queries.

## Quick start

You need Go ≥ 1.26 and a VeloDB Cloud warehouse or an Apache Doris ≥ 3.0 cluster.

### 1. Configure the connection

All connection settings are `VELODB_*` variables (see [`env.example`](env.example)). Pick one of:

```bash
cp env.example .env && $EDITOR .env          # a) a file (git-ignored)

export VELODB_HOST=<fe-or-lb-host>           # b) environment only: nothing on disk
export VELODB_USER=admin VELODB_PASSWORD='...'
```

**Environment variables take precedence over `.env`, and `.env` over the defaults**, so any setting can be changed for
one run without editing a file, e.g. `VELODB_TABLE=log_events_test ./recreateTable.sh`. Use `ENV_FILE=/path/other.env`
to load a different file; `.env` is optional.

| variable | default | |
|---|---|---|
| `VELODB_HOST` | (required) | FE / load-balancer host |
| `VELODB_USER` / `VELODB_PASSWORD` | (required) / empty | |
| `VELODB_MYSQL_PORT` | `9030` | MySQL protocol: DDL, web UI queries |
| `VELODB_STREAM_LOAD_PORT` | `8080` | FE HTTP: stream load |
| `VELODB_STREAM_LOAD_URL` | `http://$VELODB_HOST:$VELODB_STREAM_LOAD_PORT` | set for https / a different host |
| `VELODB_DATABASE` / `VELODB_TABLE` | `demo_observability` / `log_events` | |
| `ROWS_PER_SECOND` / `TOTAL_ROWS` | `100` / (required for loading) | `0` = unthrottled |
| `GENERATOR_THREADS`, `STREAMLOAD_*` | see `env.example` | loader tuning |

### 2. Create the table and load data

```bash
./recreateTable.sh                                  # DROP + CREATE from create.sql (the loader also creates it if missing)
ROWS_PER_SECOND=0 TOTAL_ROWS=10000000 ./runBackFill.sh   # bulk load, unthrottled
./runRealtime.sh                                    # realtime trickle, 100 rows/s by default
```

### 3. Search

```bash
./runWebServer.sh            # http://localhost:9273/logsearch/  (ADDR=:8080 to change)
```

The UI at `/logsearch/loadgen` can also start and stop a backfill from the browser.
