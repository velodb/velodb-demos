#!/usr/bin/env bash
#
# Generate Parquet from a Doris schema and stream it to S3.
#
#   ./run.sh                      run dest.local.toml as configured
#   ./run.sh -t 1GiB              override the size target for this run
#   ./run.sh -r 100000            or the row count
#   ./run.sh --local /tmp/pq      write to a directory instead
#   ./run.sh -n                   print the command without running it
#
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

BIN="./target/release/doris-parquet-s3-gen"
SCHEMA="sample.sql"
SPEC="sample.spec"
CONFIG="dest.local.toml"
TARGET_SIZE=""
ROWS=""
TIME_LIMIT=""
LOCAL_DIR=""
DRY_RUN="no"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,9p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  cat <<'USAGE'

Options:
  -s, --schema FILE     Doris CREATE TABLE file      (default: sample.sql)
  -p, --spec FILE       field generation spec        (default: sample.spec)
  -c, --config FILE     destination config           (default: dest.local.toml)
  -t, --target SIZE     override run.target_size for this run
  -r, --rows N          override run.rows for this run
  -T, --time DURATION   override run.time for this run
  -o, --local DIR       write to DIR with default settings instead
  -n, --dry-run         print the command and exit
  -h, --help            this text

Threads, buffering, file size and batch folders come from the config's
[run] and [layout] sections. Sizes accept K/M/G/T/P with optional i and B,
for example 512M, 2GiB, 40TiB; every unit is a power of 1024.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -s|--schema)    SCHEMA="${2:?--schema needs a file}"; shift 2 ;;
    -p|--spec)      SPEC="${2:?--spec needs a file}"; shift 2 ;;
    -c|--config)    CONFIG="${2:?--config needs a file}"; shift 2 ;;
    -t|--target)    TARGET_SIZE="${2:?--target needs a size}"; shift 2 ;;
    -r|--rows)      ROWS="${2:?--rows needs a number}"; shift 2 ;;
    -T|--time)      TIME_LIMIT="${2:?--time needs a duration}"; shift 2 ;;
    -o|--local)     LOCAL_DIR="${2:?--local needs a directory}"; shift 2 ;;
    -n|--dry-run)   DRY_RUN="yes"; shift ;;
    -h|--help)      usage; exit 0 ;;
    *)              die "unknown option '$1' (try --help)" ;;
  esac
done

# ---- preflight ------------------------------------------------------------

[[ -f "$SCHEMA" ]] || die "schema file '$SCHEMA' not found"
[[ -f "$SPEC" ]]   || die "spec file '$SPEC' not found"

if [[ ! -x "$BIN" ]]; then
  echo "building release binary..."
  cargo build --release
fi

DEST_ARGS=()
if [[ -n "$LOCAL_DIR" ]]; then
  DEST_ARGS=(--out-dir "$LOCAL_DIR")
  DEST_LABEL="$LOCAL_DIR"
else
  if [[ ! -f "$CONFIG" ]]; then
    if [[ -f "dest.toml" ]]; then
      die "config '$CONFIG' not found. Copy the template and edit it:
    cp dest.toml $CONFIG
Anything matching *.local.* is gitignored, so your bucket stays out of git."
    fi
    die "config '$CONFIG' not found; create one with:
    $BIN --emit-config $CONFIG"
  fi

  # Credentials may come from the environment, a profile, or the config file.
  if ! grep -q '^[[:space:]]*access_key_id' "$CONFIG"; then
    if [[ -z "${AWS_ACCESS_KEY_ID:-}" && -z "${AWS_PROFILE:-}" ]]; then
      die "no credentials: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY,
    set AWS_PROFILE, or add an [s3.credentials] section to $CONFIG"
    fi
  fi

  DEST_TYPE=$(sed -n 's/^[[:space:]]*type[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -1)
  BUCKET=$(sed -n 's/^[[:space:]]*bucket[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -1)
  PREFIX=$(sed -n 's/^[[:space:]]*prefix[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -1)
  if [[ "$DEST_TYPE" == "local" ]]; then
    DEST_LABEL=$(sed -n 's/^[[:space:]]*directory[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -1)
  else
    [[ -n "$BUCKET" ]] || die "no bucket found in $CONFIG"
    DEST_LABEL="s3://${BUCKET}/${PREFIX}"
  fi

  # Each run normally writes into a fresh <run_id>/ folder, so nothing can be
  # overwritten. Only a run_id pinned in the config can land on an earlier
  # run's objects, so only then is it worth asking the bucket.
  if [[ "$DEST_TYPE" != "local" ]] && grep -qE '^[[:space:]]*run_id[[:space:]]*=' "$CONFIG"; then
    RUN_ID=$(sed -n 's/^[[:space:]]*run_id[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -1)
    TARGET="s3://${BUCKET}/${PREFIX}${RUN_ID:+${RUN_ID}/}"
    if command -v aws >/dev/null 2>&1; then
      EXISTING=$(aws s3 ls "$TARGET" --recursive 2>/dev/null | grep -c '\.parquet' || true)
      if [[ "${EXISTING:-0}" -gt 0 ]]; then
        echo "note: run_id is pinned to '${RUN_ID}' in ${CONFIG}, and ${EXISTING}"
        echo "      parquet object(s) already exist under ${TARGET}"
        echo "      this run reuses those names and may overwrite them."
        read -r -p "continue? [y/N] " reply
        [[ "$reply" =~ ^[Yy]$ ]] || { echo "aborted."; exit 1; }
      fi
    fi
  fi
fi

CMD=("$BIN"
  --schema "$SCHEMA"
  --spec "$SPEC"
  "${DEST_ARGS[@]}")

# Stop conditions override the config for this run only.
if [[ -n "$TARGET_SIZE" ]]; then CMD+=(--target-size "$TARGET_SIZE"); fi
if [[ -n "$ROWS" ]];        then CMD+=(--rows "$ROWS"); fi
if [[ -n "$TIME_LIMIT" ]];  then CMD+=(--time "$TIME_LIMIT"); fi

if [[ -z "$LOCAL_DIR" ]]; then
  CMD+=(--config "$CONFIG")
fi

# ---- run ------------------------------------------------------------------

echo "schema      $SCHEMA"
echo "spec        $SPEC"
echo "destination $DEST_LABEL"
echo "limits      ${TARGET_SIZE:+target $TARGET_SIZE }${ROWS:+rows $ROWS }${TIME_LIMIT:+time $TIME_LIMIT }${TARGET_SIZE:-${ROWS:-${TIME_LIMIT:-from config}}}"
echo

if [[ "$DRY_RUN" == "yes" ]]; then
  printf '%q ' "${CMD[@]}"; echo
  exit 0
fi

START=$SECONDS
"${CMD[@]}"
echo "finished in $((SECONDS - START))s"

if [[ -z "$LOCAL_DIR" && "$DEST_TYPE" != "local" ]] && command -v aws >/dev/null 2>&1; then
  echo
  echo "objects now under ${DEST_LABEL}:"
  aws s3 ls "s3://${BUCKET}/${PREFIX}" --human-readable --summarize | tail -20
fi
