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

Shortcut:
```bash
./scripts/check.sh
```

## Testing, Linting, and formatting
Lines can be up to 120 chars long.

In sandboxed runs (like Codex), `golangci-lint` may fail to read/write default caches under `~/Library/Caches`.
Use `./scripts/check.sh` to handle this automatically. If running commands manually, set writable cache dirs first:

```bash
mkdir -p /tmp/go-build-cache /tmp/golangci-lint-cache
export GOCACHE=/tmp/go-build-cache
export GOLANGCI_LINT_CACHE=/tmp/golangci-lint-cache

# Format (uses formatters section of .golangci.yml)
golangci-lint fmt ./...

# Lint (uses linters section of .golangci.yml)
golangci-lint run --fix ./...
```

Browser smoke tests:

```bash
./scripts/smoke.sh
```

If Chrome/Chromium is not on `PATH`, set `PULSE_SMOKE_BROWSER_BIN`:

```bash
PULSE_SMOKE_BROWSER_BIN="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ./scripts/smoke.sh
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
- `internal/server/browser_smoke_test.go` browser smoke test suite (build tag: `smoke`)
- `internal/content/*_test.go` HTML rewrite, srcset, and proxy policy tests
- `internal/feed/*.go` refresh + scheduling tests
- `internal/store/store_test.go` and `internal/store/auth_test.go` DB/store tests
- `internal/auth/service_test.go` passkey service tests
- `internal/opml/opml_test.go` OPML parsing tests

## Conventions
- Use 'bd' for task tracking.
- Keep Go Linting and formatting as described
- Prefer server-rendered partials + htmx swaps.
- Add tests in the package closest to the change (`internal/server`, `internal/store`, `internal/feed`, `internal/content`).
- Avoid non-ASCII text in files unless already present.
- Interpret "ship it" as: commit the relevant changes and run `git push`.

<!-- BEGIN BEADS INTEGRATION -->
## Issue Tracking with bd (beads)

**IMPORTANT**: This project uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs, task lists, or other tracking methods.

### Why bd?

- Dependency-aware: Track blockers and relationships between issues
- Git-friendly: Auto-syncs to JSONL for version control
- Agent-optimized: JSON output, ready work detection, discovered-from links
- Prevents duplicate tracking systems and confusion

### Quick Start

**Check for ready work:**

```bash
bd ready --json
```

**Create new issues:**

```bash
bd create "Issue title" --description="Detailed context" -t bug|feature|task -p 0-4 --json
bd create "Issue title" --description="What this issue is about" -p 1 --deps discovered-from:bd-123 --json
```

**Claim and update:**

```bash
bd update bd-42 --status in_progress --json
bd update bd-42 --priority 1 --json
```

**Complete work:**

```bash
bd close bd-42 --reason "Completed" --json
```

### Issue Types

- `bug` - Something broken
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature with subtasks
- `chore` - Maintenance (dependencies, tooling)

### Priorities

- `0` - Critical (security, data loss, broken builds)
- `1` - High (major features, important bugs)
- `2` - Medium (default, nice-to-have)
- `3` - Low (polish, optimization)
- `4` - Backlog (future ideas)

### Workflow for AI Agents

1. **Check ready work**: `bd ready` shows unblocked issues
2. **Claim your task**: `bd update <id> --status in_progress`
3. **Work on it**: Implement, test, document
4. **Discover new work?** Create linked issue:
   - `bd create "Found bug" --description="Details about what was found" -p 1 --deps discovered-from:<parent-id>`
5. **Complete**: `bd close <id> --reason "Done"`

### Auto-Sync

bd automatically syncs with git:

- Exports to `.beads/issues.jsonl` after changes (5s debounce)
- Imports from JSONL when newer (e.g., after `git pull`)
- No manual export/import needed!

### Important Rules

- ✅ Use bd for ALL task tracking
- ✅ Always use `--json` flag for programmatic use
- ✅ Link discovered work with `discovered-from` dependencies
- ✅ Check `bd ready` before asking "what should I work on?"
- ❌ Do NOT create markdown TODO lists
- ❌ Do NOT use external issue trackers
- ❌ Do NOT duplicate tracking systems

For more details, see README.md and docs/QUICKSTART.md.

<!-- END BEADS INTEGRATION -->

## Landing the Plane (Session Completion)

**When ending a work session**, complete the steps below.

**WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Hand off** - Provide context for next session
