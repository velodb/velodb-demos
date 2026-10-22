#!/usr/bin/env python3
"""Export faces.db to JSON-lines chunks for Doris stream load (export/faces-NNNN.jsonl).

Also computes `label_score` for every face. IMDB-WIKI labels are noisy (an IMDb
still tagged with one actor often shows a co-star), so for every name with at
least 3 faces we estimate what that person looks like -- the centroid of their
embeddings, re-computed from the faces that agree with it -- and store each
face's cosine similarity to it. A low label_score means "the photo probably
shows someone else"; the UI and SQL can filter on it.

It also writes export/people-0000.jsonl: one row per person with the centroid
("average face") of their verified photos, for "which celebrity do you look
like" searches that rank identities instead of individual photos.

  python face-export.py              # all faces with a vector
  python face-export.py --chunk 20000
"""

import argparse
import base64
import glob
import json
import os
import sqlite3
import time
from collections import defaultdict

import numpy as np

DIM = 512


def person_scores(names, vecs, min_faces=3, same=0.4):
    """Per face: cosine to the centroid of its name's dominant identity (NaN if unknown).

    The dominant identity is the densest group of mutually similar faces under
    the name: the face with the most same-name neighbours at cosine >= `same`,
    plus those neighbours. Scores are leave-one-out (a face never counts towards
    its own centroid), otherwise small groups would vouch for themselves."""
    by_name = defaultdict(list)
    for i, n in enumerate(names):
        if n:
            by_name[n].append(i)
    score = np.full(len(names), np.nan, dtype=np.float32)
    count = np.zeros(len(names), dtype=np.int32)
    people = []       # (name, member indices, centroid)
    for name, idx in by_name.items():
        idx = np.asarray(idx)
        count[idx] = len(idx)
        if len(idx) < min_faces:
            continue
        v = vecs[idx]
        sim = v @ v.T
        np.fill_diagonal(sim, -1)
        nbrs = (sim >= same).sum(1)
        seed = int(nbrs.argmax())
        if nbrs[seed] < 1:             # no two photos agree: identity unknown
            continue
        members = sim[seed] >= same
        members[seed] = True
        total = v[members].sum(0)
        people.append((name, idx[members], total / (np.linalg.norm(total) + 1e-9)))
        for j in range(len(idx)):
            c = total - v[j] if members[j] else total
            if members[j] and members.sum() == 1:
                continue
            score[idx[j]] = float(v[j] @ c) / (np.linalg.norm(c) + 1e-9)
    return score, count, people


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", default="faces.db")
    ap.add_argument("--out", default="export")
    ap.add_argument("--chunk", type=int, default=20000)
    args = ap.parse_args()

    t0 = time.time()
    con = sqlite3.connect(args.db)
    ids, names, vecs = [], [], []
    for fid, name, emb in con.execute("SELECT face_id, name, embedding FROM faces WHERE embedding IS NOT NULL ORDER BY face_id"):
        ids.append(fid)
        names.append(name)
        vecs.append(np.frombuffer(emb, np.float32))
    vecs = np.stack(vecs)
    print(f"{len(ids)} faces loaded in {time.time() - t0:.0f}s; computing label consistency ...")
    lscore, pcount, people = person_scores(names, vecs)
    extra = {fid: (s, c) for fid, s, c in zip(ids, lscore, pcount)}
    print(f"  faces with label_score: {np.isfinite(lscore).sum()}, "
          f"of which < 0.3 (likely mislabeled): {(lscore < 0.3).sum()}")

    os.makedirs(args.out, exist_ok=True)
    for f in glob.glob(os.path.join(args.out, "*.jsonl")):
        os.remove(f)

    meta = {fid: (g, b) for fid, g, b in con.execute("SELECT face_id, gender, birth_date FROM faces")}
    with open(os.path.join(args.out, "people-0000.jsonl"), "w") as fh:
        for name, members, c in people:
            rep = int(members[np.argmax(vecs[members] @ c)])      # most typical photo
            genders = [meta[ids[m]][0] for m in members if meta[ids[m]][0]]
            bd = meta[ids[rep]][1]
            by = bd // 10000 if bd and bd > 10000 else None
            fh.write(json.dumps({
                "name": name, "gender": max(set(genders), key=genders.count) if genders else None,
                "birth_year": by if by and 1800 < by < 2030 else None,
                "photos": int(pcount[rep]), "verified_photos": int(len(members)), "face_id": ids[rep],
                "embedding": [round(float(x), 6) for x in c]}, separators=(",", ":")) + "\n")
    print(f"  people with a centroid: {len(people)}")
    q = ("SELECT face_id, name, gender, birth_date, photo_year, age, source, img_w, img_h, n_faces, "
         "det_score, face_px, thumb, embedding FROM faces WHERE embedding IS NOT NULL ORDER BY face_id")
    n, part, fh = 0, 0, None
    for (fid, name, gender, bdate, pyear, age, source, w, h, nfaces, det, fpx, thumb, emb) in con.execute(q):
        if n % args.chunk == 0:
            if fh:
                fh.close()
            fh = open(os.path.join(args.out, f"faces-{part:04d}.jsonl"), "w")
            part += 1
        s, c = extra[fid]
        by = bdate // 10000 if bdate and bdate > 10000 else None
        rec = {
            "face_id": fid, "name": name, "gender": gender,
            "birth_year": by if by and 1800 < by < 2030 else None,
            "photo_year": pyear, "age": age if age is not None and 0 <= age <= 110 else None,
            "source": source, "img_w": w, "img_h": h, "n_faces": nfaces,
            "det_score": det, "face_px": fpx,
            "person_faces": int(c), "label_score": None if np.isnan(s) else round(float(s), 4),
            "label_score_pct": None if np.isnan(s) else int(round(float(s) * 100)),
            "image": base64.b64encode(thumb).decode() if thumb else None,
            "embedding": [round(float(x), 6) for x in np.frombuffer(emb, np.float32)],
        }
        fh.write(json.dumps(rec, separators=(",", ":")) + "\n")
        n += 1
    if fh:
        fh.close()
    json.dump({"rows": n, "chunks": part, "dim": DIM, "model": "insightface buffalo_l w600k_r50"},
              open(os.path.join(args.out, "manifest.json"), "w"))
    print(f"wrote {n} rows in {part} chunk(s) to {args.out}/ in {time.time() - t0:.0f}s")


if __name__ == "__main__":
    main()
