# AGENTS

Project: Pulse RSS

## Stack
- Go 1.26
- SQLite (`modernc.org/sqlite`)
- htmx + HTML templates
- CSS (no framework)

## Local dev
```bash
go mod tidy
go run .
```
Open `http://localhost:8080`.

One-command checks:
```bash
./scripts/check.sh
```

## Testing, linting, and formatting
- Max line length: 120 chars.
- Prefer `./scripts/check.sh` for CI-like local checks.
- In sandboxed environments, set writable caches before running lint manually:

```bash
mkdir -p /tmp/go-build-cache /tmp/golangci-lint-cache
export GOCACHE=/tmp/go-build-cache
export GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache

golangci-lint fmt ./...
golangci-lint run --fix ./...
```

Browser smoke tests:
```bash
./scripts/smoke.sh
```

If Chrome/Chromium is not on `PATH`:
```bash
PULSE_SMOKE_BROWSER_BIN="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ./scripts/smoke.sh
```

## Project layout
- `main.go`: thin entrypoint (wiring, config, startup)
- `internal/server/`: routes, handlers, templates, auth/session flows
- `internal/store/`: SQLite init and persistence
- `internal/feed/`: fetch/refresh/scheduling
- `internal/content/`: HTML rewriting, srcset normalization, proxy helpers
- `internal/auth/`: passkey service logic
- `internal/opml/`: OPML import/export
- `internal/view/`: template-facing view models
- `internal/testutil/`: shared test helpers
- `templates/`: full-page and htmx partial templates
- `static/`: JS/CSS/icons/vendor assets

## Conventions
- Prefer server-rendered partials + htmx swaps.
- Add tests in the nearest package to the change (`internal/server`, `internal/store`, `internal/feed`, `internal/content`).
- Keep files ASCII unless non-ASCII already exists.
- Avoid broad refactors unless required for the task.
- Keep `main.go` thin; put behavior in internal packages.

## Issue tracking (`bd`)
- Use `bd` for task tracking instead of markdown TODO lists.
- Basic flow:
  - `bd ready --json`
  - `bd update <id> --claim --json`
  - implement + test
  - `bd close <id> --reason "Done" --json`
- For follow-up work, create linked issues with `--deps discovered-from:<id>`.

Dolt notes:
- This repo uses Dolt-backed `bd`.
- If `bd` health looks broken, run `bd doctor --fix --yes`.
- Check current Dolt connection with `bd dolt show`.

## "Ship it"
Interpret "ship it" as:
1. Commit relevant changes.
2. Run appropriate checks (`./scripts/check.sh`, plus smoke test when UI behavior changed).
3. Push the branch (`git push`).
