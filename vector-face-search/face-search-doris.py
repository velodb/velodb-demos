#!/usr/bin/env python3
"""Find the most similar faces in Doris for a photo, from the command line.

  python face-search-doris.py photo.jpg
  python face-search-doris.py photo.jpg --top 20 --where "gender = 'female' AND label_score_pct >= 35"
  python face-search-doris.py photo.jpg --print-vector      # vector literal for hand-written SQL

Every face in the photo is searched (largest first). For each: the top people
(per-person centroid table) and the top individual photos (faces table).
"""

import argparse
import os
import time

import cv2

from doris_sql import connect, load_env
from facemodel import FaceModel


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("image")
    ap.add_argument("--top", type=int, default=10)
    ap.add_argument("--where", help="extra SQL predicate on the faces table")
    ap.add_argument("--max-faces", type=int, default=3)
    ap.add_argument("--print-vector", action="store_true")
    ap.add_argument("--env", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"))
    args = ap.parse_args()

    img = cv2.imread(args.image)
    if img is None:
        raise SystemExit(f"cannot read {args.image}")
    fm = FaceModel(det_size=640)
    t = time.time()
    faces = fm.detect(img)[:args.max_faces]
    if not faces:
        raise SystemExit("no face found")
    vecs = fm.embed([fm.align(img, f) for f in faces])
    print(f"{len(faces)} face(s), detect + embed {1000 * (time.time() - t):.0f} ms")
    if args.print_vector:
        print("[" + ", ".join(f"{x:.6f}" for x in vecs[0]) + "]")
        return

    env = load_env(args.env)
    con = connect(env, env.get("VELODB_DATABASE") or "face_search_demo")
    cur = con.cursor()
    where = f"WHERE {args.where}\n" if args.where else ""
    for i, (f, v) in enumerate(zip(faces, vecs)):
        lit = "[" + ",".join(f"{x:.6f}" for x in v) + "]"
        x1, y1, x2, y2 = (int(c) for c in f.bbox)
        print(f"\n=== face {i + 1} at ({x1},{y1})-({x2},{y2}), detector score {f.score:.2f}")

        t = time.time()
        hint = "/*+ SET_VAR(hnsw_ef_search=128, inverted_index_skip_threshold=100) */"
        cur.execute(f"SELECT {hint} name, birth_year, verified_photos, inner_product_approximate(embedding, {lit}) AS score "
                    f"FROM people ORDER BY score DESC LIMIT 5")
        rows = cur.fetchall()
        print(f"-- people (centroids), {1000 * (time.time() - t):.0f} ms")
        for name, by, n, s in rows:
            print(f"  {s:6.3f}  {name} (b. {by}, {n} photos)")

        t = time.time()
        cur.execute(f"SELECT {hint} face_id, name, photo_year, label_score, inner_product_approximate(embedding, {lit}) AS score "
                    f"FROM faces {where}ORDER BY score DESC LIMIT {args.top}")
        rows = cur.fetchall()
        print(f"-- photos, {1000 * (time.time() - t):.0f} ms")
        for fid, name, year, ls, s in rows:
            flag = "" if ls is None or ls >= 0.35 else "  (label doubtful)"
            print(f"  {s:6.3f}  #{fid:<8d} {name}, {year}{flag}")


if __name__ == "__main__":
    main()
