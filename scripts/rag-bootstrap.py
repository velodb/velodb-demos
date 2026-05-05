#!/usr/bin/env python3
"""
RAG bootstrap (pure Python — uses mysql-connector-python already in the
rag-api image, so no extra system packages needed).

  1. waits for the Doris FE
  2. applies every *.sql file under $INIT_DIR (idempotent)
  3. if rag_unified is empty, stream-loads $DATA_PATH into it
"""
from __future__ import annotations

import logging
import os
import re
import sys
import time
from pathlib import Path

import mysql.connector

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
log = logging.getLogger("rag-bootstrap")


FE_HOST = os.environ.get("DORIS_HOST", "doris-fe")
FE_QUERY_PORT = int(os.environ.get("DORIS_PORT", "9030"))
FE_HTTP_PORT = os.environ.get("DORIS_HTTP_PORT", "8030")
USER = os.environ.get("DORIS_USER", "root")
PASSWORD = os.environ.get("DORIS_PASSWORD", "")

INIT_DIR = Path(os.environ.get("INIT_DIR", "/rag/init"))
DATA_PATH = os.environ.get("DATA_PATH", "/rag/data/chunks.json")
LOAD_SCRIPT = os.environ.get("LOAD_SCRIPT", "/scripts/rag-load.py")


def connect(database: str | None = None) -> mysql.connector.MySQLConnection:
    return mysql.connector.connect(
        host=FE_HOST,
        port=FE_QUERY_PORT,
        user=USER,
        password=PASSWORD,
        database=database,
        autocommit=True,
        connection_timeout=10,
    )


def wait_for_fe(timeout_s: int = 240) -> None:
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            with connect() as cn, cn.cursor() as cur:
                cur.execute("SELECT 1")
                cur.fetchone()
            log.info("FE ready at %s:%s", FE_HOST, FE_QUERY_PORT)
            return
        except Exception as exc:  # noqa: BLE001
            log.debug("FE not ready: %s", exc)
            time.sleep(2)
    sys.exit(f"FE not reachable after {timeout_s}s")


def split_statements(sql: str) -> list[str]:
    # Strip comments and split by ';' at end-of-line. Doris doesn't support
    # multi-statement send through the python connector, so we split.
    no_comments = re.sub(r"^\s*--.*$", "", sql, flags=re.MULTILINE)
    return [s.strip() for s in no_comments.split(";") if s.strip()]


def apply_sql_files() -> None:
    files = sorted(INIT_DIR.glob("*.sql"))
    if not files:
        log.warning("no .sql files in %s", INIT_DIR)
        return
    with connect() as cn, cn.cursor() as cur:
        for f in files:
            log.info("applying %s", f)
            for stmt in split_statements(f.read_text()):
                try:
                    cur.execute(stmt)
                except mysql.connector.Error as e:
                    # IF NOT EXISTS handles most cases, but DROP TABLE on a
                    # missing table can raise — keep going for idempotence.
                    log.warning("stmt failed (%s): %s", e, stmt[:80])


def already_loaded() -> bool:
    try:
        with connect("rag_db") as cn, cn.cursor() as cur:
            cur.execute("SELECT COUNT(*) FROM rag_unified")
            (count,) = cur.fetchone()
            return count > 0
    except mysql.connector.Error:
        return False


def run_loader() -> None:
    log.info("running %s", LOAD_SCRIPT)
    rc = os.spawnvpe(
        os.P_WAIT,
        sys.executable,
        [sys.executable, LOAD_SCRIPT],
        os.environ,
    )
    if rc != 0:
        sys.exit(f"loader failed with exit code {rc}")


def main() -> int:
    wait_for_fe()
    apply_sql_files()
    if already_loaded():
        log.info("rag_unified already populated — skipping load")
        return 0
    run_loader()
    return 0


if __name__ == "__main__":
    sys.exit(main())
