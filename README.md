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

## Requirements

- Go 1.26.5 or newer. This minimum includes required standard-library security fixes and is enforced by `go.mod`.

## Run
```bash
go mod tidy
AUTH_ENABLED=false go run .
```

Then open http://localhost:8080.

Optional environment variables:
- `LOG_LEVEL` controls structured log verbosity (`debug`, `info`, `warn`, `error`; default `info`).
- `DB_PATH` sets the SQLite database file path (default `rss.db` in the process working directory).

## Run as a public service
Production templates in this repo:
- [`Caddyfile.example`](./Caddyfile.example) (hardened TLS reverse-proxy config)
- [`deploy/systemd/pulse-rss.service`](./deploy/systemd/pulse-rss.service)
- [`deploy/systemd/pulse-rss.env.example`](./deploy/systemd/pulse-rss.env.example)

### Linux production setup (systemd + Caddy)

Quick deploy helper:
```bash
./scripts/deploy-linux.sh
```

When `BUILD_BINARY=true`, the deployment host must provide Go 1.26.5 or newer.

`scripts/deploy-linux.sh` defaults to:
- optional `sudo` escalation when run as a non-root user
- building the binary from repo source (`BUILD_BINARY=true`)
- installing `/usr/local/bin/pulse-rss`
- installing systemd unit/env templates (if needed)
- reloading Caddy config (with placeholder-domain safety check)
- restarting one service: `pulse-rss.service`

For multi-instance services, set `SERVICES`:
```bash
SERVICES='pulse-rss@instance1.service pulse-rss@instance2.service' ./scripts/deploy-linux.sh
```

When `SERVICES` includes instance units (`@...`), the script automatically:
- installs the unit as `/etc/systemd/system/pulse-rss@.service`
- ensures per-instance data dirs under `/var/lib/pulse-rss/<instance>`
- seeds missing env files at `/etc/pulse-rss/<instance>.env`
- validates that effective `PORT` values are unique across restarted instance units
- validates that effective `DB_PATH` values stay inside each instance directory
- validates that effective `AUTH_RP_ID` is not `rss.example.com`
- fails if `pulse-rss.service` is still active/enabled (unless explicitly allowed)

Useful overrides:
- `SERVICES='pulse-rss.service'` to pick explicit unit names (space or comma separated).
- `GIT_PULL=true` to run `git pull --ff-only` before build.
- `RUN_CHECKS=true` to run `./scripts/check.sh` before install/restart.
- `BUILD_BINARY=false` to skip `go build` and use an existing `BIN_SRC`.
- `ENABLE_SERVICES=false` to skip `systemctl enable` and only restart units.
- `VALIDATE_INSTANCE_PORTS=false` to skip duplicate-port safety checks.
- `VALIDATE_INSTANCE_DB_PATH=false` to skip instance DB path safety checks.
- `VALIDATE_RP_ID_PLACEHOLDER=false` to skip placeholder RP ID safety checks.
- `VALIDATE_AUTH_SETUP_TOKEN=false` to skip setup-token safety checks.
- `ALLOW_BASE_SERVICE_WITH_INSTANCES=true` to allow running base and instance units together.
- `APPLY_CADDY=false` to skip installing/reloading Caddy.
- `BIN_SRC=/path/to/rss` to deploy a different binary path.
- `CADDY_SRC=/path/to/Caddyfile` to deploy a different Caddy config file.
- `CADDY_ALLOW_PLACEHOLDER=true` to bypass placeholder-domain safety check.

Pulse RSS should remain bound to loopback (`127.0.0.1:8080`) behind Caddy.
By default, authentication trusts forwarded client addresses only from loopback proxies. Set
`AUTH_TRUSTED_PROXY_CIDRS` to a comma-separated CIDR list if Caddy connects from another network.

### Authentication (Passkeys)

Pulse RSS can run with passkey-only authentication for public hosting.

Set these env vars before `go run .` in production:

```bash
# Run this once, then paste its output into AUTH_SETUP_TOKEN below:
openssl rand -base64 32

AUTH_ENABLED=true
AUTH_RP_ID=rss.example.com
AUTH_RP_ORIGIN=https://rss.example.com
AUTH_RP_NAME="Pulse RSS"
AUTH_SETUP_TOKEN="<paste-generated-value-here>"
AUTH_SESSION_TTL=24h
AUTH_CHALLENGE_TTL=5m
AUTH_COOKIE_NAME=pulse_rss_session
AUTH_COOKIE_SECURE=true
```

