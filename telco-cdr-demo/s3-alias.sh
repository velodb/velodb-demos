# Sourced by generate.sh and run-load.sh after s3.env: (re)create the rs3 alias for the S3 target, including the
# region. `rs3 alias set` has no region argument and signs for us-east-1 without one, which AWS rejects outside that
# region, so the region is written into the alias config (~/.mc/config.json) directly. Then fail fast if the bucket
# cannot be listed, instead of treating an error as an empty listing.
rs3 alias set "$S3_RS3_ALIAS" "$S3_ENDPOINT" "$S3_AK" "$S3_SK" S3v4 "$([[ $S3_PATH_STYLE == true ]] && echo on || echo off)" > /dev/null
python3 - "$S3_RS3_ALIAS" "$S3_REGION" <<'PY'
import json, os, sys
alias, region = sys.argv[1], sys.argv[2]
path = os.path.expanduser("~/.mc/config.json")
with open(path) as f:
    config = json.load(f)
config["aliases"][alias]["region"] = region
tmp = path + ".tmp"
with open(os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
    json.dump(config, f, indent=2)
os.replace(tmp, path)
PY
if ! rs3 ls "$S3_RS3_ALIAS/$S3_BUCKET/" > /dev/null; then
  echo "error: cannot list s3://$S3_BUCKET with rs3 alias $S3_RS3_ALIAS (region $S3_REGION)" >&2
  exit 1
fi
