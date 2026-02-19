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
AUTH_ENABLED=false go run .
```

Then open http://localhost:8080.

Optional environment variables:
- `LOG_LEVEL` controls structured log verbosity (`debug`, `info`, `warn`, `error`; default `info`).

## Run as a public service
### Caddy reverse proxy (recommended)

Use Caddy for TLS termination and edge headers. Start from [`Caddyfile.example`](./Caddyfile.example) and replace the domain.

Run the app on loopback (`127.0.0.1:8080`) behind Caddy.

### Authentication (Passkeys)

Pulse RSS can run with passkey-only authentication for public hosting.

Set these env vars before `go run .` in production:

```bash
AUTH_ENABLED=true
AUTH_RP_ID=rss.example.com
AUTH_RP_ORIGIN=https://rss.example.com
AUTH_RP_NAME="Pulse RSS"
AUTH_SETUP_TOKEN="<long-random-secret>"
AUTH_SESSION_TTL=24h
AUTH_CHALLENGE_TTL=5m
AUTH_COOKIE_NAME=pulse_rss_session
AUTH_COOKIE_SECURE=true
```

Notes:
- `AUTH_SETUP_TOKEN` is required for initial enrollment.
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
