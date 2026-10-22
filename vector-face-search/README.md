# Face similarity search: 500k celebrity faces → ArcFace vectors → Doris HNSW

Upload a photo (or use the webcam), and Doris finds the most similar faces among
~490,000 face photos of celebrities (65k names, 12.4k with a verified identity) using an **HNSW ANN index**. SQL filters
(gender, birth year, label quality, …) run in the same query. A second ANN table
with one "average face" per person answers *"which celebrity does this face look like?"*.

```
./run-embedding.sh   -> faces.db     download IMDB-WIKI faces (9.7 GB) + detect/align/embed every face (SQLite)   offline
./run-export.sh      -> export/      JSON lines: faces-*.jsonl (vector + labels + thumbnail), people-0000.jsonl  offline
./run-load.sh        -> Doris        <db>.faces + <db>.people, both with HNSW indexes (stream load)
./run-web-server.sh  -> http://localhost:8091
./run-search.sh photo.jpg            command-line search
```

## Quick start

Everything runs from this directory. You need Python 3.11–3.14 (onnxruntime has no wheels for 3.15 yet), `curl`, `jq`
and `unzip`, and a VeloDB Cloud warehouse or an Apache Doris cluster with ANN index support (Doris ≥ 4.0).
The first script you run creates `venv/` from `requirements.txt` (`./ensure-venv.sh`; it picks the newest supported
`python3.x`, or set `PYTHON=python3.12`), and the first script that needs them downloads the InsightFace `buffalo_l`
models into `models/` (~280 MB, `./ensure-models.sh`). Embedding runs on the Apple GPU through CoreML and falls back
to CPU elsewhere.

### 1. Configure the connection

All settings are `VELODB_*` variables (see [`env.example`](env.example)). Pick one of:

```bash
cp env.example .env && $EDITOR .env          # a) a file (git-ignored)

export VELODB_HOST=<fe-or-lb-host>           # b) environment only: nothing on disk
export VELODB_USER=admin VELODB_PASSWORD='...'
```

Example `.env` (placeholder values; never commit real ones):

```bash
VELODB_HOST=lb-0123456789abcdef.elb.ap-southeast-1.amazonaws.com
VELODB_MYSQL_PORT=9030
VELODB_STREAM_LOAD_PORT=8080
VELODB_USER=admin
VELODB_PASSWORD=s3cr3t-Example-Only
VELODB_DATABASE=face_search_demo
```

Every Doris-facing script sources [`env.sh`](env.sh), and the Python tools read the same `.env` via
[`doris_sql.py`](doris_sql.py). **Environment variables take precedence over `.env`, and `.env` over the defaults**, so
you can change any setting for a single run without editing a file, e.g. `VELODB_DATABASE=face_test ./run-load.sh`.
Use `ENV_FILE=/path/other.env` to load a different file.

| variable | default | |
|---|---|---|
| `VELODB_HOST` | (required) | FE / load-balancer host |
| `VELODB_USER` / `VELODB_PASSWORD` | (required) / empty | |
| `VELODB_MYSQL_PORT` | `9030` | MySQL protocol: queries, DDL |
| `VELODB_STREAM_LOAD_PORT` | `8080` | FE HTTP: stream load (`run-load.sh`) |
| `VELODB_STREAM_LOAD_URL` | `http://$VELODB_HOST:$VELODB_STREAM_LOAD_PORT` | set for https / a different host |
| `VELODB_DATABASE` | `face_search_demo` | holds the `faces` and `people` tables |
| `VELODB_FACES_BUCKETS` / `VELODB_PEOPLE_BUCKETS` | `16` / `4` | hash buckets |
| `VELODB_REPLICATION_NUM` | `1` | 1 on VeloDB Cloud / single-BE, 3 on self-managed HA |

### 2. Check that everything is valid

```bash
./check_env.sh        # read-only: config, tools, MySQL login, stream-load port, demo tables, models, faces.db, export/
```

It prints `ok` / `warn` / `FAIL` for each item and exits non-zero if any check fails. To run the checks by hand:

```bash
venv/bin/python doris_sql.py -e "SELECT version(), current_user()"        # MySQL port + credentials
curl -s "http://$VELODB_HOST:8080/api/health"                             # stream-load port
venv/bin/python doris_sql.py -e "SELECT count(*) FROM faces"              # demo table (in VELODB_DATABASE)
```

### 3. Build the embeddings (offline, once)

```bash
./run-embedding.sh                    # download all 20 shards (9.7 GB, resumable) and embed them into faces.db
./run-embedding.sh --max-shards 2     # quick trial: first 2 shards (~51k photos)
WORKERS=4 ./run-embedding.sh          # fewer worker processes (default 6)
./run-export.sh                       # faces.db -> export/*.jsonl (label_score + per-person centroids)
```

Embedding everything takes about 30 min on an M3 Max; finished shards are skipped on re-run. Neither step needs Doris.

