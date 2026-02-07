# Pulse RSS

A compact RSS reader built with Go, htmx, and SQLite.

## Features
- Subscribe to feeds by URL
- Sidebar feed list with item counts
- Click a feed to view items
- Expand an item to read the summary; close to collapse
- Item title opens in a new tab
- Mark items read/unread
- Keep at most 200 items per feed (oldest auto-deleted)
- Auto-delete read items after 30 minutes
- Non-disruptive polling with a "New items (N)" banner

## Run
```bash
go mod tidy
go run .
```

Then open http://localhost:8080.

## Tests
```bash
go test ./...
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
