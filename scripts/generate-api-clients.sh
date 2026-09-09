#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GENERATE="$ROOT/scripts/generate-api-client.py"
GENERATOR_VERSION="0.29.1"
RUFF_VERSION="0.16.6"
ATLAS_PYTHON="${ATLAS_PYTHON:-$ROOT/../../env/bin/python}"

if [ ! -x "$ATLAS_PYTHON" ]; then
	echo "Set ATLAS_PYTHON to a bench environment python that imports frappe and atlas." >&2
	exit 1
fi

if ! command -v openapi-python-client >/dev/null; then
	echo "Install openapi-python-client==$GENERATOR_VERSION and ruff==$RUFF_VERSION." >&2
	exit 1
fi

if ! command -v ruff >/dev/null; then
	echo "Install ruff==$RUFF_VERSION. The generator formats its output with it." >&2
	exit 1
fi

if ! command -v uv >/dev/null; then
	echo "Install uv. It supplies the environment of the HTTP proxy control daemon." >&2
	exit 1
fi

"$ATLAS_PYTHON" "$GENERATE" atlas
uv run --no-project --with "$ROOT/services/http-proxy/control" python "$GENERATE" atlas-http-proxy
