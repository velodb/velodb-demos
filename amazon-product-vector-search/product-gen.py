#!/usr/bin/env python3
"""Build a product catalog from the Amazon Reviews 2023 product metadata
(McAuley Lab, UCSD -- https://amazon-reviews-2023.github.io/), embed each
product with BGE (bge-small-en-v1.5), and store everything -- metadata plus
vector -- in a local SQLite database.

The source is one JSONL file per Amazon category (Home_and_Kitchen has ~3.7M
products, 11.8 GB). It is streamed straight from Hugging Face and the stream
stops as soon as --count usable products have been collected, so nothing
large is written to disk.

Output:
  products.db   - SQLite file with a `products` table (see SCHEMA below) and a
                  `meta` table (model name, query prefix, vector dim).

Usage:
  python product-gen.py [--count 100000] [--category Home_and_Kitchen]
                        [--device auto|mps|cpu] [--batch-size 128]

Embedding runs on the Apple GPU (MPS, fp16) when available -- ~4x faster than
CPU on an M3 Max with identical vectors -- and falls back to CPU otherwise.

Idempotent and resumable:
  * Collection resumes from the last committed byte offset of the stream if
    interrupted. Once the selection is complete it is reused; delete the file
    to re-select with a different --count/--category.
  * Embedding skips rows that already have a vector, so an interrupted run
    just continues where it left off.

Then:  ./run-export.sh  -> export/*.jsonl      ./run-load.sh -> Doris
"""

import argparse
import json
import re
import sqlite3
import sys
import time
import urllib.request
from pathlib import Path

SOURCE_BASE = ("https://huggingface.co/datasets/McAuley-Lab/Amazon-Reviews-2023"
               "/resolve/main/raw/meta_categories")
MODEL_NAME = "BAAI/bge-small-en-v1.5"
# bge-*-en-v1.5 models want this prepended to *queries* (not passages);
# stored in the db so the search scripts apply it automatically.
QUERY_PREFIX = "Represent this sentence for searching relevant passages: "
MIN_DESCRIPTION = 30
MAX_DESCRIPTION = 1500

# Column order here is the column order everywhere else (export, Doris DDL).
SCHEMA = """
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS products (
    product_id          TEXT PRIMARY KEY,   -- parent ASIN
    product_name        TEXT NOT NULL,
    product_description TEXT NOT NULL,      -- description + feature bullets
    main_category       TEXT,      -- Amazon top-level department, e.g. "Amazon Home"
    product_type        TEXT,      -- most specific category, e.g. "Mug Sets"
    brand               TEXT,
    color               TEXT,
    material            TEXT,
    style               TEXT,
    categories          TEXT,      -- JSON array: full category path
    browse_node         TEXT,      -- same path joined with " / "
    image_url           TEXT,      -- main product image
    price               REAL,      -- USD
    average_rating      REAL,      -- 1..5
    rating_number       INTEGER,   -- number of ratings
    weight_lb           REAL,      -- parsed from details."Item Weight"
    height_in           REAL,      -- parsed from details."Product Dimensions"
    length_in           REAL,
    width_in            REAL,
    embedding           BLOB       -- float32[dim] little-endian, L2-normalized; NULL until embedded
);
"""
COLUMNS = [
    "product_id", "product_name", "product_description", "main_category",
    "product_type", "brand", "color", "material", "style", "categories",
    "browse_node", "image_url", "price", "average_rating", "rating_number",
    "weight_lb", "height_in", "length_in", "width_in",
]

_ws = re.compile(r"\s+")
_num = r"(\d+(?:\.\d+)?)"
_weight_re = re.compile(_num + r"\s*(pounds?|lbs?|ounces?|oz|kilograms?|kg|grams?|g)\b", re.I)
_dims_re = re.compile(_num + r"\s*[\"']?[lLwWhHdD]?\s*x\s*" + _num + r"\s*[\"']?[lLwWhHdD]?\s*x\s*"
                      + _num + r"\s*[\"']?[lLwWhHdD]?\s*(inches|inch|in|cm|centimeters?|mm|millimeters?|feet|ft)?\b", re.I)
