#!/usr/bin/env python3
"""Detect, align and embed every face of the IMDB-WIKI faces dataset into faces.db.

Source: https://huggingface.co/datasets/ljnlonoljpiljm/imdb_wiki_faces
  ~512k face crops of ~20k celebrities, 20 parquet shards (~9.7 GB), each row
  with image + name, date of birth, year the photo was taken, gender, and
  whether it came from IMDb or Wikipedia.

For every image: SCRFD finds the faces, the main (large + central) face is
aligned to 112x112 and embedded with ArcFace R50 -> 512-d L2-normalized vector.
Stored per row in SQLite (faces.db): labels, detection info, a small JPEG
thumbnail, and the vector. Images without a detectable face are kept with a
NULL vector so they are not retried.

Resumable: shards already finished are skipped, so re-running after an
interruption (or after more shards finished downloading) just continues.

  python face-gen.py                 # every shard currently on disk
  python face-gen.py --download      # download missing shards first (resumable)
  python face-gen.py --max-shards 2  # quick trial
  python face-gen.py --download --max-shards 1 --limit 20   # smoke test: 20 photos
"""

import argparse
import io
import os
import sqlite3
import sys
import time
from concurrent.futures import ProcessPoolExecutor

import numpy as np

REPO = "ljnlonoljpiljm/imdb_wiki_faces"
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "imdb_wiki_faces")
SHARD_DIR = os.path.join(DATA_DIR, "data")
THUMB_PX = 160
CHUNK = 256          # rows per worker task

SCHEMA = """
CREATE TABLE IF NOT EXISTS faces (
    face_id     INTEGER PRIMARY KEY,   -- shard * 100000 + row within shard
    shard       INTEGER NOT NULL,
    row_idx     INTEGER NOT NULL,
    name        TEXT,
    gender      TEXT,                  -- male / female (dataset label)
    birth_date  INTEGER,               -- yyyymmdd as in the dataset
    photo_year  INTEGER,
    age         INTEGER,
    source      TEXT,                  -- imdb / wiki
    img_w       INTEGER,
    img_h       INTEGER,
    n_faces     INTEGER,               -- faces the detector found in the image
    det_score   REAL,
    face_px     INTEGER,               -- width of the chosen face box in source pixels
    bbox        TEXT,                  -- x1,y1,x2,y2 of the chosen face
    thumb       BLOB,                  -- JPEG, longest side THUMB_PX
    embedding   BLOB                   -- 512 x float32, L2-normalized; NULL = no face
);
CREATE TABLE IF NOT EXISTS shards_done (shard INTEGER PRIMARY KEY, rows INTEGER, faces INTEGER, seconds REAL);
"""

_fm = None


def _init_worker(threads):
    global _fm
    import cv2
    cv2.setNumThreads(1)
    from facemodel import FaceModel
    _fm = FaceModel(det_size=320, threads=threads)


def _process(task):
    """Worker: decode + detect + align + embed one chunk of rows."""
    import cv2
    shard, start, rows = task
    out, aligned, owners = [], [], []
    for i, r in enumerate(rows):
        rec = dict(face_id=shard * 100000 + start + i, shard=shard, row_idx=start + i,
                   name=r["name"], gender=r["gender"], birth_date=r["dob"],
                   photo_year=r["photo_taken"], age=r["age"], source=r["dataset"],
                   img_w=None, img_h=None, n_faces=0, det_score=None, face_px=None,
                   bbox=None, thumb=None, embedding=None)
        out.append(rec)
        data = r["image"]
        img = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR) if data else None
        if img is None:
            continue
        h, w = img.shape[:2]
        rec.update(img_w=w, img_h=h)
        s = THUMB_PX / max(h, w)
        th = cv2.resize(img, (max(1, round(w * s)), max(1, round(h * s))), interpolation=cv2.INTER_AREA) if s < 1 else img
        rec["thumb"] = cv2.imencode(".jpg", th, [cv2.IMWRITE_JPEG_QUALITY, 82])[1].tobytes()
        faces = _fm.detect(img)
        rec["n_faces"] = len(faces)
        f = _fm.main_face(faces, img.shape)
        if f is None:
            continue
        rec.update(det_score=round(f.score, 4), face_px=int(f.bbox[2] - f.bbox[0]),
                   bbox=",".join(str(int(round(v))) for v in f.bbox))
        aligned.append(_fm.align(img, f))
        owners.append(i)
    if aligned:
        vecs = _fm.embed(aligned)
        for i, v in zip(owners, vecs):
            out[i]["embedding"] = v.astype(np.float32).tobytes()
    return out


