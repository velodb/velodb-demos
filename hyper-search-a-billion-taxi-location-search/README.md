# Doris hyper-search demo: taxi pings near MRT stations

One billion taxi GPS pings in a single Doris table, showcasing **partition pruning**
(`ts`), **bucket pruning** (`geohash6` distribution key) and **inverted indexes**
(`geohash6`, `geohash8`, `taxi_id`, `status`, `plate`) on one query:

```sql
SELECT taxi_id, plate, max_by(lat, ts) lat, max_by(lon, ts) lon, max(ts) last_seen
FROM taxi_ping
WHERE ts >= now() - INTERVAL 5 MINUTE              -- partition pruning
  AND geohash6 IN ('w21z7b', ...9 cells...)         -- bucket pruning (+ inverted index)
  AND status = 'AVAILABLE'                          -- inverted index
GROUP BY taxi_id, plate
HAVING st_distance_sphere(lon, lat, 103.8456, 1.2989) < 500;   -- exact radius (Dhoby Ghaut)
```

## Quick start

Everything runs from this directory. You need Python 3 and `curl` for all scripts, Go ≥ 1.22 for the feeder and the web
UI, Rust/cargo only to generate the data, `rs3` or any other S3 client only to upload it, and a VeloDB Cloud warehouse or
an Apache Doris ≥ 2.1 cluster.

### 1. Configure the connection

All settings are `VELODB_*` variables (see [`env.example`](env.example)). Pick one of:

```bash
cp env.example .env && $EDITOR .env          # a) a file (git-ignored)

export VELODB_HOST=<fe-or-lb-host>           # b) environment only: nothing on disk
export VELODB_USER=admin VELODB_PASSWORD='...'
```

Every script sources [`env.sh`](env.sh). **Environment variables take precedence over `.env`, and `.env` over the
defaults**, so you can change any setting for a single run without editing a file, e.g.
`VELODB_DATABASE=hs_test VELODB_BUCKETS=4 ./create_table_doris.sh`. Use `ENV_FILE=/path/other.env` to load a different file.

| variable | default | |
|---|---|---|
| `VELODB_HOST` | (required) | FE / load-balancer host |
| `VELODB_USER` / `VELODB_PASSWORD` | (required) / empty | |
| `VELODB_MYSQL_PORT` | `9030` | MySQL protocol: queries, DDL, Broker Load |
| `VELODB_STREAM_LOAD_PORT` | `8080` | FE HTTP: stream load (feeder, `--local` loads) |
| `VELODB_STREAM_LOAD_URL` | `http://$VELODB_HOST:$VELODB_STREAM_LOAD_PORT` | set for https / a different host |
| `VELODB_DATABASE` / `VELODB_TABLE` | `demo_hyper_search` / `taxi_ping` | |
| `VELODB_BUCKETS` | `32` | hash buckets on `geohash6` |
| `VELODB_REPLICATION_NUM` | `1` | |
| `VELODB_PARTITION_KEEP_DAYS` / `VELODB_PARTITION_AHEAD_DAYS` | `497` / `3` | dynamic-partition retention |

For the S3 Broker Load, also run `cp s3.yaml.example s3.yaml` and fill in the endpoint, region, keys, bucket and prefix
(`s3.yaml` is git-ignored). The Doris cluster must be able to reach that bucket.

### 2. Check that everything is valid

```bash
./check_env.sh        # read-only: config, tools, MySQL login, stream-load port, demo table, s3.yaml
```

It prints `ok` / `warn` / `FAIL` for each item and exits non-zero if any check fails. To run the checks by hand:

```bash
venv/bin/python doris_sql.py -e "SELECT version(), current_user()"          # MySQL port + credentials
curl -s "http://$VELODB_HOST:8080/api/health"                               # stream-load port
venv/bin/python doris_sql.py -e "SELECT count(*), min(ts), max(ts) FROM demo_hyper_search.taxi_ping"
```

(The first script you run creates `venv/`; `./ensure-venv.sh` does the same.)

### 3. Generate the history (once)

```bash
./run-gen.sh plan        # what would be generated (default 1 B rows / 490 days / 100K taxis, ~24 GB)
./run-gen.sh             # writes data/day_DDD_PP.parquet + state.json + manifest.json (resumable)
./upload.sh              # data/ -> s3://<Bucket>/<Prefix>/ from s3.yaml (resumable)
```

