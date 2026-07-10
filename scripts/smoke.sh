#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

GO_BUILD_CACHE="${GO_BUILD_CACHE:-/tmp/go-build-cache}"

mkdir -p "$GO_BUILD_CACHE"
export GOCACHE="$GO_BUILD_CACHE"

SMOKE_PATTERN='TestBrowserSmoke(AuthLoginSwitchesFromConditionalToExplicit|AuthLoginUnsupportedFallback|InactiveFeedContentBoundary|ReaderFlows|PulseIndicatorFlows)'

echo "==> go test -tags=smoke -count=1 ./internal/server -run ${SMOKE_PATTERN}"
go test -tags=smoke -count=1 ./internal/server -run "${SMOKE_PATTERN}" "$@"
