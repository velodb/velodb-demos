#!/usr/bin/env bash
# Build and start the billing pipeline web app against the VELODB_* cluster (environment or .env, see env.example).
#
#   ./run-billing-app.sh                                # database telco_demo, http://localhost:8088
#   VELODB_DATABASE=telco_app_dev ./run-billing-app.sh  # another database with call_log / data_stream_log / rate_plan
#   LISTEN=:9000 PARALLEL=3 ./run-billing-app.sh
set -euo pipefail
cd "$(dirname "$0")"
. ./env.sh
require_velodb
cd billing-app
go build -o telco-billing .
exec ./telco-billing