_WEIGHT_TO_LB = {"pound": 1, "lb": 1, "ounce": 1 / 16, "oz": 1 / 16,
                 "kilogram": 2.20462, "kg": 2.20462, "gram": 0.00220462, "g": 0.00220462}
_LEN_TO_IN = {"inch": 1, "in": 1, "cm": 0.393701, "centimeter": 0.393701,
              "mm": 0.0393701, "millimeter": 0.0393701, "feet": 12, "ft": 12}


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def clean(text, limit=MAX_DESCRIPTION):
    text = _ws.sub(" ", str(text)).strip()
    return text[:limit] or None


def parse_weight_lb(text):
    m = _weight_re.search(text or "")
    if not m:
        return None
    unit = m.group(2).lower().rstrip("s")
    unit = "lb" if unit == "lb" else unit
    factor = _WEIGHT_TO_LB.get(unit)
    return round(float(m.group(1)) * factor, 3) if factor else None


def parse_dims_in(text):
    """'10 x 5 x 3 inches' / '10"L x 5"W x 3"H' -> (10, 5, 3) in inches.
    Amazon lists dimensions as L x W x H; the table stores height/length/width."""
    m = _dims_re.search(text or "")
    if not m:
        return None, None, None
    unit = (m.group(4) or "inch").lower().rstrip("s")
    unit = {"inche": "inch", "fee": "feet"}.get(unit, unit)
    factor = _LEN_TO_IN.get(unit)
    if not factor:
        return None, None, None
    length, width, height = (round(float(m.group(i)) * factor, 3) for i in (1, 2, 3))
    return height, length, width


def to_float(v):
    try:
        f = float(v)
        return f if f == f else None  # drop NaN
    except (TypeError, ValueError):
        return None


def extract_product(rec):
    """Return a dict with the COLUMNS keys, or None if the record is unusable."""
    title = clean(rec.get("title") or "", 300)
    if not title:
        return None
    parts = [p for p in (rec.get("description") or []) if isinstance(p, str)]
    parts += [p for p in (rec.get("features") or []) if isinstance(p, str)]
    description = clean(". ".join(p.strip().rstrip(". ") for p in parts if p.strip()))
    if not description or len(description) < MIN_DESCRIPTION:
        return None

    details = rec.get("details") or {}
    if not isinstance(details, dict):
        details = {}
    cats = [c for c in (rec.get("categories") or []) if isinstance(c, str) and c.strip()]
    images = rec.get("images") or []
    image = None
    for im in images:
        if isinstance(im, dict) and im.get("large"):
            image = im["large"]
            break
    height, length, width = parse_dims_in(
        details.get("Product Dimensions") or details.get("Item Dimensions LxWxH")
        or details.get("Package Dimensions"))

    return {
        "product_id": rec.get("parent_asin"),
        "product_name": title,
        "product_description": description,
        "main_category": clean(rec.get("main_category") or "", 128),
        "product_type": clean(cats[-1], 128) if cats else None,
        "brand": clean(rec.get("store") or details.get("Brand") or "", 128),
        "color": clean(details.get("Color") or "", 128),
        "material": clean(details.get("Material") or "", 128),
        "style": clean(details.get("Style") or "", 128),
        "categories": json.dumps(cats) if cats else None,
        "browse_node": clean(" / ".join(cats), 512) if cats else None,
        "image_url": image,
        "price": to_float(rec.get("price")),
        "average_rating": to_float(rec.get("average_rating")),
        "rating_number": int(rec["rating_number"]) if isinstance(rec.get("rating_number"), (int, float)) else None,
        "weight_lb": parse_weight_lb(details.get("Item Weight")),
        "height_in": height, "length_in": length, "width_in": width,
    }


def open_db(db_path: Path) -> sqlite3.Connection:
    con = sqlite3.connect(str(db_path))
    con.executescript(SCHEMA)
    return con


