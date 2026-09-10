#!/usr/bin/env bash
# Check that Metal accepts a token that Atlas signs. Creates the Atlas signing
# key on the site when it is missing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ATLAS_PYTHON="${ATLAS_PYTHON:-$ROOT/../../env/bin/python}"
ATLAS_SITES="${ATLAS_SITES:-$ROOT/../../sites}"
SITE="${1:-}"

if [ -z "$SITE" ]; then
	echo "Usage: scripts/check-metal-token.sh <site>" >&2
	exit 1
fi

if [ ! -x "$ATLAS_PYTHON" ]; then
	echo "Set ATLAS_PYTHON to a bench environment python that imports frappe and atlas." >&2
	exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "== Atlas issues the token =="
(cd "$ATLAS_SITES" && "$ATLAS_PYTHON" "$ROOT/scripts/check-metal-token.py" "$SITE" "$WORK")

echo
echo "== Metal verifies the token =="
cd "$ROOT/metal"
ATLAS_TRUSTED_KEYS_FILE="$WORK/atlas-jwt.json" ATLAS_TOKEN_FILE="$WORK/token" \
	go test ./internal/token/ -count=1 -run TestAtlasIssuedToken -v
