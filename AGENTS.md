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

## Linting and formatting
Lines can be up to 120 chars long.

```bash
# Rewrite code to newer/cleaner Go idioms when safe
go fix ./...

# Format (uses formatters section of .golangci.yml)
golangci-lint fmt ./...

# Lint (uses linters section of .golangci.yml)
golangci-lint run ./...

# Apply autofixes (lint fixes + formatting fixes)
golangci-lint run --fix ./...
```

## Project layout
- `main.go` thin entrypoint (logging, wiring, server startup)
- `internal/server/` HTTP routes, handlers, template rendering, background loops
- `internal/store/` SQLite open/init and data access logic
- `internal/feed/` feed fetch/refresh and refresh scheduling
- `internal/content/` summary HTML rewriting and image proxy helpers
- `internal/view/` template-facing view models and formatting builders
- `internal/testutil/` shared test helpers
- `templates/` HTML templates and htmx partials
- `static/` frontend assets
- `internal/server/handlers_test.go` integration-style handler tests
- `internal/content/rewrite_test.go` HTML rewrite tests
- `internal/feed/*.go` refresh + scheduling tests
- `internal/store/store_test.go` DB/store tests

## Conventions
- Keep Go Linting and formatting as described
- Prefer server-rendered partials + htmx swaps.
- Add tests in the package closest to the change (`internal/server`, `internal/store`, `internal/feed`, `internal/content`).
- Avoid non-ASCII text in files unless already present.