### 4. Bootstrap the tables

```bash
./create_table_doris.sh               # CREATE DATABASE / TABLE IF NOT EXISTS (writes create_table.sql)
./create_table_doris.sh --recreate    # DROP + CREATE both tables (after a schema change)
./create_table_doris.sh --dry-run     # only write create_table.sql to review it
```

`run-load.sh` runs the first form for you, so this step is optional.

### 5. Load the data

```bash
./run-load.sh                         # create tables if missing, stream-load every export/*.jsonl chunk
./run-load.sh --truncate              # empty both tables first: they are duplicate-key, a plain re-load doubles the rows
./run-load.sh --recreate              # DROP + CREATE first
FROM=faces-0005 ./run-load.sh         # resume after a failure: skip chunks sorting before this name
```

Each chunk is one stream load with its own label, retried up to 5 times with the same label (so a retry never double-loads).

### 6. Run the web server

```bash
./run-web-server.sh                   # http://localhost:8091
PORT=9000 ./run-web-server.sh
./run-web-server.sh --host 0.0.0.0    # listen on all interfaces (default 127.0.0.1)
./run-search.sh photo.jpg --top 10 --where "gender = 'female'"     # same search from the command line
```

The server loads the models once (warming up CoreML takes a few seconds), then serves `static/index.html` and the JSON
API. Upload, drag & drop, paste or the webcam all work.

### Small-scale test run (~5 minutes, mostly the download)

This runs every step above with 20 photos, against a throwaway database configured only through the environment:

```bash
export VELODB_DATABASE=face_search_smoke VELODB_FACES_BUCKETS=1 VELODB_PEOPLE_BUCKETS=1
./run-embedding.sh --max-shards 1 --limit 20    # downloads shard 0 (~490 MB) + models, embeds 20 photos
./run-export.sh                                  # 20 faces, a couple of people centroids
./check_env.sh
./run-load.sh
./run-search.sh some-photo.jpg --top 3
./run-web-server.sh                              # open http://localhost:8091
venv/bin/python doris_sql.py -D information_schema -e "DROP DATABASE face_search_smoke"   # clean up
rm faces.db && rm -r export                      # --limit never marks the shard done, but start clean anyway
```

## Pieces

