#!/usr/bin/env bash
# Preflight: verify config, tools, VeloDB/Doris connectivity, the demo tables and the S3 settings.
#   ./check_env.sh                                      # uses .env / s3.env + environment
#   VELODB_DATABASE=telco_q3_smoke ./check_env.sh       # check another database without editing .env
# Read-only: never creates, loads or deletes anything. Exit code = number of failed checks.
set -uo pipefail
cd "$(dirname "$0")"
set -e; . ./env.sh; require_velodb; set +e

fails=0
ok()   { printf '  \033[32mok\033[0m    %s\n' "$*"; }
warn() { printf '  \033[33mwarn\033[0m  %s\n' "$*"; }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; fails=$((fails+1)); }

echo "config ($( [ -f "$ENV_FILE" ] && echo "$ENV_FILE + environment" || echo "environment only, no $ENV_FILE"))"
echo "  VELODB_HOST=$VELODB_HOST  VELODB_MYSQL_PORT=$VELODB_MYSQL_PORT  VELODB_DATABASE=$VELODB_DATABASE"
echo "  VELODB_USER=$VELODB_USER  VELODB_PASSWORD=$( [ -n "$VELODB_PASSWORD" ] && echo '(set)' || echo '(empty)')"
case "$VELODB_HOST$VELODB_PASSWORD" in *example.com*|*change-me*) bad "placeholder values from env.example are still in use";; esac

echo "tools"
for t in mysql go cargo rs3; do
  if command -v "$t" >/dev/null; then ok "$t  $( { "$t" --version 2>/dev/null || "$t" version; } | head -1)"
  else case "$t" in mysql) bad "mysql client not found (run-load.sh, this check)";;
                    go)    bad "go not found (run-billing-app.sh)";;
                    *)     warn "$t not found (needed only for generate.sh / run-load.sh)";; esac; fi
done
(( BASH_VERSINFO[0] >= 4 )) && ok "bash $BASH_VERSION" || warn "bash $BASH_VERSION: run-load.sh needs bash 4+"

echo "VeloDB MySQL protocol ${VELODB_HOST}:${VELODB_MYSQL_PORT}"
export MYSQL_PWD="$VELODB_PASSWORD"
sql() { mysql -h"$VELODB_HOST" -P"$VELODB_MYSQL_PORT" -u"$VELODB_USER" --connect-timeout=10 --batch --skip-column-names -e "$1" 2>&1; }
if out=$(sql "SELECT concat(version(), ' / ', current_user())"); then ok "connected: $out"
else bad "cannot connect: $(echo "$out" | tail -1)"; fi

echo "demo database ${VELODB_DATABASE}"
for t in call_log data_stream_log rate_plan cdr_duplicate_correction subscriber_bill pipeline_run; do
  rows=$(sql "SELECT table_rows FROM information_schema.tables WHERE table_schema='${VELODB_DATABASE}' AND table_name='$t'")
  if [ -n "$rows" ]; then ok "$t: $rows rows"
  else case "$t" in call_log|data_stream_log|rate_plan) warn "$t missing: run ./run-load.sh START_MONTH --create";;
                    *) echo "  -     $t missing (the billing app creates it on start)";; esac; fi
done

echo "S3 (generate.sh / run-load.sh only)"
if out=$(load_s3_env 2>&1); then
  load_s3_env; ok "s3://$S3_BUCKET/$S3_PREFIX/ in $S3_REGION ($( [ -f "$S3_ENV_FILE" ] && echo "$S3_ENV_FILE + environment" || echo "environment only"))"
  case "$S3_BUCKET" in *'<'*) bad "placeholder values from s3.env.example are still in use";; esac
else warn "${out#error: } (only needed to generate or load data)"; fi

echo; [ "$fails" = 0 ] && echo "all checks passed" || echo "$fails check(s) failed"
exit "$fails"
