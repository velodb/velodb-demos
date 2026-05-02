#!/usr/bin/env python3
"""
Static RAG data loader — streams the bundled 15-doc dataset into Doris.

Reads dist/rag/data/chunks.json (pre-computed BGE-M3 embeddings, 82 chunks
across 15 VeloDB docs) and ingests it via Doris Stream Load.

No live embedding model needed — the embeddings are baked in.
"""
from __future__ import annotations

import base64
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


def env(name: str, default: str | None = None) -> str:
    v = os.environ.get(name, default)
    if v is None:
        sys.exit(f"[rag-load] missing env: {name}")
    return v


FE_HOST = env("DORIS_HOST", "doris-fe")
FE_HTTP_PORT = env("DORIS_HTTP_PORT", "8030")
DB = env("DORIS_DB", "rag_db")
TABLE = env("DORIS_TABLE", "rag_unified")
USER = env("DORIS_USER", "root")
PASSWORD = env("DORIS_PASSWORD", "")
DATA_PATH = env("DATA_PATH", "/data/chunks.json")
# Match the frontend RAGDemo page defaults so the demo "just works" out of
# the box. The TenantSelector dropdown ships these IDs preselected.
TENANT = env("RAG_TENANT_ID", "VeloDB Sample")
CORPUS = env("RAG_CORPUS_ID", "velodb_docs")


def basic_auth_header() -> str:
    creds = f"{USER}:{PASSWORD}".encode()
    return "Basic " + base64.b64encode(creds).decode()


def wait_for_doris(timeout_s: int = 240) -> None:
    """Block until the FE accepts requests."""
    url = f"http://{FE_HOST}:{FE_HTTP_PORT}/api/health"
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=5) as r:
                if r.status < 500:
                    print(f"[rag-load] FE reachable at {FE_HOST}:{FE_HTTP_PORT}")
                    return
        except (urllib.error.URLError, ConnectionError):
            pass
        time.sleep(2)
    sys.exit(f"[rag-load] FE not reachable after {timeout_s}s")


def transform(chunk: dict) -> dict:
    """The chunks.json shipped in this distro is already a literal dump of
    rag_unified rows from a production VeloDB cluster. We only:
      - override tenant_id/corpus_id from env (so the demo is configurable),
      - JSON-serialize the JSON columns (entities, relationships,
        multimodal_data) since Doris Stream Load wants string literals there.
    """
    def as_json_string(v):
        if v is None:
            return "[]" if isinstance(v, list) else "{}"
        if isinstance(v, str):
            return v
        return json.dumps(v)

    return {
        "tenant_id": TENANT,
        "corpus_id": CORPUS,
        "row_id": chunk["row_id"],
        "content_type": chunk.get("content_type", "text"),
        "content": chunk.get("content", "") or "",
        "content_embedding": chunk.get("content_embedding") or [],
        "entity_names": chunk.get("entity_names") or [],
        "entity_types": chunk.get("entity_types") or [],
        "entities": as_json_string(chunk.get("entities") or []),
        "relationships": as_json_string(chunk.get("relationships") or []),
        "multimodal_data": as_json_string(chunk.get("multimodal_data") or {}),
        "neighbor_ids": chunk.get("neighbor_ids") or [],
        "related_entity_names": chunk.get("related_entity_names") or [],
        "doc_id": chunk.get("doc_id") or "",
        "chunk_index": int(chunk.get("chunk_index") or 0),
        "page_number": int(chunk.get("page_number") or 0),
    }


def _put(url: str, body: bytes, label: str, follow: bool) -> tuple[int, dict[str, str], bytes]:
    """One PUT, no auto-follow. Returns (status, headers, body)."""
    req = urllib.request.Request(
        url,
        data=body,
        method="PUT",
        headers={
            "Authorization": basic_auth_header(),
            "Content-Type": "application/json",
            "Expect": "100-continue",
            "format": "json",
            "strip_outer_array": "true",
            "label": label,
        },
    )

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *_a, **_kw):  # pragma: no cover
            return None

    opener = urllib.request.build_opener(NoRedirect)
    try:
        resp = opener.open(req, timeout=120)
        return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers or {}), e.read()


def stream_load(rows: list[dict]) -> dict:
    """Doris Stream Load. The FE replies 307 → BE; we re-PUT to the BE
    explicitly so user-info embedded in the redirect URL doesn't confuse
    urllib's resolver."""
    body = json.dumps(rows).encode()
    label = f"rag_load_{int(time.time() * 1000)}"

    fe_url = f"http://{FE_HOST}:{FE_HTTP_PORT}/api/{DB}/{TABLE}/_stream_load"
    status, headers, payload = _put(fe_url, body, label, follow=False)

    if status in (301, 302, 307, 308):
        location = headers.get("Location") or headers.get("location")
        if not location:
            raise RuntimeError(f"FE returned {status} but no Location header")
        # Strip embedded user:pass — we already provide an Authorization header.
        parsed = urllib.parse.urlsplit(location)
        if parsed.username or parsed.password:
            netloc = parsed.hostname + (f":{parsed.port}" if parsed.port else "")
            location = urllib.parse.urlunsplit(parsed._replace(netloc=netloc))
        status, headers, payload = _put(location, body, label, follow=False)

    if status >= 400:
        raise RuntimeError(f"stream load failed: HTTP {status}: {payload!r}")

    return json.loads(payload)


def main() -> int:
    path = Path(DATA_PATH)
    if not path.exists():
        sys.exit(f"[rag-load] dataset not found: {path}")

    print(f"[rag-load] loading {path}")
    with path.open() as f:
        chunks = json.load(f)
    print(f"[rag-load] {len(chunks)} chunks across "
          f"{len({c['doc_id'] for c in chunks})} docs")

    wait_for_doris()

    rows = [transform(c) for c in chunks]
    # Stream Load has BE-side request size limits. With 1024-d embeddings
    # baked into each row, a 411-row payload is ~6 MB which exceeds the
    # default. Batch in groups so each PUT is well under the limit.
    BATCH = int(os.environ.get("LOAD_BATCH_SIZE", "20"))

    total_loaded = 0
    total_filtered = 0
    total_bytes = 0
    for i in range(0, len(rows), BATCH):
        batch = rows[i : i + BATCH]
        result = stream_load(batch)
        status = result.get("Status")
        if status not in {"Success", "Publish Timeout"}:
            print(f"[rag-load] FAILED batch {i // BATCH + 1}: {json.dumps(result, indent=2)}", file=sys.stderr)
            return 1
        total_loaded += int(result.get("NumberLoadedRows") or 0)
        total_filtered += int(result.get("NumberFilteredRows") or 0)
        total_bytes += int(result.get("LoadBytes") or 0)
        print(
            f"[rag-load] batch {i // BATCH + 1}/{(len(rows) + BATCH - 1) // BATCH}: "
            f"{result.get('NumberLoadedRows')} rows loaded, "
            f"{result.get('NumberFilteredRows')} filtered"
        )

    print(f"[rag-load] total loaded   : {total_loaded}")
    print(f"[rag-load] total filtered : {total_filtered}")
    print(f"[rag-load] total bytes    : {total_bytes}")
    print("[rag-load] done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
