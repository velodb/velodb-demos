#!/usr/bin/env python3
"""Vector / hybrid product search on Doris (VELODB_DATABASE.products, default hybrid_search_demo).

Embeds the query text with the same BGE model used at load time, then runs an
ANN top-k query that Doris serves from the HNSW index. An optional --where
clause turns it into a hybrid search (scalar filters + vector similarity).

Usage:
  python product-search-doris.py                          # interactive prompt
  python product-search-doris.py "coffee maker"           # one-shot query
  python product-search-doris.py -k 10 "soft rug" --where "price < 50 AND average_rating >= 4.5"
  python product-search-doris.py "espresso" --where "product_description MATCH_ANY 'stainless steel'"
  python product-search-doris.py "espresso" --exact       # brute-force inner_product, no index
  python product-search-doris.py "espresso" --show-sql

Interactive mode accepts the same query text; type  where <expr>  to set /
change the filter,  where  alone to clear it, and  sql  to toggle SQL echo.

Reads VELODB_HOST VELODB_MYSQL_PORT VELODB_USER VELODB_PASSWORD from the
environment (see doris-sql.py); the model name and query prefix come from
products.db's meta table so local and Doris search embed identically.
"""

import argparse
import os
import sqlite3
import sys
import time
from pathlib import Path

DB = os.environ.get("VELODB_DATABASE") or "hybrid_search_demo"
TABLE = "products"


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", file=sys.stderr, flush=True)


def load_model(name: str):
    """Load from the local HF cache first (no network); download only if absent."""
    from sentence_transformers import SentenceTransformer
    try:
        return SentenceTransformer(name, local_files_only=True)
    except Exception:
        log(f"{name} not in local cache, downloading from Hugging Face ...")
        return SentenceTransformer(name)


def read_meta(db_path: Path):
    if not db_path.exists():
        raise SystemExit(f"{db_path} not found -- run ./run-embedding.sh first "
                         f"(it records which model/prefix to embed queries with)")
    con = sqlite3.connect(str(db_path))
    meta = dict(con.execute("SELECT key, value FROM meta").fetchall())
    con.close()
    if "model" not in meta:
        raise SystemExit(f"{db_path} has no embedding metadata yet")
    return meta["model"], meta.get("query_prefix", "")


def build_sql(vec, where, top_k, exact):
    fn = "inner_product" if exact else "inner_product_approximate"
    literal = "[" + ", ".join(f"{x:.6f}" for x in vec) + "]"
    where_sql = f"\nWHERE {where}" if where else ""
    return (f"SELECT product_id, product_name, product_type, brand, color, "
            f"price, average_rating, rating_number,\n       {fn}(embedding, {literal}) AS score\n"
            f"FROM {TABLE}{where_sql}\nORDER BY score DESC\nLIMIT {top_k}")


def search(cur, model, prefix, query, where, top_k, exact, show_sql):
    t0 = time.time()
    vec = model.encode([prefix + query], normalize_embeddings=True)[0]
    t_embed = time.time() - t0
    sql = build_sql(vec, where, top_k, exact)
    if show_sql:
        # elide the vector so the SQL stays readable
        head, _, tail = sql.partition("[")
        print(f"\n{head}[... {len(vec)} floats ...{tail[tail.index(']'):]}\n", file=sys.stderr)
    t1 = time.time()
    try:
        cur.execute(sql)
    except Exception as e:
        print(f"query failed: {e}", file=sys.stderr)
        return
    rows = cur.fetchall()
    t_query = time.time() - t1

    mode = "exact scan" if exact else "HNSW"
    filt = f", filter: {where}" if where else ""
    print(f'\ntop {len(rows)} matches for "{query}" '
          f'({mode}{filt}; embed {t_embed * 1000:.0f} ms, doris {t_query * 1000:.0f} ms):\n')
    for rank, (pid, name, ptype, brand, color, price, rating, nratings, score) in enumerate(rows, 1):
        tags = " | ".join(str(t) for t in (ptype, brand, color) if t)
        if price is not None:
            tags += f" | ${price:g}"
        if rating is not None:
            tags += f" | {rating}★ ({nratings})"
        print(f"{rank:3d}. [{score:.4f}] {pid}  {name}")
        print(f"     ({tags})")


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("query", nargs="*", help="search text (omit for interactive mode)")
    ap.add_argument("-k", "--top-k", type=int, default=10)
    ap.add_argument("--where", default="", help="SQL predicate for hybrid search")
    ap.add_argument("--exact", action="store_true",
                    help="use inner_product (brute force) instead of the HNSW index")
    ap.add_argument("--show-sql", action="store_true", help="echo the SQL sent to Doris")
    ap.add_argument("--db", default="products.db", help="local db (for model metadata)")
    args = ap.parse_args()

    model_name, prefix = read_meta(Path(args.db))
    log(f"loading {model_name} ...")
    model = load_model(model_name)
    model.max_seq_length = 512

    sys.path.insert(0, str(Path(__file__).parent))
    from importlib import import_module
    doris_sql = import_module("doris-sql")
    log(f"connecting to Doris {DB}.{TABLE} ...")
    con = doris_sql.connect(DB)
    cur = con.cursor()
    cur.execute(f"SELECT count(*) FROM {TABLE}")
    log(f"{cur.fetchone()[0]} products in Doris, ready")

    where, show_sql = args.where, args.show_sql
    if args.query:
        search(cur, model, prefix, " ".join(args.query), where, args.top_k, args.exact, show_sql)
        return

    print("enter a keyword or description (empty line or Ctrl-D to quit)")
    print("  where <expr>  set hybrid filter      where   clear filter      sql   toggle SQL echo")
    while True:
        try:
            line = input(f"doris{' [' + where + ']' if where else ''}> ").strip()
        except (EOFError, KeyboardInterrupt):
            break
        if not line:
            break
        if line == "sql":
            show_sql = not show_sql
            print(f"SQL echo {'on' if show_sql else 'off'}")
            continue
        if line == "where" or line.startswith("where "):
            where = line[5:].strip()
            print(f"filter: {where or '(none)'}")
            continue
        search(cur, model, prefix, line, where, args.top_k, args.exact, show_sql)
    con.close()


if __name__ == "__main__":
    main()
