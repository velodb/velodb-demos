# Hybrid vector search demo: 1M Amazon products → BGE embeddings → Doris HNSW

Streams product metadata from the public [Amazon Reviews 2023](https://amazon-reviews-2023.github.io/)
dataset (McAuley Lab, UCSD — `Home_and_Kitchen`, 3.7M listings), embeds every
product with `BAAI/bge-small-en-v1.5` (384-dim) on the Apple GPU, and loads
metadata + vectors into Doris / VeloDB, where an **HNSW ANN index** on the
vector column and **inverted indexes** on the text/category columns let you
combine semantic similarity with ordinary SQL `WHERE` filters.

```
./run-embedding.sh --count 1000000   ->  products.db          (SQLite: metadata + vector)
./run-export.sh                      ->  export/*.jsonl       (JSON lines, vector as float array)
./run-load.sh                        ->  hybrid_search_demo.products in Doris (HNSW + inverted indexes)
./run-doris-search.sh "cozy throw blanket" --where "price < 40 AND average_rating >= 4.5"
./run-ui.sh                          ->  http://localhost:9121  (web UI with hybrid-search knobs)
```

## Configure

All Doris settings are `VELODB_*` variables (see [`env.example`](env.example)). Pick one of:

```sh
cp env.example .env && $EDITOR .env          # a) a file (git-ignored)

export VELODB_HOST=<fe-or-lb-host>           # b) environment only: nothing on disk
export VELODB_USER=admin VELODB_PASSWORD='...'
```

Every script sources [`env.sh`](env.sh). **Environment variables take precedence over `.env`, and `.env` over the
defaults**, so any setting can change for a single run, e.g. `VELODB_DATABASE=hybrid_search_test ./run-load.sh`.
`ENV_FILE=/path/other.env` loads a different file.

| variable | default | |
|---|---|---|
| `VELODB_HOST` | (required) | FE / load balancer host |
| `VELODB_MYSQL_PORT` | `9030` | MySQL protocol port |
| `VELODB_STREAM_LOAD_PORT` | `8080` | FE HTTP port (stream load in `run-load.sh`) |
| `VELODB_USER` | (required) | |
| `VELODB_PASSWORD` | empty | |
| `VELODB_DATABASE` | `hybrid_search_demo` | database of the `products` table |

**Python:** the deps are pinned in [`requirements.txt`](requirements.txt). The first script you run creates `./venv`
from it ([`ensure-venv.sh`](ensure-venv.sh), ~2 minutes, torch is ~1 GB), and it is rebuilt when `requirements.txt`
changes. The venv is git-ignored and disposable: `rm -rf venv` gives a clean reinstall. torch has no wheels for
brand-new Python releases, so the newest of python3.14 .. python3.11 is used (`PYTHON=python3.12` to choose).

Check everything (config, tools, venv, MySQL login, stream-load port, demo table, local data):

```sh
./check_env.sh
```

Never commit `.env`, `venv/` or the generated data (`products.db`, `export/`): `.gitignore` keeps them out.

## Scripts

| Script | What it does |
|---|---|
| `run-embedding.sh [--count N] [--category C]` | Streams the category's JSONL from Hugging Face, keeps the first N usable products, embeds them (MPS fp16, ~360–700 products/s on an M3 Max; `--device cpu` to force CPU). Resumable at every stage; delete `products.db` to re-select. |
| `run-search.sh "text"` | Local brute-force cosine search over `products.db` (the baseline, reports embed vs scan time). `--print-vector "text"` prints a vector literal for hand-written SQL. |
| `run-export.sh` | Writes `export/products-NNNN.jsonl` (20,000 rows each) + `manifest.json`. |
| `run-load.sh [--truncate\|--recreate]` | Runs `create_doris_table.sql`, stream-loads every chunk, prints the row count. The table is duplicate-key (required by the ANN index), so `--truncate` before re-loading; `--recreate` after a schema change. |
| `run-doris-search.sh "text" [--where ...]` | Embeds the text and runs an ANN top-k query on Doris. Interactive when no text is given. |
| `run-ui.sh` | Web UI: Go server (`ui/`) + `embed-server.py` (query text → vector). |
| `doris-sql.py file.sql` / `-e "SQL"` | Runs SQL on Doris (the Homebrew `mysql` client cannot authenticate to Doris). |
| `sample_query.sql` | Runnable examples with a pre-computed query vector: `venv/bin/python doris-sql.py sample_query.sql` (with `VELODB_*` exported) |

## Table columns (filterable in `--where` and the UI)

| Column | Type | Example values | Coverage |
|---|---|---|---|
| `product_id` | VARCHAR | `B07R3DYMH6` (parent ASIN → `amazon.com/dp/<id>`) | 100% |
| `product_name` | VARCHAR, inverted index (english) | | 100% |
| `product_description` | STRING, inverted index (english) | description + feature bullets | 100% |
| `main_category` | VARCHAR | `Amazon Home` (84%), `Tools & Home Improvement`, … | 97% |
| `product_type` | VARCHAR, inverted index | leaf category: `Coffee Mugs`, `Area Rugs`, `Throw Pillow Covers`, `Posters & Prints` (1,853 distinct) | 97% |
| `brand` | VARCHAR, inverted index | store name | ~99% |
| `color`, `material`, `style` | VARCHAR (color/material inverted) | `Black`, `White`; `Ceramic`, `Stainless Steel`, `Wood` | ~76% / ~65% / ~38% |
| `categories` | ARRAY\<STRING\>, inverted index | `["Home & Kitchen", "Kitchen & Dining", "Dining & Entertaining", …]` — 2nd element is the department | 97% |
| `browse_node` | VARCHAR | same path joined with ` / ` | 97% |
| `image_url` | VARCHAR | main product image | ~100% |
| `price` | DOUBLE | USD | ~55% |
| `average_rating` | DOUBLE | 1–5 | 100% |
| `rating_number` | INT | number of ratings | 100% |
| `weight_lb` | DOUBLE | parsed from "Item Weight" | ~85% |
| `height_in`, `length_in`, `width_in` | DOUBLE | parsed from "Product Dimensions" | ~65% |
| `embedding` | ARRAY\<FLOAT\>(384), HNSW index | L2-normalized, so `inner_product` = cosine | 100% |

Coverage is whatever Amazon publishes; a filter on `price` or dimensions
naturally excludes rows without a value. Live facet counts: last three queries
in `sample_query.sql`, or the UI's autocomplete lists.

## Using `--where` (hybrid search)

`--where` takes any Doris SQL predicate. It is placed in the `WHERE` clause of
the ANN query, so Doris returns the top-k most similar products **among the rows
that pass the filter** — not "top-k, then filter", which would return fewer than
k rows.

Price / rating / popularity:

```bash
./run-doris-search.sh "cozy throw blanket for a grey sofa" --where "price < 40 AND average_rating >= 4.5"
./run-doris-search.sh "espresso machine" --where "price BETWEEN 100 AND 300 AND rating_number >= 1000"
```

Categories (leaf `product_type`, or any level of the `categories` path):

```bash
./run-doris-search.sh "mug that keeps coffee hot" --where "product_type = 'Coffee Mugs'"
./run-doris-search.sh "rug for a kids room"       --where "array_contains(categories, 'Area Rugs')"
./run-doris-search.sh "minimalist wall clock"     --where "array_contains(categories, 'Home Décor Products') AND price < 50"
```

Brand / color / material:

```bash
./run-doris-search.sh "insulated tumbler" --where "brand = 'YETI'"
./run-doris-search.sh "dining chair"      --where "material = 'Wood' AND color = 'Black'"
```

Full-text conditions on the inverted indexes (`MATCH_ANY` = any term,
`MATCH_ALL` = every term, `MATCH_PHRASE` = exact phrase):

```bash
./run-doris-search.sh "bed sheets"      --where "product_description MATCH_ALL 'egyptian cotton'"
./run-doris-search.sh "coffee"          --where "product_name MATCH_PHRASE 'french press'"
./run-doris-search.sh "cutting board"   --where "product_description MATCH_ANY 'bamboo walnut' AND price < 30"
```

Numeric size filters (inches / pounds):

```bash
./run-doris-search.sh "storage bin" --where "height_in < 12 AND weight_lb < 3"
```

Other flags:

```bash
./run-doris-search.sh -k 20 "table lamp"        # more results (default 10)
./run-doris-search.sh "table lamp" --show-sql   # print the SQL sent to Doris
./run-doris-search.sh "table lamp" --exact      # inner_product() brute force over 1M rows instead of the HNSW index
```

Interactive mode keeps the model loaded and the connection open between queries:

```
$ ./run-doris-search.sh
doris> where price < 30 AND average_rating >= 4.5
doris [price < 30 AND average_rating >= 4.5]> insulated tumbler
doris [...]> where          # clear the filter
doris> sql                  # toggle SQL echo
```

## Web UI (`./run-ui.sh`)

Go server in `ui/` (single binary, UI embedded) + `embed-server.py` (BGE model
on the GPU, answers `POST /embed`). Open http://localhost:9121.

Knobs in the sidebar:

* **Mode** — *Hybrid* (HNSW + filters), *Vector only* (ignores filters),
  *Exact scan* (`inner_product`, no index — the latency baseline),
  *Keyword only* (inverted-index `MATCH_ANY`, no vector).
* **Top K**, **HNSW `ef_search`** (Doris session variable `hnsw_ef_search`,
  default 32: larger = better recall, slower), **min similarity**.
* **Scalar filters** — price range, min rating, min number of ratings,
  category (2nd level), product type (leaf), brand, color, material, with
  autocomplete from live facet counts.
* **Full-text condition** — must-contain text on description or name, as
  any-term / all-terms / exact-phrase `MATCH`.

Every search shows embed time vs Doris time, and the exact SQL that ran
(vector elided). Product titles link to the Amazon page.

API: `POST /api/search` with the JSON fields shown in `ui/main.go`
(`searchRequest`), `GET /api/facets`, `GET /api/info`.

## Measured on the loaded data (1,000,000 rows)

8 distinct queries, top-10, `hnsw_ef_search = 64`, Doris SQL cache off,
VeloDB Cloud with 2 BEs (Doris-side time only; embedding the query on the
M3 Max GPU adds ~35–90 ms):

| Query shape | median | min | max |
|---|---|---|---|
| HNSW (`inner_product_approximate`) | **37 ms** | 33 | 104 |
| Exact scan (`inner_product`) | 235 ms | 217 | 398 |
| HNSW + `price < 50 AND average_rating >= 4.5` | 118 ms | 110 | 126 |
| Exact + same filter | 109 ms | 99 | 118 |

Recall@10 of HNSW against the exact scan: 100% on all 8 queries.

The unfiltered case shows the index (~6× faster than scanning 1M vectors).
With a selective filter Doris evaluates the predicate first and the ANN
search runs over the surviving rows, so index and scan converge — the win
there is that the filter is applied *inside* the top-k, which the local
brute-force script cannot do without scanning everything. For comparison,
`./run-search.sh` scans the same 1M vectors in NumPy in ~200 ms with the
whole 1.5 GB matrix resident in RAM.

## The SQL behind it

```sql
SET hnsw_ef_search = 64;
SELECT product_id, product_name, price, average_rating,
       inner_product_approximate(embedding, [0.012, -0.034, ... 384 floats ...]) AS score
FROM hybrid_search_demo.products
WHERE price < 40 AND average_rating >= 4.5        -- your --where predicate
ORDER BY score DESC
LIMIT 10;
```

`inner_product_approximate(...)` + `ORDER BY ... DESC LIMIT k` is the shape the
planner turns into an HNSW lookup (`EXPLAIN` shows `ANN SORT LIMIT: 10`).
Replace it with `inner_product(...)` for an exact scan. The vector literal for
any text: `./run-search.sh --print-vector "your text"`.

## Notes

* Query and product vectors come from the same model; BGE v1.5 expects the
  prefix `Represent this sentence for searching relevant passages: ` on
  queries only — the scripts and the UI handle this automatically.
* Model weights are loaded from the local Hugging Face cache without any
  network calls after the first download.
* Other category files work too, e.g. `--category Appliances` or
  `Clothing_Shoes_and_Jewelry`; `main_category` then tells them apart.