def collect_products(con, category: str, count: int, retries: int = 20):
    """Stream the category's metadata JSONL from Hugging Face and insert the
    first `count` usable products, stopping as soon as it has enough.

    Resumable: the byte offset of the last committed batch is stored in meta,
    so a dropped connection (or a re-run) continues with an HTTP Range request
    instead of starting the multi-GB stream over."""
    url = f"{SOURCE_BASE}/meta_{category}.jsonl"
    placeholders = ", ".join("?" for _ in COLUMNS)
    insert = f"INSERT OR IGNORE INTO products ({', '.join(COLUMNS)}) VALUES ({placeholders})"
    meta = dict(con.execute("SELECT key, value FROM meta").fetchall())
    offset = int(meta.get("stream_offset", 0))
    read = int(meta.get("stream_records", 0))
    seen = {r[0] for r in con.execute("SELECT product_id FROM products")}
    kept = len(seen)
    con.executemany("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)",
                    [("source", url), ("category", category), ("target_count", str(count))])
    con.commit()
    log(f"streaming {url}" + (f" (resuming at byte {offset:,}, {kept} collected)" if offset else ""))

    def commit(batch, offset, read):
        if batch:
            con.executemany(insert, batch)
        con.executemany("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)",
                        [("stream_offset", str(offset)), ("stream_records", str(read))])
        con.commit()

    start = time.time()
    attempt = 0
    while kept < count:
        req = urllib.request.Request(url, headers={"User-Agent": "product-gen/1.0",
                                                   "Range": f"bytes={offset}-"})
        batch, batch_offset = [], offset
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                if offset and resp.status != 206:
                    raise RuntimeError("server ignored the Range request; cannot resume")
                for raw in resp:
                    batch_offset += len(raw)
                    read += 1
                    if not raw.endswith(b"\n"):
                        break  # truncated tail line: re-read it on the next attempt
                    try:
                        rec = json.loads(raw)
                    except ValueError:
                        continue
                    prod = extract_product(rec)
                    if prod is None or not prod["product_id"] or prod["product_id"] in seen:
                        continue
                    seen.add(prod["product_id"])
                    batch.append(tuple(prod[c] for c in COLUMNS))
                    kept += 1
                    if len(batch) >= 1000:
                        commit(batch, batch_offset, read)
                        offset, batch = batch_offset, []
                        attempt = 0
                    if kept % 10000 == 0:
                        log(f"collected {kept:,}/{count:,} products from {read:,} records, "
                            f"{offset / 1e9:.2f} GB read ({time.time() - start:.0f}s)")
                    if kept >= count:
                        break
                else:
                    if kept < count:
                        commit(batch, batch_offset, read)
                        log(f"reached end of file with {kept} products (< {count})")
                        return
            commit(batch, batch_offset, read)
            offset = batch_offset
        except (OSError, RuntimeError, ValueError) as e:
            attempt += 1
            if attempt > retries:
                raise
            wait = min(60, 5 * attempt)
            log(f"stream error ({e}); resuming from byte {offset:,} in {wait}s "
                f"(attempt {attempt}/{retries})")
            time.sleep(wait)
    log(f"stored {kept:,} products (scanned {read:,} records, {offset / 1e9:.2f} GB, "
        f"{time.time() - start:.0f}s)")


def load_model(name: str):
    """Load a sentence-transformers model, preferring the local HF cache.

    A plain SentenceTransformer(name) makes ~15 HEAD requests to
    huggingface.co on every start to check whether the 'main' revision moved,
    even when the model is fully cached. Trying local_files_only first skips
    that entirely (0.2s, no network); we only go online if the cache is empty.
    """
    from sentence_transformers import SentenceTransformer
    try:
        return SentenceTransformer(name, local_files_only=True)
    except Exception:
        log(f"{name} not in local cache, downloading from Hugging Face ...")
        return SentenceTransformer(name)


def pick_device(requested: str):
    import torch
    if requested == "auto":
        requested = "mps" if torch.backends.mps.is_available() else "cpu"
    if requested == "mps" and not torch.backends.mps.is_available():
        log("MPS not available, falling back to CPU")
        requested = "cpu"
    return requested


