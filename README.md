# VeloDB Demo

End-to-end demo of [Apache Doris 4.1.0](https://doris.apache.org/) covering
three workloads — real-time e-commerce analytics, OpenTelemetry observability,
and multimodal RAG — packaged as a single `docker compose up`.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  E-commerce dashboard  ──►  backend (Go)  ──►  Apache Doris 4.1 (FE + BE)   │
│   (5173)                    (8081)              (9030 mysql, 8030 http)     │
│                                │                                            │
│                                ├──►  PostgreSQL  (5432)                     │
│                                └──►  Redpanda    (9092)                     │
│                                                                             │
│  Grafana (33000) ───────►  OTEL collector  ◄─── telemetry-generator         │
│   - VeloDB Demo dashboards     │                                            │
│   - Doris App plugin           ▼                                            │
│                          Doris (demo_observability)                         │
│                                                                             │
│  RAG UI                ──►  rag-api (8000)  ──►  Doris (rag_db)             │
│   - chunk explorer          - hybrid search          - 411 chunks           │
│   - knowledge graph         - chat (OpenRouter)      - 300 entities         │
│   - 1024-d BGE-M3                                    - 19 demo images       │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Quick start

```bash
git clone <this-repo>
cd velodb-demo
cp .env.example .env                # set OTEL_TIMEZONE if not in PT
docker compose --profile all up -d  # ~90 s for OTEL schema + RAG ingest
```

Then open:

| URL | What |
|---|---|
| http://localhost:5173 | E-commerce dashboard |
| http://localhost:5173/rag | RAG / GenAI demo |
| http://localhost:33000 | Grafana — VeloDB Demo folder |
| http://localhost:33000/a/doris-app | Doris-App (Kibana-style log explorer) |
| http://localhost:8000/docs | RAG API Swagger |
| `mysql -h 127.0.0.1 -P 9030 -u root -pVeloDB@demo123` | Direct Doris SQL |

Stop with `docker compose --profile all down` (keeps data) or `down -v` (wipes
volumes).

## Profiles

| Profile | Adds | Use when |
|---|---|---|
| (default) | core e-commerce + Doris + Postgres + Redpanda | quickest demo |
| `--profile observability` | Grafana + OTEL collector + telemetry generator + partition init | OpenTelemetry / Grafana story |
| `--profile rag` | rag-init + rag-api with bundled 411-chunk corpus | RAG / multimodal story |
| `--profile all` | everything | full pitch |

## What's inside

```
velodb-demo/
├── docker-compose.yml             ← single source of truth
├── .env.example                   ← tunables (ports, creds, image tags, timezone)
├── docs/TIMEZONE.md               ← Doris-App timezone setup (read this!)
├── doris/                         ← FE/BE config + schema bootstrap SQL
├── postgres/                      ← OLTP schema + TTL job
├── grafana/                       ← provisioning + 4 dashboards (logs/traces/metrics)
├── otel/                          ← collector config + telemetry-gen definitions
├── rag/
│   ├── init/                      ← rag_unified schema + 3 materialized views
│   └── data/
│       ├── chunks.json            ← 411 chunks across 15 docs (BGE-M3, 1024-d)
│       └── images/                ← 19 demo images
├── scripts/                       ← bootstrap + buildx helpers
└── images/                        ← Dockerfile build contexts
    ├── backend/                   ← Go source
    ├── frontend/                  ← React/Vite source
    ├── grafana-image/             ← Grafana base + doris-app plugin (~50 MB)
    ├── telemetry-generator/       ← Eclipse Temurin + Cisco fat JAR
    └── rag-api/                   ← Dockerfile only (build context: ../AI repo)
```

The compose file pulls **pre-built images from Docker Hub** by default
(namespace `velodb/`), so a clone + `docker compose up` works without building
anything locally.

## Building / publishing the images

The five images are:

| Target | Image | Approx size (arm64) |
|---|---|---|
| backend | `velodb/velodb-demo-backend:4.1.0` | ~25 MB |
| frontend | `velodb/velodb-demo-frontend:4.1.0` | ~50 MB |
| grafana | `velodb/velodb-demo-grafana:4.1.0` | ~660 MB (Grafana + plugin) |
| telemetry-generator | `velodb/velodb-demo-telemetry-generator:4.1.0` | ~313 MB (JRE) |
| rag-api | `velodb/velodb-demo-rag-api:4.1.0` | ~295 MB (Python) |

**The first four** build from sources in this repo:

```bash
export DOCKERHUB_TOKEN=dckr_pat_...     # or write to ~/.docker/velodb-pat
./scripts/docker-login.sh
./scripts/build-and-push.sh             # all 5
./scripts/build-and-push.sh --no-push   # validate locally without pushing
./scripts/build-and-push.sh frontend    # one image
```

**The fifth (`rag-api`)** has Dockerfile + requirements.txt only — the Python
source (`hipporag_doris/`) lives in the velodb AI repo:

```bash
RAG_REPO=/path/to/AI ./scripts/build-and-push.sh rag-api
```

Multi-arch (linux/amd64 + linux/arm64) via `docker buildx`.

## Timezone gotcha

The Grafana **Doris App** plugin formats time-range queries in your *browser's*
timezone. Set `OTEL_TIMEZONE` in `.env` to match your browser zone (or set the
laptop OS to UTC). The included `otel-partition-init` service handles the
historical-partition backfill automatically. Full explanation and per-region
examples in **[`docs/TIMEZONE.md`](docs/TIMEZONE.md)**.

## RAG features that need an API key

The RAG hybrid search and chat endpoints embed user queries via OpenRouter.
Add a key to `.env` (free tier from <https://openrouter.ai>) and restart:

```bash
echo 'OPENROUTER_API_KEY=sk-or-v1-...' >> .env
docker compose restart rag-api
```

Without a key, the corpus browser (`/api/v1/chunks`), entity graph
(`/api/v1/knowledge-graph`), and image serving (`/api/v1/images/{path}`)
still work — only semantic/keyword search and chat require it.

## License

Apache-2.0 — see [LICENSE](LICENSE).

The bundled Grafana **Doris App** plugin is © SelectDB and ships under its
own terms.
