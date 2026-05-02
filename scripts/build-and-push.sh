#!/usr/bin/env bash
# Build & push velodb/* demo images for linux/amd64 + linux/arm64.
#
# Usage:
#   ./scripts/build-and-push.sh                # build all, push all
#   ./scripts/build-and-push.sh --no-push      # local multi-arch build only
#   ./scripts/build-and-push.sh backend        # build a single image
#   IMAGE_TAG=4.1.1-rc1 ./scripts/build-and-push.sh
#
# Targets and build contexts (all under images/ in this repo, except rag-api):
#   backend                velodb/velodb-demo-backend                 images/backend
#   frontend               velodb/velodb-demo-frontend                images/frontend
#   grafana                velodb/velodb-demo-grafana                 images/grafana-image
#   telemetry-generator    velodb/velodb-demo-telemetry-generator     images/telemetry-generator
#   rag-api                velodb/velodb-demo-rag-api                 EXTERNAL ($RAG_REPO)
#
# `rag-api` is build-context-only: it requires the hipporag_doris/ source
# from the velodb AI repo. Set RAG_REPO=/path/to/AI to override the default
# (../AI relative to this repo).
#
# Requirements:
#   - Docker 24+ with buildx
#   - Logged in to Docker Hub (run scripts/docker-login.sh first)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGES_DIR="$REPO_ROOT/images"

NAMESPACE="${NAMESPACE:-velodb}"
IMAGE_TAG="${IMAGE_TAG:-4.1.0}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"
BUILDER="${BUILDER:-velodb-demo-builder}"

PUSH=true
TARGETS=()
for arg in "$@"; do
  case "$arg" in
    --no-push) PUSH=false ;;
    -h|--help) sed -n '1,28p' "$0"; exit 0 ;;
    *) TARGETS+=("$arg") ;;
  esac
done
if [[ ${#TARGETS[@]} -eq 0 ]]; then
  TARGETS=(backend frontend grafana telemetry-generator rag-api)
fi

# rag-api builds against an external repo (hipporag_doris source).
RAG_REPO="${RAG_REPO:-$REPO_ROOT/../AI}"

# Ensure a buildx builder with multi-arch support exists.
if ! docker buildx inspect "$BUILDER" >/dev/null 2>&1; then
  echo "[buildx] creating builder '$BUILDER'"
  docker buildx create --name "$BUILDER" --driver docker-container --use --bootstrap
else
  docker buildx use "$BUILDER"
fi

build_one() {
  local name="$1" context="$2" dockerfile="$3"
  shift 3
  local extra_args=("$@")
  local image="$NAMESPACE/velodb-demo-$name:$IMAGE_TAG"
  local image_latest="$NAMESPACE/velodb-demo-$name:latest"

  echo "============================================================"
  echo "  $name  →  $image"
  echo "  context : $context"
  echo "  push    : $PUSH"
  echo "  arches  : $PLATFORMS"
  echo "============================================================"

  local action_flag
  if [[ "$PUSH" == "true" ]]; then
    action_flag="--push"
  else
    # Multi-arch local builds can't load into the docker image store; emit OCI
    # archives in /tmp instead so the build still validates both arches.
    action_flag="--output=type=oci,dest=/tmp/${name}-${IMAGE_TAG//\//_}.tar"
  fi

  docker buildx build \
    --platform "$PLATFORMS" \
    --file "$dockerfile" \
    --tag "$image" \
    --tag "$image_latest" \
    --provenance=false \
    "${extra_args[@]}" \
    $action_flag \
    "$context"
}

for t in "${TARGETS[@]}"; do
  case "$t" in
    backend)
      build_one backend "$IMAGES_DIR/backend" "$IMAGES_DIR/backend/Dockerfile"
      ;;
    frontend)
      build_one frontend "$IMAGES_DIR/frontend" "$IMAGES_DIR/frontend/Dockerfile"
      ;;
    telemetry-generator)
      build_one telemetry-generator "$IMAGES_DIR/telemetry-generator" "$IMAGES_DIR/telemetry-generator/Dockerfile"
      ;;
    grafana)
      build_one grafana "$IMAGES_DIR/grafana-image" "$IMAGES_DIR/grafana-image/Dockerfile"
      ;;
    rag-api)
      if [[ ! -d "$RAG_REPO/hipporag_doris" ]]; then
        echo "[build] RAG source not found at $RAG_REPO/hipporag_doris" >&2
        echo "[build] set RAG_REPO=/path/to/AI to override" >&2
        exit 1
      fi
      # The Dockerfile lives in this repo; the build context is the AI repo.
      # We pass our images/rag-api/ as a named build-context for requirements.txt.
      build_one rag-api "$RAG_REPO" "$IMAGES_DIR/rag-api/Dockerfile" \
        --build-context "dist=$IMAGES_DIR/rag-api"
      ;;
    *)
      echo "[build] unknown target: $t" >&2
      exit 1
      ;;
  esac
done

echo
echo "Done.  Tag: $IMAGE_TAG"
$PUSH && echo "Pushed to: https://hub.docker.com/u/$NAMESPACE"
