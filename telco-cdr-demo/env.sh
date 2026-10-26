# Shared config loader: sourced (not executed) by every *.sh script in this directory.
#
# Precedence: variables already set in the environment > $ENV_FILE (default ./.env) > defaults below.
# So `VELODB_DATABASE=other ./run-billing-app.sh` overrides .env for one run without editing any file.
# S3 settings are loaded the same way from $S3_ENV_FILE (default ./s3.env) by load_s3_env, for the scripts that need S3.
# See env.example and s3.env.example for the variables.

_ENV_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# load_env_file FILE: export the file's variables, except those already set in the environment
load_env_file() {
  [ -f "$1" ] || return 0
  # remember the keys of the file that are already set, source the file, then put them back
  local _restore="" _k
  for _k in $(sed -nE 's/^[[:space:]]*(export[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*)=.*/\2/p' "$1"); do
    if [ -n "${!_k+x}" ]; then _restore+="export $_k=$(printf %q "${!_k}");"; fi
  done
  set -a; . "$1"; set +a
  eval "$_restore"
}

ENV_FILE="${ENV_FILE:-$_ENV_DIR/.env}"
load_env_file "$ENV_FILE"
export ENV_FILE

export VELODB_PASSWORD="${VELODB_PASSWORD:-}"
export VELODB_MYSQL_PORT="${VELODB_MYSQL_PORT:-9030}"
export VELODB_DATABASE="${VELODB_DATABASE:-telco_demo}"

# require_velodb: the scripts that talk to the cluster call this (generate.sh only needs S3)
require_velodb() {
  : "${VELODB_HOST:?is not set - copy env.example to .env or export it (see README: Configure)}"
  : "${VELODB_USER:?is not set - copy env.example to .env or export it (see README: Configure)}"
  export VELODB_HOST VELODB_USER
}

# load_s3_env: S3 target for generate.sh / run-load.sh
load_s3_env() {
  S3_ENV_FILE="${S3_ENV_FILE:-$_ENV_DIR/s3.env}"
  load_env_file "$S3_ENV_FILE"
  export S3_ENV_FILE
  export S3_AK="${S3_AK:-${AWS_ACCESS_KEY_ID:-}}" S3_SK="${S3_SK:-${AWS_SECRET_ACCESS_KEY:-}}"
  export S3_PATH_STYLE="${S3_PATH_STYLE:-false}" S3_RS3_ALIAS="${S3_RS3_ALIAS:-telco-cdr-s3}"
  local _k
  for _k in S3_ENDPOINT S3_REGION S3_AK S3_SK S3_BUCKET S3_PREFIX; do
    [ -n "${!_k:-}" ] || { echo "error: $_k is not set - copy s3.env.example to s3.env or export it (see README: Configure)" >&2; return 1; }
  done
}
