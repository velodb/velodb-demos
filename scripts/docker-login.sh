#!/usr/bin/env bash
# Log in to Docker Hub using a Personal Access Token without leaking it.
# The token is read from (in priority order):
#   1) $DOCKERHUB_TOKEN env var
#   2) ~/.docker/velodb-pat   (chmod 600 file with the token on a single line)
#
# Username defaults to `velodb`. Override with $DOCKERHUB_USER.
set -euo pipefail

USER="${DOCKERHUB_USER:-velodb}"
TOKEN="${DOCKERHUB_TOKEN:-}"

if [[ -z "$TOKEN" && -f "$HOME/.docker/velodb-pat" ]]; then
  TOKEN="$(cat "$HOME/.docker/velodb-pat")"
fi

if [[ -z "$TOKEN" ]]; then
  echo "[docker-login] no token found." >&2
  echo "  Either:  export DOCKERHUB_TOKEN=dckr_pat_..." >&2
  echo "  Or write the token to ~/.docker/velodb-pat (chmod 600)." >&2
  exit 1
fi

echo "$TOKEN" | docker login --username "$USER" --password-stdin