| | What | Notes |
|---|---|---|
| Dataset | [IMDB-WIKI faces](https://huggingface.co/datasets/ljnlonoljpiljm/imdb_wiki_faces) (Rothe et al., ETH Zürich) | 511,980 face crops from IMDb and Wikipedia, labelled with name, date of birth, photo year, gender. Academic/non-commercial use. |
| Face detector | SCRFD-10G (`det_10g.onnx`, InsightFace `buffalo_l`) | Boxes + 5 landmarks. |
| Face embedding | ArcFace ResNet-50 trained on WebFace600K (`w600k_r50.onnx`) | 512-d vectors, L2-normalized, so `inner_product` = cosine. InsightFace models are for non-commercial research use. |
| Runtime | onnxruntime + CoreML (Apple GPU) | No `insightface` package needed. Static-shape copies of the models are generated automatically because CoreML needs them. About 300 photos/s end-to-end on an M3 Max; falls back to CPU elsewhere. |
| Vector DB | Doris / VeloDB HNSW (`inner_product`) | See `create_table_doris.sh`. |

## How good is the score?

Cosine similarity between ArcFace vectors (the UI uses the same bands):

| score | meaning |
|---|---|
| ≥ 0.50 | same person (very high confidence) |
| 0.35 – 0.50 | likely the same person (different age, lighting, pose) |
| 0.20 – 0.35 | resemblance / look-alike |
| < 0.20 | different people |

Scores against the `people` centroids run a little higher than photo-to-photo
scores, because averaging many photos removes pose and lighting noise.

## The labels are noisy, so every face gets `label_score`

IMDB-WIKI was collected automatically. An IMDb still tagged with one actor often
shows a co-star, and roughly a third of the IMDb-sourced photos show someone
other than the named person. `face-export.py` fixes this without manual work:

1. For each name, find the largest group of photos that agree with each other (cosine ≥ 0.4).
   This group is the person's real identity.
2. `label_score` = cosine between a photo and the centroid of that group, computed
   leave-one-out so a photo never vouches for itself. A score ≥ 0.35 means the label is
   trustworthy. A score near 0 means the photo shows someone else.
3. The group's normalized centroid becomes the person's row in `people`.

The UI's "Only photos whose name label is verified" checkbox is simply
`WHERE label_score_pct >= 35` added to the ANN query (`label_score_pct` is an indexed INT copy of the score: Doris cannot put an inverted index on FLOAT, and an unindexed filter makes the ANN query fall back to brute force).

## Tables

`<VELODB_DATABASE>.faces` (default `face_search_demo.faces`): one row per photo.

| column | |
|---|---|
| `face_id` | `shard * 100000 + row` in the HF dataset |
| `name`, `gender`, `birth_year`, `photo_year`, `age`, `source` (`imdb`/`wiki`) | dataset labels |
| `n_faces`, `det_score`, `face_px`, `img_w`, `img_h` | detection info (the main face of the photo is embedded) |
| `person_faces`, `label_score`, `label_score_pct` | photos under this name; label consistency (see above); `label_score_pct` = round(score × 100), the indexed copy used in filters |
| `image` | base64 JPEG thumbnail (160 px), served by the UI straight from Doris |
| `embedding` | `ARRAY<FLOAT>` 512-d, **HNSW index** |

`<VELODB_DATABASE>.people`: one row per identity: `name`, `gender`, `birth_year`,
`photos`, `verified_photos`, `face_id` (most typical photo), `embedding` (centroid, **HNSW index**).

## Query shape

```sql
SELECT /*+ SET_VAR(hnsw_ef_search=64) */ face_id, name, label_score,
       inner_product_approximate(embedding, [0.0435, -0.0832, ...512 floats]) AS score
FROM faces
WHERE gender = 'female' AND label_score_pct >= 35        -- optional hybrid filters (indexed columns only)
ORDER BY score DESC
LIMIT 30;
```

`inner_product_approximate(...)` + `ORDER BY ... DESC LIMIT k` is the shape the ANN
index serves. With a `WHERE`, Doris returns the top-k **among the rows that pass
the filter**. Use `inner_product(...)` (no `_approximate`) for an exact brute-force
scan to compare recall and latency. `sample_query.sql` has runnable examples with a
real query vector (`venv/bin/python doris_sql.py sample_query.sql`).

## Making sure searches use the HNSW index

Verified with query profiles (`SET enable_profile = true`, then check the scan
counters `AnnIndexTopNSearchCnt` for HNSW vs `AnnIndexFallbackBruteForceCnt`).
On the full table (488,541 rows, 16 segments):

| query | path | rows read |
|---|---|---|
| no filter | HNSW in 16/16 segments | 480 |
| `label_score_pct >= 35` (UI default) | HNSW + inverted-index bitmap | 480 |
| `people` centroids | HNSW in 4/4 segments | 48 |
| narrow filters, e.g. `gender = 'female' AND label_score_pct >= 35` (23% of rows) | exact scan of the filtered rows | ~113k |

Three things decide it:

1. **Every filter column needs an inverted index.** A predicate Doris can't answer
   from an index makes the whole ANN query brute-force. Doris can't put an inverted
   index on FLOAT, which is why the table has the INT `label_score_pct` next to `label_score`.
2. **`inverted_index_skip_threshold=100`** (set per query by the UI). By default Doris
   skips the inverted index for a predicate matching more than 50% of rows, and
   `label_score_pct >= 35` matches 52%. That silently disabled HNSW.
3. **`ann_index_candidate_rows_percent_threshold` (default 0.3)** is left alone. When
   the filters leave fewer than 30% of rows, Doris scans those exactly. Forcing HNSW
   there measured recall@30 of only 0.67–0.84, versus 1.0 for the exact scan, for
   about 130 ms saved.

`hnsw_ef_search` defaults to 128 in the UI and CLI: recall@30 is 0.99 against exact
search (0.97 at 64).

## Files

| path | what |
|---|---|
| `env.example`, `env.sh` | configuration template and the loader every Doris-facing script sources |
| `check_env.sh` | read-only preflight (step 2) |
| `ensure-venv.sh`, `ensure-models.sh` | create `venv/`, download `models/` on first use |
| `run-embedding.sh` → `face-gen.py` | download shards (resumable) and embed them into `faces.db`. `--max-shards N`, `--limit N` (first N photos per shard, smoke tests), `WORKERS` |
| `run-export.sh` → `face-export.py` | computes `label_score` and person centroids, writes `export/` |
| `create_table_doris.sh` | writes and runs `create_table.sql` (both tables, HNSW + inverted indexes) |
| `run-load.sh [--truncate\|--recreate]` | creates the tables if missing and stream-loads every chunk |
| `run-web-server.sh` → `face-server.py`, `static/` | web UI on :8091. Multi-face photos show clickable boxes; clicking any result searches with that face's vector, read back from Doris |
| `run-search.sh` → `face-search-doris.py` | CLI search. `--print-vector` prints a vector literal for hand-written SQL |
| `doris_sql.py` | runs SQL on Doris (`-e "SQL"` or a file); the Homebrew mysql 9 client can't authenticate to Doris |
| `facemodel.py` | detection, alignment and embedding, reusable on its own |
| `sample_query.sql` | runnable ANN / hybrid queries with a real query vector |

Local, git-ignored state: `.env`, `venv/`, `models/`, `data/` (parquet shards), `faces.db`, `export/`, `create_table.sql`.
