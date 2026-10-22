#!/usr/bin/env python3
"""Face similarity search web UI: photo -> face detection + ArcFace vector (local,
Apple GPU) -> HNSW ANN top-k on Doris -> matching faces with scores.

  python face-server.py              # http://localhost:8091
  python face-server.py --port 9000

Endpoints
  GET  /                      the UI (static/index.html)
  GET  /api/stats             row / person counts
  POST /api/search            {"image": dataURL|base64, "face": idx?, "top_k", "filters"}
  POST /api/search-by-id      {"face_id", "top_k", "filters"}  (a dataset face as the query)
  GET  /api/image/<face_id>   JPEG thumbnail, read from Doris
  GET  /api/thumbs?ids=1,2,3  several thumbnails in one Doris query -> {id: base64 JPEG}
  GET  /api/samples           random well-labelled faces to try

Connection: VELODB_* from .env (--env) and the environment, see env.example; run-web-server.sh does this.
"""

import argparse
import base64
import io
import json
import os
import queue
import re
import threading
import time
from collections import OrderedDict
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import cv2
import numpy as np
import pymysql
from PIL import Image, ImageOps

from doris_sql import connect, load_env
from facemodel import FaceModel

DB, TABLE = "face_search_demo", "faces"   # DB is replaced by VELODB_DATABASE in main()
ENV = {}
STATIC = os.path.join(os.path.dirname(os.path.abspath(__file__)), "static")
LABEL_OK_PCT = 35     # label_score_pct (label_score * 100) at/above which the dataset's name is trusted

# ------------------------------------------------------------------ Doris pool
class Pool:
    def __init__(self, size):
        self.q = queue.LifoQueue()
        for _ in range(size):
            self.q.put(None)

    def _connect(self):
        return connect(ENV, DB)

    def query(self, sql, args=None):
        con = self.q.get()
        try:
            for attempt in (0, 1):
                try:
                    if con is None:
                        con = self._connect()
                    with con.cursor() as cur:
                        cur.execute(sql, args)
                        cols = [d[0] for d in cur.description] if cur.description else []
                        return [dict(zip(cols, r)) for r in cur.fetchall()]
                except pymysql.err.OperationalError:
                    if con is not None:
                        try:
                            con.close()
                        except Exception:
                            pass
                    con = None
                    if attempt:
                        raise
        finally:
            self.q.put(con)


pool = None
model = None
executor = ThreadPoolExecutor(8)
model_lock = threading.Lock()   # CoreML sessions: one inference at a time
image_cache = OrderedDict()
cache_lock = threading.Lock()


# Per-query settings that keep filtered searches on the HNSW index:
#  * inverted_index_skip_threshold=100: always evaluate filters with their inverted
#    index. By default Doris skips the index for a predicate matching > 50% of rows
#    (e.g. label_score_pct >= 35), and an ANN query with any predicate evaluated
#    outside an index falls back to a brute-force scan.
#  * ann_index_candidate_rows_percent_threshold is left at its default (0.3):
#    when a filter leaves < 30% of rows, Doris does an exact scan of just those
#    rows, because HNSW recall degrades for narrow filters (measured 0.7-0.8 vs 1.0).
#  * hnsw_ef_search=128: recall@30 0.99 (0.97 at the default 32/64), ~1 ms/segment.
def ann_hint(ef):
    return f"/*+ SET_VAR(hnsw_ef_search={int(ef or 128)}, inverted_index_skip_threshold=100) */ "


def vector_literal(v):
    return "[" + ",".join(f"{x:.6f}" for x in v) + "]"


def where_clause(f):
    """Filters from the UI -> SQL predicates (values are validated, not interpolated raw)."""
    preds = []
    f = f or {}
    if f.get("gender") in ("male", "female"):
        preds.append(f"gender = '{f['gender']}'")
    if f.get("source") in ("imdb", "wiki"):
        preds.append(f"source = '{f['source']}'")
    if f.get("verified"):
        # label_score_pct, not the FLOAT label_score: only indexed predicates let
        # Doris keep using the HNSW index (unindexed ones force a brute-force scan)
        preds.append(f"label_score_pct >= {LABEL_OK_PCT}")
    for key, col, op in (("born_from", "birth_year", ">="), ("born_to", "birth_year", "<="),
                         ("age_from", "age", ">="), ("age_to", "age", "<=")):
        v = f.get(key)
        if isinstance(v, (int, float)) or (isinstance(v, str) and v.strip().isdigit()):
            preds.append(f"{col} {op} {int(v)}")
    if f.get("exclude_id") is not None:
        preds.append(f"face_id != {int(f['exclude_id'])}")
    return preds


