#!/usr/bin/env python3
"""Tiny HTTP service that turns query text into a BGE vector for the Go UI.

The model and query prefix are read from products.db's meta table so the UI
embeds queries exactly like product-gen.py embedded the products. Runs on the
Apple GPU (MPS) when available.

  POST /embed   {"text": "coffee maker"}  ->  {"vector": [...384 floats...], "ms": 12}
  GET  /health                            ->  {"model": ..., "dim": 384}

Usage:  python embed-server.py [--port 7234] [--db products.db]
"""

import argparse
import json
import sqlite3
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", file=sys.stderr, flush=True)


def load_model(name, device):
    from sentence_transformers import SentenceTransformer
    try:
        m = SentenceTransformer(name, device=device, local_files_only=True)
    except Exception:
        log(f"{name} not in local cache, downloading ...")
        m = SentenceTransformer(name, device=device)
    m.max_seq_length = 512
    return m


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--port", type=int, default=7234)
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--db", default="products.db")
    args = ap.parse_args()

    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    meta = dict(con.execute("SELECT key, value FROM meta").fetchall())
    con.close()
    model_name, prefix = meta["model"], meta.get("query_prefix", "")

    import torch
    device = "mps" if torch.backends.mps.is_available() else "cpu"
    log(f"loading {model_name} on {device} ...")
    model = load_model(model_name, device)
    dim = model.get_sentence_embedding_dimension()
    lock = threading.Lock()
    model.encode([prefix + "warm up"])  # first call is slow; do it before serving
    log(f"ready on http://{args.host}:{args.port}  (dim={dim})")

    class Handler(BaseHTTPRequestHandler):
        def _json(self, code, obj):
            body = json.dumps(obj).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == "/health":
                return self._json(200, {"model": model_name, "dim": dim, "device": device})
            self._json(404, {"error": "not found"})

        def do_POST(self):
            if self.path != "/embed":
                return self._json(404, {"error": "not found"})
            try:
                n = int(self.headers.get("Content-Length") or 0)
                text = json.loads(self.rfile.read(n) or b"{}").get("text", "")
            except ValueError:
                return self._json(400, {"error": "bad json"})
            if not text.strip():
                return self._json(400, {"error": "text is required"})
            t0 = time.time()
            with lock:  # MPS is not safe to drive from several threads at once
                vec = model.encode([prefix + text], normalize_embeddings=True)[0]
            self._json(200, {"vector": [round(float(x), 6) for x in vec],
                             "ms": round((time.time() - t0) * 1000, 1)})

        def log_message(self, fmt, *a):  # quiet
            pass

    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