GENDER = {0: "male", 1: "female"}


def read_shard(path, shard, limit=0):
    import pyarrow.parquet as pq
    pf = pq.ParquetFile(path)
    cols = ["image", "name", "dob", "photo_taken", "age", "gender", "dataset"]
    start = 0
    for batch in pf.iter_batches(batch_size=CHUNK, columns=cols):
        if limit:
            if start >= limit:
                return
            batch = batch.slice(0, limit - start)
        d = batch.to_pydict()
        rows = []
        for j in range(batch.num_rows):
            img = d["image"][j]
            rows.append(dict(image=(img or {}).get("bytes"), name=d["name"][j], dob=d["dob"][j],
                             photo_taken=d["photo_taken"][j], age=d["age"][j],
                             gender=GENDER.get(d["gender"][j]), dataset=d["dataset"][j]))
        yield shard, start, rows
        start += batch.num_rows


def download(max_shards):
    from huggingface_hub import snapshot_download
    patterns = ["data/*.parquet"] if not max_shards else \
        [f"data/train-{i:05d}-of-00020.parquet" for i in range(max_shards)]
    snapshot_download(REPO, repo_type="dataset", local_dir=DATA_DIR,
                      allow_patterns=patterns + ["README.md"], max_workers=4)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", default="faces.db")
    ap.add_argument("--workers", type=int, default=6, help="worker processes (default 6)")
    ap.add_argument("--threads", type=int, default=1, help="CPU threads per worker")
    ap.add_argument("--max-shards", type=int, default=0, help="only the first N shards")
    ap.add_argument("--download", action="store_true", help="download missing shards first")
    ap.add_argument("--limit", type=int, default=0,
                    help="only the first N rows of each shard (smoke test; the shard is not marked done)")
    args = ap.parse_args()

    if args.download:
        download(args.max_shards)
    shards = sorted(f for f in os.listdir(SHARD_DIR) if f.endswith(".parquet")) if os.path.isdir(SHARD_DIR) else []
    if args.max_shards:
        shards = [s for s in shards if int(s.split("-")[1]) < args.max_shards]
    if not shards:
        sys.exit(f"no parquet shards in {SHARD_DIR} -- run with --download")

    con = sqlite3.connect(args.db)
    con.executescript(SCHEMA)
    done = set() if args.limit else {r[0] for r in con.execute("SELECT shard FROM shards_done")}
    todo = [s for s in shards if int(s.split("-")[1]) not in done]
    print(f"{len(shards)} shard(s) on disk, {len(shards) - len(todo)} already embedded, {len(todo)} to do")
    if not todo:
        return

    cols = ["face_id", "shard", "row_idx", "name", "gender", "birth_date", "photo_year", "age", "source",
            "img_w", "img_h", "n_faces", "det_score", "face_px", "bbox", "thumb", "embedding"]
    insert = f"INSERT OR REPLACE INTO faces ({','.join(cols)}) VALUES ({','.join('?' * len(cols))})"
    t_all, n_all = time.time(), 0
    with ProcessPoolExecutor(args.workers, initializer=_init_worker, initargs=(args.threads,)) as pool:
        for fname in todo:
            shard = int(fname.split("-")[1])
            t0, n, nf = time.time(), 0, 0
            con.execute("DELETE FROM faces WHERE shard = ?", (shard,))   # partial earlier attempt
            for recs in pool.map(_process, read_shard(os.path.join(SHARD_DIR, fname), shard, args.limit)):
                con.executemany(insert, [[r[c] for c in cols] for r in recs])
                n += len(recs)
                nf += sum(r["embedding"] is not None for r in recs)
                el = time.time() - t0
                print(f"\r  shard {shard:2d}: {n:6d} rows, {nf:6d} faces, {n / el:5.0f} img/s", end="", flush=True)
            el = time.time() - t0
            if not args.limit:   # a partial shard is redone by the next full run
                con.execute("INSERT OR REPLACE INTO shards_done VALUES (?,?,?,?)", (shard, n, nf, el))
            con.commit()
            n_all += n
            print(f"\r  shard {shard:2d}: {n:6d} rows, {nf:6d} faces ({nf / max(n, 1):.1%}) in {el:.0f}s")
    total, faces = con.execute("SELECT count(*), count(embedding) FROM faces").fetchone()
    print(f"done: {n_all} rows in {time.time() - t_all:.0f}s; faces.db now has {total} rows, {faces} with a vector")


if __name__ == "__main__":
    main()
