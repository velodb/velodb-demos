# syntax=docker/dockerfile:1.7
#
# velodb-demo-rag-api — minimal RAG API on Apache Doris.
#
# Build context expected: the AI repo root (`/path/to/demos/AI`),
# with `dist/rag-api/Dockerfile` and `dist/rag-api/requirements.txt`
# referenced via -f / build-time copy.
#
# Two stages:
#   1. builder   — install python wheels into a venv (uses BUILDPLATFORM)
#   2. runtime   — slim Python with the venv copied in (target arch)

# -----------------------------------------------------------------------------
# Stage 1 — wheel install
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM python:3.11-slim AS builder

ENV PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PYTHONDONTWRITEBYTECODE=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
    && rm -rf /var/lib/apt/lists/*

RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

COPY --from=dist requirements.txt /tmp/requirements.txt
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --upgrade pip wheel \
 && pip install -r /tmp/requirements.txt \
 && find /opt/venv -name '__pycache__' -prune -exec rm -rf {} + \
 && find /opt/venv -name 'tests'       -prune -exec rm -rf {} + \
 && find /opt/venv -name '*.pyc' -delete \
 && find /opt/venv -name '*.so'  -exec strip --strip-unneeded {} + 2>/dev/null || true

# -----------------------------------------------------------------------------
# Stage 2 — runtime
# -----------------------------------------------------------------------------
FROM python:3.11-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --create-home --shell /sbin/nologin app

ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/app

WORKDIR /app

COPY --from=builder /opt/venv /opt/venv
COPY --chown=app:app hipporag_doris/ /app/hipporag_doris/

USER app
EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=10s --start-period=20s --retries=3 \
    CMD curl -fsS http://localhost:8000/health || exit 1

CMD ["uvicorn", "hipporag_doris.api.main:app", "--host", "0.0.0.0", "--port", "8000"]
