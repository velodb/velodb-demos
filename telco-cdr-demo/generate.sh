#!/usr/bin/env bash
# Generate the raw Parquet for one or both tables and stream it to S3 (s3.env: bucket, prefix, endpoint).
# Nothing touches local disk: generator threads feed upload workers that stream multipart uploads.
#
#   nohup ./generate.sh all > generate.log 2>&1 &   # the full 3 months: 2.25B call_log + 9B data_stream_log rows
#   ./generate.sh call_log --rows 4500000           # extra args go to doris-parquet-s3-gen (--rows, --target-size, --time)
#   RUN_ID=smoke ./generate.sh all --rows 1000000   # write into another run folder
#
# Wipe first: `all` deletes everything under s3://<bucket>/<prefix>/ before generating; a single table deletes
# s3://<bucket>/<prefix>/<table>/. Objects are removed and unfinished multipart uploads (left by an interrupted run)
# are aborted, so do not run two generations against the same prefix at once.
# Tables already loaded into Doris are not affected.
#
# The generator is vendored in tools/ and built on first use (needs cargo; see tools/README.md for build packages).
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
load_s3_env
export AWS_ACCESS_KEY_ID="$S3_AK" AWS_SECRET_ACCESS_KEY="$S3_SK"
GEN_DIR="tools/doris-parquet-s3-gen"
BIN="$GEN_DIR/target/release/doris-parquet-s3-gen"
CALCSIZE="$GEN_DIR/target/release/calcsize"
[[ -x "$BIN" && -x "$CALCSIZE" ]] || (cd "$GEN_DIR" && cargo build --release)
. ./s3-alias.sh

TABLES="${1:?usage: ./generate.sh call_log|data_stream_log|all [generator args]}"; shift
case "$TABLES" in
  all) TABLES="call_log data_stream_log"; WIPE="$S3_PREFIX/" ;;
  call_log|data_stream_log) WIPE="$S3_PREFIX/$TABLES/" ;;
  *) echo "unknown table $TABLES" >&2; exit 1 ;;
esac
EXTRA=("$@")
[[ -t 1 ]] || EXTRA+=(--no-progress)   # under nohup / redirected output: plain log lines, no live display
log() { echo "$(date '+%F %T') $*"; }

# render_config table: gen/dest-<table>.toml with the destination filled in from s3.env
render_config() {
  local t=$1 cfg
  cfg="$(mktemp "${TMPDIR:-/tmp}/dest-$t.XXXXXX")"
  sed -E \
    -e "s|^bucket = .*|bucket = \"$S3_BUCKET\"|" \
    -e "s|^prefix = .*|prefix = \"$S3_PREFIX/$t/\"|" \
    -e "s|^region = .*|region = \"$S3_REGION\"|" \
    -e "s|^endpoint = .*|endpoint = \"$S3_ENDPOINT\"|" \
    -e "s|^path_style = .*|path_style = $S3_PATH_STYLE|" \
    ${RUN_ID:+-e "s|^run_id = \"[^\"]*\"|run_id = \"$RUN_ID\"|"} \
    "gen/dest-$t.toml" > "$cfg"
  echo "$cfg"
}

# ---- wipe ---------------------------------------------------------------------------------------
log "wiping s3://$S3_BUCKET/$WIPE"
target="$S3_RS3_ALIAS/$S3_BUCKET/$WIPE"
listing=$(rs3 ls "$target")   # an error aborts the script (set -e) rather than looking like an empty prefix
if [[ -n "$listing" ]]; then
  log "  removing objects: $(rs3 du "$target" | tail -1)"
  rs3 rm --recursive --force "$target" > /dev/null
fi
wipe_cfg=$(render_config call_log)
# calcsize does not read AWS_* from the environment, only [s3.credentials] (the temp file is mode 600, removed below)
printf '\n[s3.credentials]\naccess_key_id = "%s"\nsecret_access_key = "%s"\n' "$S3_AK" "$S3_SK" >> "$wipe_cfg"
report=$("$CALCSIZE" -c "$wipe_cfg" -p "$WIPE" --all --force-abort 2>&1) || { echo "$report" >&2; rm -f "$wipe_cfg"; exit 1; }
rm -f "$wipe_cfg"
echo "$report" | sed "s/^/$(date '+%F %T')   /"
remaining=$(echo "$report" | awk '$1 == "objects" {print $2}' | tr -d ,)
if [[ "$remaining" != "0" ]]; then
  log "error: s3://$S3_BUCKET/$WIPE still holds ${remaining:-an unknown number of} objects after the wipe" >&2; exit 1
fi
log "  prefix is empty"

# ---- generate -----------------------------------------------------------------------------------
for t in $TABLES; do
  cfg=$(render_config "$t")
  run=$(sed -nE 's/^run_id = "([^"]*)".*/\1/p' "$cfg")
  log "=== $t -> s3://$S3_BUCKET/$S3_PREFIX/$t/$run/"
  "$BIN" --schema "gen/${t}_raw.sql" --spec "gen/$t.spec" --config "$cfg" "${EXTRA[@]}"
  rm -f "$cfg"
  log "=== $t done"
done
log "objects now under s3://$S3_BUCKET/$S3_PREFIX/: $(rs3 du "$S3_RS3_ALIAS/$S3_BUCKET/$S3_PREFIX/" | tail -1)"
