#!/usr/bin/env python3
"""Export products.db to newline-delimited JSON for Doris stream load.

Each line is one product with every catalog column plus `embedding` as a JSON
array of floats (matching ARRAY<FLOAT> in create_doris_table.sql). Output is
split into numbered chunk files so each stream-load request stays small.

Usage:
  python product-export.py [--db products.db] [--out export] [--chunk 5000]
"""

import argparse
import json
import shutil
import sqlite3
import sys
import time
from pathlib import Path

import numpy as np

# Keep in sync with product-gen.py COLUMNS and create_doris_table.sql
COLUMNS = [
    "product_id", "product_name", "product_description", "main_category",
    "product_type", "brand", "color", "material", "style", "categories",
    "browse_node", "image_url", "price", "average_rating", "rating_number",
    "weight_lb", "height_in", "length_in", "width_in",
]


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", file=sys.stderr, flush=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", default="products.db")
    ap.add_argument("--out", default="export", help="output directory (recreated)")
    ap.add_argument("--chunk", type=int, default=20000, help="rows per file")
    ap.add_argument("--precision", type=int, default=6,
                    help="decimal places for vector components")
    args = ap.parse_args()

    db_path = Path(args.db)
    if not db_path.exists():
        raise SystemExit(f"{db_path} not found -- run ./run-embedding.sh first")
    con = sqlite3.connect(str(db_path))
    meta = dict(con.execute("SELECT key, value FROM meta").fetchall())
    total, embedded = con.execute(
        "SELECT count(*), count(embedding) FROM products").fetchone()
    if not embedded:
        raise SystemExit("no embeddings in the database yet -- run ./run-embedding.sh")
    if embedded < total:
        log(f"warning: {total - embedded} of {total} products have no vector yet "
            f"and are skipped (run ./run-embedding.sh to finish them)")

    out = Path(args.out)
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)

    cur = con.execute(
        f"SELECT {', '.join(COLUMNS)}, embedding FROM products "
        f"WHERE embedding IS NOT NULL ORDER BY rowid")
    n, part, f = 0, 0, None
    for row in cur:
        if n % args.chunk == 0:
            if f:
                f.close()
            part += 1
            f = open(out / f"products-{part:04d}.jsonl", "w", encoding="utf-8")
        rec = dict(zip(COLUMNS, row[:-1]))
        rec["categories"] = json.loads(rec["categories"]) if rec["categories"] else None
        vec = np.frombuffer(row[-1], dtype=np.float32)
        rec["embedding"] = [round(float(x), args.precision) for x in vec]
        f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        n += 1
    if f:
        f.close()
    con.close()

    (out / "manifest.json").write_text(json.dumps({
        "rows": n, "files": part, "chunk": args.chunk,
        "model": meta.get("model"), "dim": int(meta.get("dim", 0)),
        "query_prefix": meta.get("query_prefix"),
        "columns": COLUMNS + ["embedding"],
    }, indent=2))
    size = sum(p.stat().st_size for p in out.glob("*.jsonl"))
    log(f"exported {n} products to {part} files in {out}/ ({size / 1e6:.0f} MB)")


if __name__ == "__main__":
    main()
