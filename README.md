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
- `DB_PATH` sets the SQLite database file path (default `rss.db` in the process working directory).

## Run as a public service
Production templates in this repo:
- [`Caddyfile.example`](./Caddyfile.example) (hardened TLS reverse-proxy config)
- [`deploy/systemd/pulse-rss.service`](./deploy/systemd/pulse-rss.service)
- [`deploy/systemd/pulse-rss.env.example`](./deploy/systemd/pulse-rss.env.example)

### Linux production setup (systemd + Caddy)

Quick deploy helper:
```bash
go build -o ./rss .
./scripts/deploy-linux.sh
```

Useful overrides:
- `APPLY_CADDY=false` to skip installing/reloading Caddy.
- `BIN_SRC=/path/to/rss` to deploy a different binary path.
- `CADDY_SRC=/path/to/Caddyfile` to deploy a different Caddy config file.
- `CADDY_ALLOW_PLACEHOLDER=true` to bypass placeholder-domain safety check.

1. Install Pulse RSS binary:
```bash
sudo install -d -m 0750 /var/lib/pulse-rss
sudo install -d -m 0750 /etc/pulse-rss
sudo install -o root -g root -m 0755 ./rss /usr/local/bin/pulse-rss
```

2. Create runtime user and install service files:
```bash
sudo useradd --system --home /var/lib/pulse-rss --shell /usr/sbin/nologin pulse-rss 2>/dev/null || true
sudo chown pulse-rss:pulse-rss /var/lib/pulse-rss
sudo cp ./deploy/systemd/pulse-rss.service /etc/systemd/system/pulse-rss.service
sudo cp ./deploy/systemd/pulse-rss.env.example /etc/pulse-rss/pulse-rss.env
sudo chmod 0640 /etc/pulse-rss/pulse-rss.env
```

3. Edit `/etc/pulse-rss/pulse-rss.env` for your domain and secrets.

4. Install Caddy config:
```bash
sudo cp ./Caddyfile.example /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

5. Start and enable Pulse RSS:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pulse-rss
sudo systemctl status pulse-rss --no-pager
```

Pulse RSS should remain bound to loopback (`127.0.0.1:8080`) behind Caddy.

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
- `internal/auth/` passkey registration/authentication service logic
- `internal/opml/` OPML import/export parsing and rendering helpers
- `internal/view/` template-facing view models and formatting builders
- `internal/testutil/` shared test helpers
- `templates/` HTML templates and htmx partials (including auth screens)
- `static/` frontend assets (`app.js`, `auth.js`, CSS, icons, vendor JS)
