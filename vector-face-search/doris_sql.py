#!/usr/bin/env python3
# Run via ./ensure-venv.sh / venv/bin/python (see requirements.txt); the shell scripts do this for you.
"""Tiny mysql-client replacement for Doris (mysql 9 client can't auth against Doris).

  ./doris_sql.py -e "SHOW DATABASES"          # one statement, tabular output
  ./doris_sql.py -e "SHOW CREATE TABLE t" -G  # vertical output (like \\G)
  ./doris_sql.py sample_query.sql             # run every statement in a file
  ./doris_sql.py --env other.env -e "..."     # different connection file

Connection: VELODB_HOST, VELODB_MYSQL_PORT, VELODB_USER, VELODB_PASSWORD, VELODB_DATABASE from .env
(see env.example); variables set in the environment override the file. face-server.py and
face-search-doris.py import load_env() / connect() from here.
"""
import argparse
import os
import sys
import time

import pymysql


def load_env(path):
    env = {}
    if os.path.exists(path):
        for line in open(path):
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            v = v.split(" #", 1)[0].split("\t#", 1)[0].strip()
            env[k.strip().removeprefix("export ").strip()] = v.strip('"').strip("'")
    env.update({k: v for k, v in os.environ.items() if k.startswith("VELODB_")})
    for k in ("VELODB_HOST", "VELODB_USER"):
        if not env.get(k):
            sys.exit(f"{k} is not set: copy env.example to .env or export it")
    return env


def connect(env, database=None):
    return pymysql.connect(
        host=env["VELODB_HOST"],
        port=int(env.get("VELODB_MYSQL_PORT") or 9030),
        user=env["VELODB_USER"],
        password=env.get("VELODB_PASSWORD", ""),
        database=database,
        autocommit=True,
        connect_timeout=10,
        read_timeout=int(env.get("READ_TIMEOUT", 3600)),
        charset="utf8mb4",
    )


def split_statements(text):
    """Split on ';' outside quotes/comments (good enough for DDL/DML files)."""
    stmts, buf, quote, i = [], [], None, 0
    while i < len(text):
        c = text[i]
        if quote:
            buf.append(c)
            if c == "\\" and i + 1 < len(text):
                buf.append(text[i + 1]); i += 1
            elif c == quote:
                quote = None
        elif c in ("'", '"', "`"):
            quote = c; buf.append(c)
        elif text.startswith("--", i):
            j = text.find("\n", i); i = len(text) if j < 0 else j; buf.append("\n"); continue
        elif text.startswith("/*", i):
            j = text.find("*/", i + 2); i = len(text) if j < 0 else j + 2; continue
        elif c == ";":
            s = "".join(buf).strip()
            if s: stmts.append(s)
            buf = []
        else:
            buf.append(c)
        i += 1
    s = "".join(buf).strip()
    if s: stmts.append(s)
    return stmts


def print_rows(cur, vertical):
    if cur.description is None:
        print(f"Query OK, {cur.rowcount} rows affected")
        return
    cols = [d[0] for d in cur.description]
    rows = cur.fetchall()
    if vertical:
        for n, row in enumerate(rows, 1):
            print(f"*************************** {n}. row ***************************")
            w = max(len(c) for c in cols)
            for c, v in zip(cols, row):
                print(f"{c:>{w}}: {v}")
    else:
        print("\t".join(cols))
        for row in rows:
            print("\t".join("NULL" if v is None else str(v) for v in row))
    print(f"({len(rows)} rows)", file=sys.stderr)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("file", nargs="?", help="SQL file to execute")
    ap.add_argument("-e", "--execute", help="statement(s) to execute")
    ap.add_argument("-G", "--vertical", action="store_true", help="vertical output")
    ap.add_argument("--env", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"))
    ap.add_argument("-D", "--database", help="default database (default: VELODB_DATABASE)")
    ap.add_argument("-q", "--quiet", action="store_true", help="don't echo statements")
    a = ap.parse_args()

    env = load_env(a.env)
    text = a.execute if a.execute else (open(a.file).read() if a.file else sys.stdin.read())
    stmts = split_statements(text)
    if not stmts:
        sys.exit("no statements")

    try:
        conn = connect(env, a.database or env.get("VELODB_DATABASE"))
    except pymysql.err.OperationalError as e:
        if e.args[0] != 1049:  # unknown database: connect without one (CREATE DATABASE comes first)
            raise
        conn = connect(env)
    try:
        with conn.cursor() as cur:
            for s in stmts:
                if not a.quiet:
                    first = s.strip().splitlines()[0]
                    print(f"-- {first[:100]}{'...' if len(s) > 100 else ''}", file=sys.stderr)
                t0 = time.time()
                cur.execute(s)
                print_rows(cur, a.vertical)
                if not a.quiet:
                    print(f"-- {time.time() - t0:.2f}s", file=sys.stderr)
    finally:
        conn.close()


if __name__ == "__main__":
    try:
        main()
    except pymysql.MySQLError as e:
        sys.exit(f"doris error: {e}")
