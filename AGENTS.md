# AGENTS

Project: Pulse RSS

## Stack
- Go 1.22
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

## Project layout
- `main.go` server, routes, DB, feed refresh
- `templates/` HTML templates and htmx partials
- `static/styles.css` UI styles
- `main_test.go` unit tests (feed simulation + polling)

## Conventions
- Keep Go formatting via `gofmt`.
- Prefer server-rendered partials + htmx swaps.
- For new features, add tests under `main_test.go`.
- Avoid non-ASCII text in files unless already present.
