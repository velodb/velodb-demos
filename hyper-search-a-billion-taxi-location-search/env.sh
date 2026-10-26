# Shared config loader: sourced (not executed) by every *.sh script in this directory.
#
# Precedence: variables already set in the environment > $ENV_FILE (default ./.env) > defaults below.
# So `VELODB_DATABASE=other ./create_table_doris.sh` overrides .env for one run without editing any file.
# See env.example for the variables.

ENV_FILE="${ENV_FILE:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.env}"
if [ -f "$ENV_FILE" ]; then
  # remember the keys of the file that are already set, source the file, then put them back
  _restore=""
  for _k in $(sed -nE 's/^[[:space:]]*(export[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*)=.*/\2/p' "$ENV_FILE"); do
    if [ -n "${!_k+x}" ]; then _restore+="export $_k=$(printf %q "${!_k}");"; fi
  done
  set -a; . "$ENV_FILE"; set +a
  eval "$_restore"
  unset _restore _k
fi
export ENV_FILE

: "${VELODB_HOST:?is not set - copy env.example to .env or export it (see README: Configure)}"
: "${VELODB_USER:?is not set - copy env.example to .env or export it (see README: Configure)}"
export VELODB_HOST VELODB_USER VELODB_PASSWORD="${VELODB_PASSWORD:-}"
export VELODB_MYSQL_PORT="${VELODB_MYSQL_PORT:-9030}"
export VELODB_STREAM_LOAD_PORT="${VELODB_STREAM_LOAD_PORT:-8080}"
export VELODB_STREAM_LOAD_URL="${VELODB_STREAM_LOAD_URL:-http://${VELODB_HOST}:${VELODB_STREAM_LOAD_PORT}}"
export VELODB_DATABASE="${VELODB_DATABASE:-demo_hyper_search}"
export VELODB_TABLE="${VELODB_TABLE:-taxi_ping}"
export VELODB_BUCKETS="${VELODB_BUCKETS:-32}"
export VELODB_REPLICATION_NUM="${VELODB_REPLICATION_NUM:-1}"
export VELODB_PARTITION_KEEP_DAYS="${VELODB_PARTITION_KEEP_DAYS:-497}"
export VELODB_PARTITION_AHEAD_DAYS="${VELODB_PARTITION_AHEAD_DAYS:-0}"

# short names used inside the scripts
DATABASE="$VELODB_DATABASE"; TABLE="$VELODB_TABLE"
# Broker Load BASE marker, one per table (load_into_doris.sh writes it, truncate_doris_table.sh removes it)
MARKER="${LOAD_BASE_FILE:-load_base_${DATABASE}.${TABLE}.txt}"
