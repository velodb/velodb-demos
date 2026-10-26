#!/usr/bin/env python3
"""Semantic product search over the vectors produced by product-gen.py.

Embeds the query with the same BGE model and ranks all products by cosine
similarity (vectors are stored normalized, so a dot product is the cosine
score). This is a brute-force scan over every vector -- the local baseline
that the Doris HNSW + WHERE-filter version (see sample_query.sql) is
compared against.

Usage:
  python product-search.py                     # interactive prompt
  python product-search.py "coffee maker"      # one-shot query
  python product-search.py -k 10 "soft rug for living room"
"""

import argparse
import sqlite3
import sys
import time
from pathlib import Path

import numpy as np


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", file=sys.stderr, flush=True)


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


def load_products(db_path: Path):
    if not db_path.exists():
        raise SystemExit(f"{db_path} not found -- run ./run-embedding.sh first")
    con = sqlite3.connect(str(db_path))
    meta = dict(con.execute("SELECT key, value FROM meta").fetchall())
    if "model" not in meta or "dim" not in meta:
        raise SystemExit(f"{db_path} has no embeddings yet -- run ./run-embedding.sh")
    rows = con.execute(
        "SELECT product_id, product_name, product_description, product_type, "
        "brand, price, average_rating, embedding FROM products WHERE embedding IS NOT NULL "
        "ORDER BY rowid").fetchall()
    con.close()
    products = [r[:-1] for r in rows]
    matrix = np.vstack([np.frombuffer(r[-1], dtype=np.float32) for r in rows])
    assert matrix.shape[1] == int(meta["dim"])
    return meta["model"], meta.get("query_prefix", ""), products, matrix


def search(model, query_prefix, matrix, products, query, top_k):
    t0 = time.time()
    qvec = model.encode([query_prefix + query], normalize_embeddings=True)[0]
    t1 = time.time()
    scores = matrix @ qvec                      # cosine against every vector
    top = np.argsort(scores)[::-1][:top_k]      # rank all N, keep top-k
    t2 = time.time()
    print(f'\ntop {len(top)} matches for "{query}" '
          f'(brute force over {len(products)} vectors; '
          f'embed {(t1 - t0) * 1000:.0f} ms, scan+rank {(t2 - t1) * 1000:.1f} ms):\n')
    for rank, idx in enumerate(top, 1):
        pid, name, desc, ptype, brand, price, rating = products[idx]
        tags = " | ".join(str(t) for t in (ptype, brand) if t)
        if price is not None:
            tags += f" | ${price:g}"
        if rating is not None:
            tags += f" | {rating}★"
        print(f"{rank:3d}. [{scores[idx]:.4f}] {pid}  {name}")
        print(f"     ({tags})")
        print(f"     {desc[:200]}{'...' if len(desc) > 200 else ''}")


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("query", nargs="*", help="search text (omit for interactive mode)")
    ap.add_argument("-k", "--top-k", type=int, default=20)
    ap.add_argument("--db", default="products.db")
    ap.add_argument("--print-vector", action="store_true",
                    help="print the query's embedding as a JSON array (for use "
                         "as the vector literal in a Doris SQL query) and exit")
    args = ap.parse_args()

    if args.print_vector:
        if not args.query:
            ap.error("--print-vector needs a query")
        con = sqlite3.connect(args.db)
        meta = dict(con.execute("SELECT key, value FROM meta").fetchall())
        con.close()
        model = load_model(meta["model"])
        vec = model.encode([meta.get("query_prefix", "") + " ".join(args.query)],
                           normalize_embeddings=True)[0]
        print("[" + ", ".join(f"{x:.6f}" for x in vec) + "]")
        return

    log("loading vectors ...")
    model_name, query_prefix, products, matrix = load_products(Path(args.db))
    log(f"{len(products)} products, dim={matrix.shape[1]}")

    log(f"loading {model_name} ...")
    model = load_model(model_name)
    model.max_seq_length = 512
    log("ready")

    if args.query:
        search(model, query_prefix, matrix, products, " ".join(args.query), args.top_k)
        return

    print("enter a keyword or description (empty line or Ctrl-D to quit)")
    while True:
        try:
            query = input("search> ").strip()
        except (EOFError, KeyboardInterrupt):
            break
        if not query:
            break
        search(model, query_prefix, matrix, products, query, args.top_k)


if __name__ == "__main__":
    main()
