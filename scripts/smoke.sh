#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

echo "==> go test -tags=smoke -count=1 ./internal/server -run TestBrowserSmokeReaderFlows"
go test -tags=smoke -count=1 ./internal/server -run TestBrowserSmokeReaderFlows "$@"