def ann_search(vec, top_k, filters, ef=None):
    preds = where_clause(filters)
    where = ("\nWHERE " + " AND ".join(preds)) if preds else ""
    cols = "face_id, name, gender, birth_year, photo_year, age, source, person_faces, label_score, det_score"

    hint = ann_hint(ef)

    def sql_for(lit):
        return (f"SELECT {hint}{cols},\n       inner_product_approximate(embedding, {lit}) AS score\n"
                f"FROM {TABLE}{where}\nORDER BY score DESC\nLIMIT {int(top_k)}")
    sql = sql_for(vector_literal(vec))
    t = time.time()
    rows = pool.query(sql)
    ms = (time.time() - t) * 1000
    shown = sql_for(f"[{vec[0]:.4f}, {vec[1]:.4f}, {vec[2]:.4f}, ... 512 floats]")
    for r in rows:
        for k in ("score", "label_score", "det_score"):
            if r.get(k) is not None:
                r[k] = round(float(r[k]), 4)
    return rows, ms, shown


def people_search(vec, top_k, filters, ef=None):
    """ANN over per-person centroid vectors (table `people`): who does this face look like?"""
    f = filters or {}
    preds = [p for p in where_clause({k: f.get(k) for k in ("gender", "born_from", "born_to")})]
    where = ("\nWHERE " + " AND ".join(preds)) if preds else ""
    hint = ann_hint(ef)

    def sql_for(lit):
        return (f"SELECT {hint}name, gender, birth_year, photos, verified_photos, face_id,\n"
                f"       inner_product_approximate(embedding, {lit}) AS score\n"
                f"FROM people{where}\nORDER BY score DESC\nLIMIT {int(top_k)}")
    t = time.time()
    rows = pool.query(sql_for(vector_literal(vec)))
    ms = (time.time() - t) * 1000
    for r in rows:
        r["score"] = round(float(r["score"]), 4)
    return rows, ms, sql_for(f"[{vec[0]:.4f}, {vec[1]:.4f}, {vec[2]:.4f}, ... 512 floats]")


def decode_upload(data):
    if "," in data[:100]:
        data = data.split(",", 1)[1]
    img = Image.open(io.BytesIO(base64.b64decode(data)))
    img = ImageOps.exif_transpose(img).convert("RGB")
    if max(img.size) > 2000:
        img.thumbnail((2000, 2000))
    return cv2.cvtColor(np.asarray(img), cv2.COLOR_RGB2BGR)


def jpeg_b64(img, px=None, q=85):
    if px:
        s = px / max(img.shape[:2])
        if s < 1:
            img = cv2.resize(img, None, fx=s, fy=s, interpolation=cv2.INTER_AREA)
    return base64.b64encode(cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, q])[1]).decode()


def search_upload(req):
    timing = {}
    t = time.time()
    img = decode_upload(req["image"])
    timing["decode_ms"] = (time.time() - t) * 1000
    t = time.time()
    with model_lock:
        faces = model.detect(img)
    timing["detect_ms"] = (time.time() - t) * 1000
    h, w = img.shape[:2]
    face_list = []
    for f in faces:
        x1, y1, x2, y2 = f.bbox
        pad = 0.25 * (x2 - x1)
        crop = img[int(max(0, y1 - pad)):int(min(h, y2 + pad)), int(max(0, x1 - pad)):int(min(w, x2 + pad))]
        face_list.append({"bbox": [round(float(v) / w if i % 2 == 0 else float(v) / h, 4)
                                   for i, v in enumerate(f.bbox)],
                          "score": round(f.score, 3), "thumb": jpeg_b64(crop, 128) if crop.size else None})
    out = {"faces": face_list, "width": w, "height": h, "timing": timing}
    if not faces:
        out["error"] = "No face found in this photo. Try a clearer, front-facing picture."
        return out
    idx = req.get("face")
    idx = idx if isinstance(idx, int) and 0 <= idx < len(faces) else 0
    t = time.time()
    with model_lock:
        aligned = model.align(img, faces[idx])
        vec = model.embed([aligned])[0]
    timing["embed_ms"] = (time.time() - t) * 1000
    out.update(chosen=idx, aligned=jpeg_b64(aligned))
    return run_search(vec, req, out)


def search_by_id(req):
    fid = int(req["face_id"])
    t = time.time()
    rows = pool.query(f"SELECT face_id, name, embedding FROM {TABLE} WHERE face_id = %s", (fid,))
    if not rows:
        return {"error": f"face {fid} not found"}
    vec = np.array(json.loads(rows[0]["embedding"]), dtype=np.float32)
    out = {"query_face": {"face_id": fid, "name": rows[0]["name"]},
           "timing": {"fetch_vector_ms": (time.time() - t) * 1000}}
    filters = dict(req.get("filters") or {})
    return run_search(vec, dict(req, filters=filters), out)


def run_search(vec, req, out):
    top_k = max(1, min(int(req.get("top_k", 24)), 200))
    filters = req.get("filters") or {}
    # photos and people are two independent ANN queries: run them concurrently
    f_faces = executor.submit(ann_search, vec, top_k, filters, req.get("ef"))
    f_people = executor.submit(people_search, vec, 12, filters, req.get("ef"))
    rows, ms, sql = f_faces.result()
    prow, pms, psql = f_people.result()
    out["timing"]["doris_ms"] = ms
    out["timing"]["doris_people_ms"] = pms
    out["results"] = rows
    out["people"] = prow
    out["sql"] = sql + ";\n\n-- in parallel, against per-person centroids:\n" + psql + ";"
    out["timing"] = {k: round(v, 1) for k, v in out["timing"].items()}
    return out