Notes:
- `AUTH_SETUP_TOKEN` is required for initial enrollment. Generate it with `openssl rand -base64 32` and
  store the output in the environment file. It must be at least 43 characters; length alone does not make a
  human-chosen value safe.
- `AUTH_RP_ORIGIN` must exactly match the public HTTPS origin.
- Passkeys do not work reliably on raw public IP addresses.
- If unset, secure defaults are applied: `AUTH_SESSION_TTL=24h`, `AUTH_CHALLENGE_TTL=5m`, and secure cookies.

## Run as a local service (macOS)

### 1. Build and install RSS on the host Mac

```bash
mkdir -p "$HOME/pulse-rss"
go build -o "$HOME/pulse-rss/rss" .
```

Note: RSS binds to `127.0.0.1` by default.
The built binary embeds `templates/` and `static/`, so it can run from any directory without copying asset files.

### 2. Create a user LaunchAgent (auto-start on login)

```bash
cat > "$HOME/Library/LaunchAgents/com.pulse-rss.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.pulse-rss</string>

  <key>ProgramArguments</key>
  <array>
    <string>$HOME/pulse-rss/rss</string>
  </array>

  <key>WorkingDirectory</key>
  <string>$HOME/pulse-rss</string>

  <key>EnvironmentVariables</key>
  <dict>
    <key>PORT</key>
    <string>8080</string>
    <key>LOG_LEVEL</key>
    <string>info</string>
    <key>AUTH_ENABLED</key>
    <string>false</string>
  </dict>

  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>

  <key>StandardOutPath</key>
  <string>$HOME/pulse-rss/rss.out.log</string>
  <key>StandardErrorPath</key>
  <string>$HOME/pulse-rss/rss.err.log</string>
</dict>
</plist>
EOF
```

Load and start it:

```bash
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.pulse-rss.plist" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.pulse-rss.plist"
launchctl enable "gui/$(id -u)/com.pulse-rss"
launchctl kickstart -k "gui/$(id -u)/com.pulse-rss"
```

### 3. Verify the service

```bash
curl -I http://127.0.0.1:8080
open http://127.0.0.1:8080
```

### Operations

Update binary after code changes:

```bash
go build -o "$HOME/pulse-rss/rss" .
launchctl kickstart -k "gui/$(id -u)/com.pulse-rss"
```

Check logs:

```bash
tail -f "$HOME/pulse-rss/rss.out.log" "$HOME/pulse-rss/rss.err.log"
```

Disable service:

```bash
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.pulse-rss.plist"
```

## Tests
```bash
go test ./...
```

Browser smoke tests (headless Chrome/Chromium required):
```bash
./scripts/smoke.sh
```

If Chrome/Chromium is not on `PATH`, point the smoke suite to a binary:
```bash
PULSE_SMOKE_BROWSER_BIN="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ./scripts/smoke.sh
```

All-in-one dev check (lint autofix + tests):
```bash
./scripts/check.sh
```

Optional overrides:
- `SKIP_LINT=true ./scripts/check.sh`
- `SKIP_TESTS=true ./scripts/check.sh`

## Project layout
- `main.go` thin entrypoint (logging, wiring, config/env parsing, server startup)
- `internal/server/` HTTP routes, handlers, template rendering, auth/session flows, background loops
- `internal/store/` SQLite open/init and data access for feeds/items and auth state
- `internal/feed/` feed fetch/refresh and refresh scheduling
- `internal/content/` summary HTML rewriting, srcset normalization, and image proxy helpers
- `internal/outbound/` shared outbound URL policy and destination-pinned HTTP transport
- `internal/auth/` passkey registration/authentication service logic
- `internal/opml/` OPML import/export parsing and rendering helpers
- `internal/view/` template-facing view models and formatting builders
- `internal/testutil/` shared test helpers
- `templates/` HTML templates and htmx partials (including auth screens)
- `static/` frontend assets (`app.js`, `auth.js`, CSS, icons, vendor JS)
