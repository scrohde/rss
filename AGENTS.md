# AGENTS

Project: Pulse RSS

## Stack
- Go 1.26
- SQLite (modernc.org/sqlite)
- htmx + HTML templates
- CSS (no framework)

## Local dev
```bash
go mod tidy
go run .
```
Open http://localhost:8080

## Tests
```bash
go test ./...
```

Shortcut:
```bash
./scripts/check.sh
```

## Linting and formatting
Lines can be up to 120 chars long.

In sandboxed runs (like Codex), `golangci-lint` may fail to read/write default caches under `~/Library/Caches`.
Use `./scripts/check.sh` to handle this automatically. If running commands manually, set writable cache dirs first:

```bash
mkdir -p /tmp/go-build-cache /tmp/golangci-lint-cache
export GOCACHE=/tmp/go-build-cache
export GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache

# Rewrite code to newer/cleaner Go idioms when safe
go fix ./...

# Format (uses formatters section of .golangci.yml)
golangci-lint fmt ./...

# Lint (uses linters section of .golangci.yml)
golangci-lint run --fix ./...
```

## Project layout
- `main.go` thin entrypoint (logging, wiring, config/env parsing, server startup)
- `internal/server/` HTTP routes, handlers, template rendering, auth/session flows, background loops
- `internal/store/` SQLite open/init and data access for feeds/items and auth state
- `internal/feed/` feed fetch/refresh and refresh scheduling
- `internal/content/` summary HTML rewriting, srcset normalization, and image proxy helpers
- `internal/auth/` passkey registration/authentication service logic
- `internal/opml/` OPML import/export parsing and rendering helpers
- `internal/view/` template-facing view models and formatting builders
- `internal/testutil/` shared test helpers
- `templates/` HTML templates and htmx partials (including auth screens)
- `static/` frontend assets (`app.js`, `auth.js`, CSS, icons, vendor JS)
- `internal/server/handlers_test.go` and `internal/server/auth_handlers_test.go` integration-style handler tests
- `internal/content/*_test.go` HTML rewrite, srcset, and proxy policy tests
- `internal/feed/*.go` refresh + scheduling tests
- `internal/store/store_test.go` and `internal/store/auth_test.go` DB/store tests
- `internal/auth/service_test.go` passkey service tests
- `internal/opml/opml_test.go` OPML parsing tests

## Conventions
- Keep Go Linting and formatting as described
- Prefer server-rendered partials + htmx swaps.
- Add tests in the package closest to the change (`internal/server`, `internal/store`, `internal/feed`, `internal/content`).
- Avoid non-ASCII text in files unless already present.