### 4. Bootstrap the table

```bash
./create_table_doris.sh             # CREATE DATABASE IF NOT EXISTS + DROP/CREATE TABLE (writes create_table.sql)
./create_table_doris.sh --dry-run   # only write create_table.sql to review it
```

### 5. Load the 1 billion rows

```bash
./truncate_doris_table.sh           # every demo run: empty the table + remove the BASE marker
./load_into_doris.sh                # BASE = now (UTC); 2 sequential Broker Loads from S3, polls until FINISHED
./load_into_doris.sh --chunks 5     # smaller loads for smaller BEs
./load_into_doris.sh --status --watch   # follow the jobs (from another terminal)
```

If a load fails, rerun `./load_into_doris.sh`: the marker `load_base_<db>.<table>.txt` keeps the BASE, and chunks that
already finished are skipped. Loading 1 B rows takes about 10–20 min, depending on cluster size. More detail under
[Demo cycle](#demo-cycle).

### 6. Catch up to "now" with the feeder

```bash
./run-feeder.sh --makeup-only       # fill the gap from the newest ping to now, then exit
./run-feeder.sh                     # ... then keep streaming pings in realtime (Ctrl-C to stop; rerun to catch up)
./run-feeder.sh --dry-run           # read Doris for the plan, simulate, load nothing
```

### 7. Run the web UI

```bash
./run-web-server.sh                 # http://localhost:8090
LISTEN=:8080 ./run-web-server.sh
```

### Small-scale test run (no S3, ~1 minute)

This runs every step above against a throwaway database that is configured only through the environment:

```bash
export VELODB_DATABASE=hs_smoke VELODB_BUCKETS=4 VELODB_PARTITION_KEEP_DAYS=10
./run-gen.sh --rows 300000 --days 3 --taxis 2000 --out "$PWD/data-small" generate
./check_env.sh
./create_table_doris.sh
./load_into_doris.sh --local data-small       # stream load of local parquet, same BASE / ts mapping
./run-feeder.sh --makeup-only --taxis 2000
./run-web-server.sh                           # open http://localhost:8090, pick an MRT station
venv/bin/python doris_sql.py -e "DROP DATABASE hs_smoke"     # clean up
```

## Layout

| path | what |
|---|---|
| `env.example` → `.env` | VeloDB connection + table settings (`.env` is git-ignored; environment overrides it) |
| `env.sh` | shared loader sourced by every script (precedence: environment > `.env` > defaults) |
| `check_env.sh` | read-only preflight: config, tools, connectivity, table, s3.yaml |
| `s3.yaml.example` → `s3.yaml` | S3 endpoint/region/credentials/bucket/prefix + rs3 alias for upload and Broker Load (git-ignored) |
| `generator/` | Rust generator (`taxi-gen`): writes the history as Parquet, **once** |
| `generator/generator.toml` | rows / days / taxis / ping interval / shift lengths / seed / output |
| `mrt_stations.csv` | ~125 Singapore MRT stations (approximate coords); shared by generator, feeder and web UI |
| `run-gen.sh` | build + run the generator (`./run-gen.sh plan`, `./run-gen.sh`) |
| `upload.sh` | `rs3 mirror -P 8 data/ <Alias>/<Bucket>/<Prefix>/`: resumable sync; extra flags (`--dry-run`, `--remove`, `--overwrite`) pass through |
| `create_table_doris.sh` | writes `create_table.sql` from the settings and runs it (`--dry-run` to only write) |
| `load_into_doris.sh` | Broker Load from S3 (or `--local DIR` stream load) with `ts = milliseconds_add(BASE, offset_ms)`; BASE marker `load_base_<db>.<table>.txt` |
| `truncate_doris_table.sh` | `TRUNCATE TABLE` (or `--drop` to DROP + CREATE) and remove the BASE marker: the "delete everything" step before a reload |
| `feeder/` + `run-feeder.sh` | Go realtime feeder: measures the ingest rate of the loaded history, makes up the gap from the newest ping to now, then keeps streaming pings at that rate (Doris stream-load Go SDK) |
| `webapp/` + `run-web-server.sh` | Go web UI: pick an MRT station (or click the map) and scrub a time slider; shows the taxis within the radius as of that moment, Doris query time and the partition/tablet pruning, click a taxi for its info and trail |
| `doris_sql.py` | pymysql-based SQL runner (`-e "..."`, `-G`, or a file); the mysql 9 client can't authenticate against Doris |
| `ensure-venv.sh` / `requirements.txt` | creates the project-local `venv/` on first use; all Python runs through it |
| `query_asof_raffles.sql` | example "as of T" radius query around Raffles Place |

## The "generate once, load forever" trick

Parquet files carry **no absolute timestamp**. Each row has `offset_ms <= 0`, milliseconds
before *load time*. Every demo run does:

```
DROP TABLE + CREATE TABLE (auto partition)      -- clean slate
BROKER LOAD ... SET (ts = milliseconds_add('<BASE>', offset_ms))
realtime feeder continues from <BASE> using data/state.json
```

`BASE` is computed once by the load script and used for every file, so the history has no
seams and `now() - INTERVAL 5 MINUTE` always lands on live data. Nothing is ever regenerated.

File `day_DDD_PP.parquet` covers part `PP` of day `DDD` (day 0 = most recent, offsets
`[-(D+1) days, -D days)`); each day is split into `ceil(rows_per_day / rows_per_file)` equal
time windows. The Broker Load glob `day_*.parquet` picks up all of them.
`state.json` lists the ~700 shifts still running at offset 0 (position, heading, status,
destination, remaining shift time) so the realtime feeder can continue them.

## Parquet schema (Broker Load maps by column name)

```
offset_ms  INT64    <= 0, ms before load time
taxi_id    INT32    0..taxis-1
plate      UTF8     e.g. SHA0000D  (prefix + series letter + 4 digits + check letter, unique)
company    UTF8     ComfortDelGro / SMRT / Trans-Cab / Premier / Prime / Strides
geohash6   UTF8     6-char geohash (~1.2 km x 0.6 km cell); bucket key + inverted index
geohash8   UTF8     8-char geohash (~38 m x 19 m cell), geohash6 is its prefix; inverted index for prefix/range lookups
                    (both precomputed: Doris has no geohash function)
lat, lon   DOUBLE
status     UTF8     AVAILABLE | ON_CALL | HIRED | OFFLINE (last ping of a shift)
speed_kmh  INT32
heading    INT32    0..359
```

## Generator

```bash
./run-gen.sh plan                       # dry run: rows/day, shifts, concurrency, size estimate
./run-gen.sh print-config               # effective config as TOML
./run-gen.sh                            # generate everything -> data/  (default 1B rows / 500 days / 100K taxis)
./run-gen.sh --days 495 --rows 1000000000 --taxis 100000   # CLI overrides
./run-gen.sh generate --from-day 0 --to-day 10             # subset; existing files skipped (add --force to redo)
./run-gen.sh generate -v                                   # per-file lines in addition to the progress line
./run-gen.sh sql --base '2026-08-26 10:00:00'              # print CREATE TABLE + BROKER LOAD for this schema
./upload.sh                             # push data/ to s3://<bucket>/<prefix>/
```

Data model: rows are clustered into **shifts** (1–4 h, one per taxi per day at most). A taxi
spawns near an MRT station, drives between stations (AVAILABLE → ON_CALL → HIRED → AVAILABLE),
pings every 30 s ± 3 s, and ends the shift with an OFFLINE ping. Default config: 2 M rows/day,
~6.7k shifts/day, ~700 taxis online at any moment, ~33 shifts (~10k rows) per plate over 500 days.

### How the work is split

- **Tasks = chunks of ~`rows_per_file` rows** (default 2 M, ~38 MB), not days. Each day has a
  deterministic *shift schedule* seeded by `(seed, day)`; the day is cut into equal time windows
  and each task replays the shifts overlapping its window, emitting only the pings inside it.
  Trajectories therefore stay continuous across file boundaries, per-day totals are exact, and
  the global total is exactly `rows`. `plan` prints the task list summary
  (`500 tasks = 500 days x 1 parts/day` for the defaults; `30 days x 17 parts/day` at `days=30`).
- **Multi-threaded**: rayon work-stealing over the task list, one task per core;
  peak memory ≈ `threads × rows_per_file × 60 B` (~1.7 GB on 14 cores), independent of `days`.
- **Resumable**: files are written as `.tmp` then renamed; on rerun existing files are skipped
  (counted, row count read from parquet metadata) unless `--force`. `--from-day/--to-day`
  restrict the range; any single file can be regenerated byte-identically.
- **Progress**: a live line on stderr — `files 37/500  rows 74.0M/1000M (7.4%)  gen 80.1M  1.41 GB
  12.3 M rows/s  elapsed 00:06  eta 01:15` — refreshed every 0.5 s on a TTY, one line every 10 s
  when redirected; `-v` adds one line per finished file.
- Output: `day_DDD_PP.parquet`, `state.json` (shifts active at offset 0), `manifest.json`
  (config, columns, per-file rows/bytes/offset windows, totals).

Expect ~24 GB for 1 B rows (ZSTD-3, dictionary-encoded strings; geohash8 is the least compressible column).

## Demo cycle

```bash
./upload.sh                              # once: data/ -> s3://<Bucket>/<Prefix>/  (~26 GB)
./create_table_doris.sh                  # once (or whenever the DDL changes)

./truncate_doris_table.sh                # every run: delete everything + remove the base marker
./load_into_doris.sh                     # every run: BASE = now (UTC) written to the marker, then 2 sequential
                                         #   Broker Loads (~245 days each); rerun as often as needed until all
                                         #   chunks are FINISHED - the marker keeps BASE, finished chunks are skipped
#   ./load_into_doris.sh --chunks 5      #   smaller loads for smaller BEs (one job holding memtables for all
#                                        #   494x32 tablets OOMs a 32 GB BE)
#   ./load_into_doris.sh --recreate      #   DROP + CREATE first (new BASE)
#   ./load_into_doris.sh --status        #   all jobs of the current BASE (from the marker); --watch to follow
#   ./load_into_doris.sh --no-wait       #   submit only
#   ./load_into_doris.sh --prefix hyper-search-test --glob day_489_01.parquet   # smoke test on one file
#   ./load_into_doris.sh --local data-small  #   no S3: stream-load local parquet files (one label per file)
./run-feeder.sh                          # make up the gap since the newest ping, then stream in realtime
```

Doris runs concurrently submitted Broker Loads in parallel, not one after another, so sequencing is done by
the script: it submits chunk *n*, polls `SHOW LOAD` until `FINISHED`, then submits *n+1* — all with the same
BASE (labels `taxi_ping_<BASE>_c01…`). The **base marker** `load_base_<db>.<table>.txt` makes this idempotent: while it
exists every run continues the same BASE; `truncate_doris_table.sh` deletes it so the next load starts fresh.
(The feeder does not need it: it starts from `max(ts)` in the table.) `LOAD_EXEC_MEM_LIMIT=<bytes>` adds an
`exec_mem_limit` property, `LOAD_BASE_FILE=<path>` moves the marker.

Smoke-tested: one 1.08 M-row file loads in ~10 s with the offsets mapped 490 days back; expect the
full 1 B rows to take on the order of 10–20 min depending on cluster size (`LOAD_TIMEOUT` default 4 h).

## Web UI

```bash
./run-web-server.sh            # builds webapp/ and serves http://localhost:8090 (LISTEN=:8080 to change)
```

- **Where**: MRT station picker (filterable) or click/drag on the map; radius 100–3000 m; look-back window.
- **When**: scrubbable slider (span 1 h … all 490 days), ⟨ ⟩ steps, play at 30 s … 1 day per tick,
  `Latest` jumps to `max(ts)` (so it follows the realtime feeder). ←/→ and space work as shortcuts.
- **Doris panel**: query time, `partitions x/494`, `tablets y/32`, geohash6 cells (drawn on the map),
  taxis found, the SQL, and a *Compare* button that reruns without the cell predicate.
- **Taxis**: arrows show heading, colour = status; click for plate/company/status/last ping/age/distance
  and *Show trail ±15 min* (inverted index on `taxi_id`).
- API: `GET /api/meta`, `GET /api/search?lat&lon&t=RFC3339&radius&lookback[&mode=nocell]`,
  `GET /api/trail?taxi_id&t&minutes`. Map tiles come from CARTO (internet needed); everything else is embedded.

## Doris side

Partitioning: `AUTO PARTITION BY RANGE(date_trunc(ts,'day'))` creates day partitions as the load
arrives, and the dynamic-partition properties handle retention — `PARTITION_KEEP_DAYS=497`
(`dynamic_partition.start=-497`, older partitions dropped) and `PARTITION_AHEAD_DAYS=3`
(`dynamic_partition.end=3`, future partitions pre-created), set by `VELODB_PARTITION_KEEP_DAYS` / `VELODB_PARTITION_AHEAD_DAYS`.
Partitions are UTC-day aligned, so the Broker Load `BASE` must be a UTC timestamp.

```bash
./create_table_doris.sh                 # DROP + CREATE $VELODB_DATABASE.$VELODB_TABLE, writes create_table.sql
./create_table_doris.sh --dry-run       # only write create_table.sql
venv/bin/python doris_sql.py -e "SHOW PARTITIONS FROM demo_hyper_search.taxi_ping"
venv/bin/python doris_sql.py -G -e "SHOW CREATE TABLE demo_hyper_search.taxi_ping"
```

## Realtime feeder

`./run-feeder.sh` keeps the table alive after a load (and after any pause). It is a separate
Go binary (`feeder/`) that:

1. finds the newest ping (`max(ts)`, recent partitions first), and measures the ingest rate of the
   24 hours before it, aligned to full hours (`--rate-window`). With a full window it also keeps the
   hour-of-day shape; with a table younger than the window it uses a flat average; with an empty
   table it makes up the last `--empty-backfill` (24h) at `--default-rate` (1B/490/24 ≈ 85K rows/h);
2. reads plate/company per taxi (last `--fleet-window`, 30 d) and the shifts in progress at the
   newest ping, so the new pings continue the same trajectories with the same identities;
3. **makes up the gap** from that ping to now with the same simulation as the Rust generator
   (`feeder/sim.go` is a port of `generator/src/sim.rs`), as fast as the cluster accepts it, with a
   progress bar (sim time, rows generated / loaded, in-flight, ETA). It prints `✔ up to date` once
   every row is confirmed by Doris;
4. then runs in **realtime**: pings are emitted when the wall clock reaches them, flushed as one
   stream load every `--flush` (5 s), with a status line every `--report` (10 s): sim time reached,
   on-shift taxis, rows issued, rows Doris confirmed loaded (summed from the stream-load responses),
   rows in flight / failed, loads, rows/min.

Rows go through the Doris stream-load Go SDK
(`github.com/apache/doris-sdk/stream-load-sdks/doris_go_stream_load`, CSV mode, basic auth from
`VELODB_USER` / `VELODB_PASSWORD`, POSTed to `VELODB_STREAM_LOAD_URL`, labels `taxi_feed_*`). Ctrl-C drains the in-flight loads and prints a summary; rerunning
simply fills the gap since the stop — so this is also the "catch up" tool after a truncate + reload.

```bash
./run-feeder.sh                     # make up + realtime
./run-feeder.sh --makeup-only       # catch up and exit (e.g. from cron)
./run-feeder.sh --dry-run           # read Doris for the plan, simulate, load nothing (SDK FakeSend)
./run-feeder.sh --rate 200000       # force rows/hour
./run-feeder.sh --table other_table # any table with the taxi_ping schema
./run-feeder.sh --help              # sim knobs: --taxis --ping-secs --jitter-secs --shift-min/max --seed ...
```

Sizing: at the generator's rate (~85K rows/h ≈ 700 taxis on shift) realtime is ~24 rows/s, i.e. a
~2.5 KB stream load every 5 s; a 32 h gap is ~2.8M rows and is generated in seconds, so make-up is
bounded by the stream loads (32 MiB bodies, `--workers 2`).

## Next steps (not built yet)

- `queries.sql`: the EXPLAIN ladder (partition → bucket → inverted index)
- search CLI: pick an MRT station → geohash6 + 8 neighbours → query
