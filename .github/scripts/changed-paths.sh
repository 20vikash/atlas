#!/usr/bin/env bash
# Usage: changed-paths.sh <extended regular expression>
# Writes matched=true or matched=false to GITHUB_OUTPUT.
set -euo pipefail

pattern="$1"

if [ "${GITHUB_EVENT_NAME:-}" != "pull_request" ]; then
	echo "matched=true" >>"$GITHUB_OUTPUT"
	echo "Not a pull request. Running everything."
	exit 0
fi

base="$(git merge-base "origin/$GITHUB_BASE_REF" HEAD)"
changed="$(git diff --name-only "$base" HEAD)"

if printf '%s\n' "$changed" | grep -Eq "$pattern"; then
	echo "matched=true" >>"$GITHUB_OUTPUT"
	echo "Matched $pattern"
else
	echo "matched=false" >>"$GITHUB_OUTPUT"
	echo "No change matched $pattern. Nothing to run."
fi
