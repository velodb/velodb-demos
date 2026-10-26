# Telco CDR demo (VeloDB / Doris)

A mobile operator with 5M subscribers (MSISDN 6580000000..6584999999, PLMN 525-01) and 3 months of usage:

| table | what | rows | Parquet |
|---|---|---|---|
| `call_log` | voice (VoLTE / VoNR / CS / video) and SMS CDRs, 5 per subscriber per day, + 0.5% duplicates | ~2.26B | ~57 GiB |
| `data_stream_log` | PGW/SMF data usage records (DPI app, bytes up/down, 5QI, rating group), 20 per subscriber per day, ~12.5 GB per subscriber per month | 9B | ~305 GiB |
| `rate_plan` | 7 prepaid/postpaid tariffs: allowances, overage, roaming (with a data cap), zero-rating | 7 | |

`queries.sql` has ad-hoc rating queries; `billing-app/` is the click-through dedup, bill run and bill dashboard.

## Pipeline

```
generate.sh ──> doris-parquet-s3-gen ──> S3  s3://<bucket>/telco-cdr/<table>/q3/batch-NNNNN/*.parquet
                (tools/, gen/*.spec)                          │   (streamed, nothing on local disk)
run-load.sh START_MONTH ──> one S3 Load job at a time ────────────┘──> Doris telco_demo.*
```

The generator writes compact raw draws (uniform ints, weighted choices) with a `day_off` slot 0..89 and no dates.
`run-load.sh` turns them into realistic records with S3 Load `SET` expressions:

- the 90 slots are spread over START_MONTH and the next two months (e.g. 202606 -> 2026-06-01 .. 2026-08-31)
- activity skewed across subscribers (index = 0.3u + 0.7u², the lowest 10% of MSISDNs make ~22% of records)
- diurnal curves: calls peak in office hours, data peaks at 21:00
- talk time exponential (mean 150 s, international 360 s, video 420 s); ring time by call result; Q.850 causes
- E.164 numbers per destination type (on-net, off-net mobile/fixed, international, 1800, 1900, 995)
- data volume log-uniform per app category; duration = volume / bitrate, cut at 3600 s (`TIME_LIMIT`)
- prepaid/postpaid and rate plan are functions of the MSISDN; each month a different 4% of subscribers take a
  3..7 day trip abroad, so both tables agree with each other and with `rate_plan`
- after the call_log batches, the same files are loaded again keeping a hashed 0.5% of the CDRs: exact duplicates,
  like mediation re-delivering files, for the dedup step to remove

Tables use **fixed day partitions** for the 3 loaded months (no auto or dynamic partitioning, no retention), so the
data never ages out: load once, demo forever. The billing app's tables copy that range (day partitions for
`cdr_call_log_deduplicate`, month partitions for `subscriber_bill`) when it starts, so start it after loading.

## Configure

All cluster settings are `VELODB_*` variables (see [`env.example`](env.example)). Pick one of:

```sh
cp env.example .env && $EDITOR .env          # a) a file (git-ignored)

export VELODB_HOST=<fe-or-lb-host>           # b) environment only: nothing on disk
export VELODB_USER=admin VELODB_PASSWORD='...'
```

Every script sources [`env.sh`](env.sh). **Environment variables take precedence over `.env`, and `.env` over the
defaults**, so any setting can change for a single run, e.g. `VELODB_DATABASE=telco_q3_smoke ./run-billing-app.sh`.
`ENV_FILE=/path/other.env` loads a different file.

| variable | default | |
|---|---|---|
| `VELODB_HOST` | (required) | FE / load balancer host |
| `VELODB_MYSQL_PORT` | `9030` | MySQL protocol port |
| `VELODB_USER` | (required) | |
| `VELODB_PASSWORD` | empty | |
| `VELODB_DATABASE` | `telco_demo` | holds `call_log`, `data_stream_log`, `rate_plan` and the billing app's tables |