def get_image(fid):
    with cache_lock:
        if fid in image_cache:
            image_cache.move_to_end(fid)
            return image_cache[fid]
    rows = pool.query(f"SELECT image FROM {TABLE} WHERE face_id = %s", (fid,))
    data = base64.b64decode(rows[0]["image"]) if rows and rows[0]["image"] else None
    if data:
        with cache_lock:
            image_cache[fid] = data
            while len(image_cache) > 5000:
                image_cache.popitem(last=False)
    return data


def get_images(ids):
    """Thumbnails for many faces with one Doris query (plus the in-process cache)."""
    out, missing = {}, []
    with cache_lock:
        for i in ids:
            if i in image_cache:
                out[i] = image_cache[i]
            else:
                missing.append(i)
    if missing:
        rows = pool.query(f"SELECT face_id, image FROM {TABLE} WHERE face_id IN ({','.join(str(int(i)) for i in missing)})")
        with cache_lock:
            for r in rows:
                if r["image"]:
                    data = base64.b64decode(r["image"])
                    out[int(r["face_id"])] = image_cache[int(r["face_id"])] = data
            while len(image_cache) > 5000:
                image_cache.popitem(last=False)
    return {str(k): base64.b64encode(v).decode() for k, v in out.items()}


_stats = {}


def stats():
    if not _stats or time.time() - _stats.get("_t", 0) > 300:
        r = pool.query(f"SELECT count(*) AS faces, count(DISTINCT name) AS names, "
                       f"sum(label_score_pct >= {LABEL_OK_PCT}) AS verified, "
                       f"(SELECT count(*) FROM people) AS people FROM {TABLE}")[0]
        _stats.update({k: int(v or 0) for k, v in r.items()}, _t=time.time())
    return {k: v for k, v in _stats.items() if not k.startswith("_")}


def samples(n=18):
    return pool.query(
        f"SELECT face_id, name FROM {TABLE} "
        f"WHERE label_score >= 0.6 AND person_faces >= 40 AND face_px >= 80 AND n_faces = 1 "
        f"ORDER BY rand() LIMIT {int(n)}")


# ------------------------------------------------------------------ HTTP
class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *a):
        if os.environ.get("VERBOSE"):
            super().log_message(fmt, *a)

    def _send(self, code, body, ctype="application/json", cache=None):
        if not isinstance(body, (bytes, bytearray)):
            body = json.dumps(body, default=str).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        if cache:
            self.send_header("Cache-Control", cache)
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        try:
            if self.path in ("/", "/index.html"):
                with open(os.path.join(STATIC, "index.html"), "rb") as fh:
                    return self._send(200, fh.read(), "text/html; charset=utf-8", "no-cache")
            if self.path == "/api/stats":
                return self._send(200, stats())
            if self.path.startswith("/api/samples"):
                return self._send(200, samples())
            m = re.fullmatch(r"/api/thumbs\?ids=([\d,]*)", self.path)
            if m:
                ids = [int(x) for x in m.group(1).split(",") if x][:300]
                return self._send(200, get_images(ids) if ids else {})
            m = re.fullmatch(r"/api/image/(\d+)", self.path)
            if m:
                data = get_image(int(m.group(1)))
                if data is None:
                    return self._send(404, {"error": "no image"})
                return self._send(200, data, "image/jpeg", "public, max-age=86400")
            self._send(404, {"error": "not found"})
        except Exception as e:
            self._send(500, {"error": f"{type(e).__name__}: {e}"})

    def do_POST(self):
        try:
            n = int(self.headers.get("Content-Length", 0))
            if n > 40 * 1024 * 1024:
                return self._send(413, {"error": "image too large"})
            req = json.loads(self.rfile.read(n) or b"{}")
            if self.path == "/api/search":
                return self._send(200, search_upload(req))
            if self.path == "/api/search-by-id":
                return self._send(200, search_by_id(req))
            self._send(404, {"error": "not found"})
        except Exception as e:
            self._send(500, {"error": f"{type(e).__name__}: {e}"})


def main():
    global pool, model, DB, ENV
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--port", type=int, default=int(os.environ.get("PORT", 8091)))
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--pool", type=int, default=12, help="Doris connections")
    ap.add_argument("--env", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"))
    args = ap.parse_args()
    ENV = load_env(args.env)
    ENV.setdefault("READ_TIMEOUT", "120")
    DB = ENV.get("VELODB_DATABASE") or DB
    pool = Pool(args.pool)
    model = FaceModel(det_size=640)
    # warm up the CoreML sessions (first call compiles them)
    dummy = np.zeros((640, 640, 3), np.uint8)
    model.detect(dummy)
    model.embed([np.zeros((112, 112, 3), np.uint8)])
    print(f"face search UI on http://localhost:{args.port}  (Doris {ENV['VELODB_HOST']}, {DB}.{TABLE})")
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
