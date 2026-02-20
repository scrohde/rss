#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

if ! command -v golangci-lint >/dev/null 2>&1; then
	echo "error: golangci-lint is not installed or not in PATH"
	exit 1
fi

GO_BUILD_CACHE="${GO_BUILD_CACHE:-/tmp/go-build-cache}"
LINT_CACHE="${LINT_CACHE:-/tmp/golangci-lint-cache}"

mkdir -p "$GO_BUILD_CACHE" "$LINT_CACHE"
export GOCACHE="$GO_BUILD_CACHE"
export GOLANGCI_LINT_CACHE="$LINT_CACHE"

if [[ "${SKIP_LINT:-false}" != "true" ]]; then
	echo "==> golangci-lint run --fix ./..."
	golangci-lint run --fix ./...
fi

if [[ "${SKIP_TESTS:-false}" != "true" ]]; then
	echo "==> go test ./..."
	go test ./...
fi

echo "check.sh complete"
