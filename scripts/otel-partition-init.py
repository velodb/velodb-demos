#!/usr/bin/env python3
"""
Wait for the OTEL collector to create otel_logs etc., then backfill 7 days
of historical UTC date partitions. This is required when OTEL_TIMEZONE
is set to anything other than UTC — see docs/TIMEZONE.md.

Idempotent: runs once on each compose-up, safe to re-run.
"""
from __future__ import annotations

import datetime as dt
import logging
import os
import sys
import time

import mysql.connector

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
log = logging.getLogger("otel-partition-init")

FE_HOST = os.environ.get("DORIS_HOST", "doris-fe")
FE_PORT = int(os.environ.get("DORIS_PORT", "9030"))
USER = os.environ.get("DORIS_USER", "root")
PASSWORD = os.environ.get("DORIS_PASSWORD", "")
DB = "demo_observability"
DAYS_BACK = int(os.environ.get("DAYS_BACK", "7"))
WAIT_TIMEOUT = int(os.environ.get("WAIT_TIMEOUT_S", "300"))

TABLES = [
    "otel_logs",
    "otel_traces",
    "otel_metrics_sum",
    "otel_metrics_gauge",
    "otel_metrics_histogram",
    "otel_metrics_summary",
    "otel_metrics_exponential_histogram",
    "otel_traces_graph",
]


def conn():
    return mysql.connector.connect(
        host=FE_HOST, port=FE_PORT, user=USER, password=PASSWORD,
        autocommit=True, connection_timeout=10,
    )


def wait_for_tables() -> None:
    deadline = time.time() + WAIT_TIMEOUT
    while time.time() < deadline:
        try:
            with conn() as c, c.cursor() as cur:
                cur.execute(
                    "SELECT table_name FROM information_schema.tables "
                    "WHERE table_schema = %s",
                    (DB,),
                )
                got = {r[0] for r in cur.fetchall()}
            missing = [t for t in TABLES if t not in got]
            if not missing:
                log.info("all %d OTEL tables exist", len(TABLES))
                return
            log.info("waiting for %d/%d tables (missing: %s)",
                     len(TABLES) - len(missing), len(TABLES), missing[:3])
        except Exception as e:  # noqa: BLE001
            log.debug("connect failed: %s", e)
        time.sleep(5)
    sys.exit(f"timeout waiting for OTEL tables in {DB}")


def backfill_partitions() -> None:
    today_utc = dt.datetime.now(dt.timezone.utc).date()
    dates = [today_utc - dt.timedelta(days=i) for i in range(1, DAYS_BACK + 1)]

    with conn() as c, c.cursor() as cur:
        cur.execute(f"USE {DB}")
        for tbl in TABLES:
            log.info("backfilling %s", tbl)
            try:
                cur.execute(f"ALTER TABLE {tbl} SET ('dynamic_partition.enable' = 'false')")
            except mysql.connector.Error as e:
                log.warning("disable dynamic_partition on %s: %s", tbl, e)

            for d in dates:
                next_d = d + dt.timedelta(days=1)
                p = f"p{d:%Y%m%d}"
                sql = (
                    f"ALTER TABLE {tbl} ADD PARTITION {p} "
                    f"VALUES [('{d:%Y-%m-%d} 00:00:00'), ('{next_d:%Y-%m-%d} 00:00:00'))"
                )
                try:
                    cur.execute(sql)
                except mysql.connector.Error as e:
                    msg = str(e)
                    if "already exists" in msg or "Duplicate" in msg:
                        continue
                    log.warning("add partition %s on %s: %s", p, tbl, msg.split("\n")[0][:120])

            try:
                cur.execute(
                    f"ALTER TABLE {tbl} SET ("
                    f"'dynamic_partition.enable' = 'true',"
                    f"'dynamic_partition.history_partition_num' = '{DAYS_BACK}')"
                )
            except mysql.connector.Error as e:
                log.warning("re-enable dynamic_partition on %s: %s", tbl, e)


def main() -> int:
    log.info("waiting for OTEL tables in %s ...", DB)
    wait_for_tables()
    log.info("backfilling %d days of historical partitions", DAYS_BACK)
    backfill_partitions()
    log.info("done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