def embed_products(con, batch_size: int, threads: int, device: str):
    total = con.execute("SELECT count(*) FROM products").fetchone()[0]
    todo = con.execute(
        "SELECT product_id, product_name, product_description FROM products "
        "WHERE embedding IS NULL ORDER BY rowid").fetchall()
    if not todo:
        log(f"all {total} products already embedded")
        return
    log(f"{total - len(todo)} products already embedded, {len(todo)} to go")

    import numpy as np
    import torch

    device = pick_device(device)
    if device == "cpu" and threads:
        torch.set_num_threads(threads)

    log(f"loading {MODEL_NAME} ...")
    model = load_model(MODEL_NAME)
    model.max_seq_length = 512  # plenty for product text
    if device == "mps":
        model.to("mps")
        model.half()  # fp16 on the Apple GPU: ~4x CPU throughput, same vectors (cos 1.0000)
        log("model loaded on Apple GPU (MPS, fp16)")
    else:
        log(f"model loaded, embedding on CPU with {torch.get_num_threads()} threads ...")

    con.executemany("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)",
                    [("model", MODEL_NAME), ("query_prefix", QUERY_PREFIX)])
    # encode() sorts each call's texts by length to minimise padding, so hand it
    # large chunks and let it split them into GPU batches of batch_size.
    chunk = batch_size * 32
    start = time.time()
    for i in range(0, len(todo), chunk):
        rows = todo[i:i + chunk]
        texts = [f"{name}. {desc}" for _, name, desc in rows]
        vecs = model.encode(texts, batch_size=batch_size, show_progress_bar=False,
                            convert_to_numpy=True).astype(np.float32)
        vecs /= np.linalg.norm(vecs, axis=1, keepdims=True)  # normalize in fp32
        con.executemany(
            "UPDATE products SET embedding = ? WHERE product_id = ?",
            [(vec.tobytes(), pid) for (pid, _, _), vec in zip(rows, vecs)])
        con.execute("INSERT OR REPLACE INTO meta (key, value) VALUES ('dim', ?)",
                    (str(vecs.shape[1]),))
        con.commit()  # one transaction per chunk: a crash keeps completed chunks
        n = i + len(rows)
        elapsed = time.time() - start
        rate = n / elapsed
        eta = (len(todo) - n) / rate if rate else 0
        log(f"embedded {n:,}/{len(todo):,} "
            f"({rate:.0f} products/s, ETA {eta / 60:.1f} min)")
    log(f"done: {len(todo):,} vectors written in {(time.time() - start) / 60:.1f} min")


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--count", type=int, default=100000,
                    help="products to collect (first N usable records of the file)")
    ap.add_argument("--category", default="Home_and_Kitchen",
                    help="Amazon Reviews 2023 category file, e.g. Home_and_Kitchen, "
                         "Tools_and_Home_Improvement, Grocery_and_Gourmet_Food")
    ap.add_argument("--batch-size", type=int, default=128)
    ap.add_argument("--threads", type=int, default=0,
                    help="CPU threads for the model when on CPU (0 = all cores)")
    ap.add_argument("--device", default="auto", choices=["auto", "mps", "cpu"],
                    help="auto = Apple GPU (MPS) when available, else CPU")
    ap.add_argument("--db", default="products.db")
    args = ap.parse_args()

    con = open_db(Path(args.db))
    meta = dict(con.execute("SELECT key, value FROM meta").fetchall())
    existing = con.execute("SELECT count(*) FROM products").fetchone()[0]
    target = int(meta.get("target_count", 0))
    if existing and (existing >= target or "stream_offset" not in meta):
        log(f"step 1/2: {args.db} already holds {existing:,} products, reusing "
            f"(delete the file to re-select)")
    else:
        log(f"step 1/2: collecting {args.count:,} products from {args.category}")
        collect_products(con, args.category, args.count)

    log(f"step 2/2: embedding descriptions with {MODEL_NAME}")
    embed_products(con, args.batch_size, args.threads, args.device)
    con.close()
    log("all done")


if __name__ == "__main__":
    sys.exit(main())
