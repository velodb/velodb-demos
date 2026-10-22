#!/usr/bin/env bash
# Sync the generated parquet files to S3 with `rs3 mirror` (resumable: existing objects are skipped).
#   ./upload.sh                    # data/ -> <Alias>/<Bucket>/<Prefix>/ from s3.yaml (RS3_ALIAS overrides)
#   ./upload.sh /other/dir
#   ./upload.sh data --dry-run     # show what would be transferred
#   ./upload.sh data --remove      # also delete remote objects that no longer exist locally
#   ./upload.sh data --overwrite   # re-upload everything
#   PARALLEL=16 ./upload.sh
set -euo pipefail
cd "$(dirname "$0")"
DATA_DIR="${1:-data}"; shift || true
[ -d "$DATA_DIR" ] || { echo "no such dir: $DATA_DIR" >&2; exit 1; }
yaml() { { grep -i "^$1:" s3.yaml || true; } | head -1 | sed -E 's/^[^:]*:[[:space:]]*//' | tr -d '"'"'"; }
BUCKET=$(yaml Bucket); PREFIX=$(yaml Prefix)
ALIAS="${RS3_ALIAS:-$(yaml Alias)}"   # rs3 alias name (see: rs3 alias list)
[ -n "$BUCKET" ] && [ -n "$ALIAS" ] || { echo "s3.yaml must define Bucket and Alias (or set RS3_ALIAS); see s3.yaml.example" >&2; exit 1; }
TARGET="${ALIAS}/${BUCKET}/${PREFIX}/"
if ls "$DATA_DIR"/*.tmp >/dev/null 2>&1; then echo "refusing: $DATA_DIR contains .tmp files (generator still running / interrupted)" >&2; exit 1; fi
echo "mirroring $DATA_DIR/ ($(ls "$DATA_DIR" | wc -l | tr -d ' ') files, $(du -sh "$DATA_DIR" | cut -f1)) -> $TARGET with ${PARALLEL:-8} parallel streams"
rs3 mirror -P "${PARALLEL:-8}" "$@" "$DATA_DIR/" "$TARGET"
echo "remote:"; rs3 du "$TARGET" 2>/dev/null || true
rs3 ls "$TARGET" | wc -l | xargs -I{} echo "{} objects under $TARGET"
