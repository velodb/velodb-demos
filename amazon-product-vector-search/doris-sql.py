#!/usr/bin/env python3
"""Run SQL against Doris / VeloDB using the VELODB_* environment variables.

The Homebrew `mysql` 9.x client cannot authenticate to Doris (it no longer
ships the mysql_native_password plugin), so this tiny pymysql wrapper is used
by run-load.sh instead.

Usage:
  python doris-sql.py file.sql            # run every statement in the file
  python doris-sql.py -e "SELECT 1"       # run one statement
  python doris-sql.py -e "..." --db hybrid_search_demo
Reads:  VELODB_HOST  VELODB_MYSQL_PORT  VELODB_USER  VELODB_PASSWORD
"""

import argparse
import os
import sys

import pymysql


def connect(db=None):
    missing = [k for k in ("VELODB_HOST", "VELODB_USER") if not os.environ.get(k)]
    if missing:
        raise SystemExit(f"missing environment variables: {', '.join(missing)}")
    return pymysql.connect(
        host=os.environ["VELODB_HOST"], port=int(os.environ.get("VELODB_MYSQL_PORT") or 9030),
        user=os.environ["VELODB_USER"], password=os.environ.get("VELODB_PASSWORD", ""),
        database=db, connect_timeout=15, read_timeout=600, autocommit=True)


def split_statements(sql: str):
    """Split on ';' outside quotes/comments -- enough for DDL and demo queries."""
    stmts, buf, quote = [], [], None
    i = 0
    while i < len(sql):
        ch = sql[i]
        if quote:
            buf.append(ch)
            if ch == quote:
                quote = None
        elif ch in ("'", '"', "`"):
            quote = ch
            buf.append(ch)
        elif sql.startswith("--", i):
            j = sql.find("\n", i)
            i = len(sql) if j < 0 else j
            continue
        elif ch == ";":
            stmts.append("".join(buf).strip())
            buf = []
        else:
            buf.append(ch)
        i += 1
    tail = "".join(buf).strip()
    if tail:
        stmts.append(tail)
    return [s for s in stmts if s]


def run(cur, stmt, quiet=False):
    cur.execute(stmt)
    if cur.description:
        cols = [d[0] for d in cur.description]
        rows = cur.fetchall()
        if not quiet:
            print("\t".join(cols))
            for r in rows:
                print("\t".join("NULL" if v is None else str(v) for v in r))
        return rows
    return None


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("file", nargs="?", help="SQL file to execute")
    ap.add_argument("-e", "--execute", help="SQL statement(s) to execute")
    ap.add_argument("--db", help="default database")
    args = ap.parse_args()
    if not args.file and not args.execute:
        ap.error("give a SQL file or -e 'statement'")

    sql = args.execute or open(args.file, encoding="utf-8").read()
    con = connect(args.db)
    cur = con.cursor()
    for stmt in split_statements(sql):
        first_line = stmt.splitlines()[0][:100]
        print(f"-- {first_line}", file=sys.stderr)
        run(cur, stmt)
    con.close()


if __name__ == "__main__":
    main()