Generating and loading also need an S3 target: `cp s3.env.example s3.env && chmod 600 s3.env` (git-ignored,
`S3_ENV_FILE=` for another file). The same precedence applies, and `S3_AK` / `S3_SK` default to
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`. The billing app needs only the `VELODB_*` settings.

Then check everything (read-only: config, tools, login, demo tables, S3 settings; exit code = failed checks):

```sh
./check_env.sh
```

Never commit `.env` or `s3.env`; only the `*.example` templates are tracked.

## Run

The folder is self-contained (generator vendored in `tools/`), so generation can run on a host with a fast network:

```sh
rsync -az --exclude target --exclude '*.log' telco-cdr-demo <gen-host>:workspace/   # includes .env / s3.env
ssh <gen-host>
cd ~/workspace/telco-cdr-demo
nohup ./generate.sh all > generate.log 2>&1 &              # wipes s3://<bucket>/telco-cdr/ first, then generates
tail -f generate.log
nohup ./run-load.sh 202606 --create --parallel 16 > run-load.log 2>&1 &   # START_MONTH required
./run-load.sh --status                                     # load jobs
./run-load.sh 202606 --parallel 16                         # resume after an interruption (no --create!)
```

Tables load one after the other (call_log with its duplicates, then data_stream_log); within a table the batch
folders (~10 GiB, one S3 Load job each) run `--parallel N` at a time, each job split across backends with
`load_parallelism` (env `LOAD_PARALLELISM`, default 16). Cancelled jobs are retried (`MAX_RETRIES`, default 2).
Labels carry the table's creation time, so `--create` reloads every batch and a plain rerun resumes. Doris caps
concurrent loads at `async_loading_load_task_pool_size` (50 on this cluster). Re-running `generate.sh` wipes the
bucket prefix and `--create` drops the tables, so use neither to resume. `run-load.sh` needs bash 4+.

Settings:

- `.env` / environment: the `VELODB_*` connection and database (see Configure).
- `s3.env` (mode 600): S3 target used by both scripts (bucket in the same region as the cluster, prefix `telco-cdr`).
- `gen/dest-*.toml`: generator row counts, file layout and tuning; the destination is filled in from `s3.env`.
- `tools/`: vendored `doris-parquet-s3-gen` (git) and `datagen` (local copy); build packages on AlmaLinux:
  `openssl-devel pkgconf-pkg-config perl cmake gcc-c++ libcurl-devel cyrus-sasl-devel zlib-devel`, plus a `mysql`
  client (`mariadb`) for `run-load.sh`.

## Billing pipeline web app (`billing-app/`, Go)

A single binary with an embedded UI that drives the rest of the pipeline inside Doris. Every step is an
`INSERT ... SELECT` executed by Doris; the app only orchestrates, tracks progress and renders.

```sh
./run-billing-app.sh                                 # VELODB_DATABASE=telco_demo on the VELODB_* cluster, http://localhost:8088
VELODB_DATABASE=telco_q3_smoke ./run-billing-app.sh  # any database with call_log, data_stream_log, rate_plan
```

On start it creates its own tables (`CREATE TABLE IF NOT EXISTS`):

| table | model | what |
|---|---|---|
| `cdr_duplicate_correction` | DUPLICATE KEY (msisdn, start_time, call_id), fixed day partitions | one row per re-delivered copy of a CDR, with `extra_copies` |
| `subscriber_bill` | UNIQUE KEY (bill_month, msisdn), month partitions | one rated bill per subscriber per month |
| `pipeline_run` | UNIQUE KEY (run_id) | run history shown in the UI |

In the UI, pick a bill month (only months that contain CDRs are offered), then:

1. **Deduplicate**: every run truncates the month's day partitions of `cdr_duplicate_correction`, then per day
   `INSERT ... SELECT ..., COUNT(*) - 1 FROM call_log WHERE <day> GROUP BY msisdn, start_time, call_id HAVING COUNT(*) > 1`
   (`PARALLEL` days at a time, default 2). Only the ~0.5% duplicates are written, so a month takes seconds rather
   than minutes, and the cost per day does not grow as the month fills. The UI shows CDRs delivered, unique CDRs and
   corrected copies. The loaded data already carries 0.5% duplicates; *Simulate re-delivered files* can add more
   (it modifies `call_log`).
2. **Bill run**: truncates the month's bill partition, then rates 48 MSISDN slices sized for equal work. Voice and
   SMS come from `call_log UNION ALL cdr_duplicate_correction` with a `sign` (+1 / -extra_copies), so every CDR
   counts exactly once without materialising a deduplicated copy. Rules:
   - domestic MO voice (per started minute), MO SMS and domestic data use the plan's allowance, then overage rates
   - international and premium minutes are charged per minute; toll-free, emergency and non-roaming MT calls are free
   - roaming voice (MO and MT), SMS (3x rate) and data use the roaming rates; roaming data stops at the plan's
     `roaming_data_cap` (bill-shock limit; the app adds this column to an older `rate_plan` on startup)
   - MMS/IMS data is always zero-rated; video and social data are zero-rated on plans that include it
   - 9% GST
3. **Bills & PDF**: revenue KPIs, revenue by plan, bill amount distribution, bill search (number, plan, type,
   roaming, minimum total, sort), a bill detail panel (charges, daily usage, data by app, itemised
   international/premium/roaming records) and a PDF bill per subscriber.

Each step shows a progress bar, ETA, a tile per task and every SQL statement with its duration and row count.
**Cancel** sends `KILL QUERY` to the step's connections.
