# Shared config loader: sourced (not executed) by every run-*.sh script in this directory.
#
# Precedence: variables already set in the environment > $ENV_FILE (default ./.env) > defaults below.
# So `VELODB_DATABASE=other ./run-load.sh` overrides .env for one run without editing any file.
# Also sets PYTHON to the project venv's interpreter, creating ./venv first if needed (ensure-venv.sh).
# See env.example for the variables.

_ENV_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$_ENV_DIR/.env}"
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

export VELODB_PASSWORD="${VELODB_PASSWORD:-}"
export VELODB_MYSQL_PORT="${VELODB_MYSQL_PORT:-9030}"
export VELODB_STREAM_LOAD_PORT="${VELODB_STREAM_LOAD_PORT:-8080}"
export VELODB_DATABASE="${VELODB_DATABASE:-hybrid_search_demo}"

# require_velodb: the scripts that talk to the cluster call this (embedding, export and local search do not)
require_velodb() {
  : "${VELODB_HOST:?is not set - copy env.example to .env or export it (see README: Configure)}"
  : "${VELODB_USER:?is not set - copy env.example to .env or export it (see README: Configure)}"
  export VELODB_HOST VELODB_USER
}

PYTHON="$("$_ENV_DIR/ensure-venv.sh")"
