# Shared config loader: sourced (not executed) by the Doris-facing scripts (run-load.sh, run-web-server.sh,
# run-search.sh, check_env.sh). run-embedding.sh / run-export.sh are offline and don't need it.
#
# Precedence: variables already set in the environment > $ENV_FILE (default ./.env) > defaults below.
# So `VELODB_DATABASE=other ./run-load.sh` overrides .env for one run without editing any file.
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
export VELODB_DATABASE="${VELODB_DATABASE:-face_search_demo}"
export VELODB_FACES_BUCKETS="${VELODB_FACES_BUCKETS:-16}"
export VELODB_PEOPLE_BUCKETS="${VELODB_PEOPLE_BUCKETS:-4}"
export VELODB_REPLICATION_NUM="${VELODB_REPLICATION_NUM:-1}"

# short name used inside the scripts
DATABASE="$VELODB_DATABASE"
