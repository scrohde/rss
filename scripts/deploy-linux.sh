#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

APP_NAME="${APP_NAME:-pulse-rss}"
APP_USER="${APP_USER:-pulse-rss}"
APP_GROUP="${APP_GROUP:-$APP_USER}"
APP_HOME="${APP_HOME:-/var/lib/pulse-rss}"

BIN_SRC="${BIN_SRC:-$REPO_ROOT/rss}"
BIN_DST="${BIN_DST:-/usr/local/bin/pulse-rss}"

SERVICE_SRC="${SERVICE_SRC:-$REPO_ROOT/deploy/systemd/pulse-rss.service}"
SERVICE_DST="${SERVICE_DST:-/etc/systemd/system/pulse-rss.service}"

ENV_EXAMPLE_SRC="${ENV_EXAMPLE_SRC:-$REPO_ROOT/deploy/systemd/pulse-rss.env.example}"
ENV_DST="${ENV_DST:-/etc/pulse-rss/pulse-rss.env}"

CADDY_SRC="${CADDY_SRC:-$REPO_ROOT/Caddyfile.example}"
CADDY_DST="${CADDY_DST:-/etc/caddy/Caddyfile}"
APPLY_CADDY="${APPLY_CADDY:-true}"
CADDY_ALLOW_PLACEHOLDER="${CADDY_ALLOW_PLACEHOLDER:-false}"

if [[ ! -f "$BIN_SRC" ]]; then
	echo "error: binary not found at $BIN_SRC"
	echo "hint: build it first with: go build -o ./rss ."
	exit 1
fi

if [[ ! -f "$SERVICE_SRC" ]]; then
	echo "error: service template not found at $SERVICE_SRC"
	exit 1
fi

if [[ ! -f "$ENV_EXAMPLE_SRC" ]]; then
	echo "error: env template not found at $ENV_EXAMPLE_SRC"
	exit 1
fi

if [[ "$APPLY_CADDY" == "true" ]] && [[ ! -f "$CADDY_SRC" ]]; then
	echo "error: Caddy template not found at $CADDY_SRC"
	exit 1
fi

if [[ "$APPLY_CADDY" == "true" ]] && [[ "$CADDY_ALLOW_PLACEHOLDER" != "true" ]] &&
	grep -q "rss.example.com" "$CADDY_SRC"; then
	echo "error: $CADDY_SRC still contains rss.example.com placeholder"
	echo "hint: replace with your real domain or set CADDY_ALLOW_PLACEHOLDER=true to bypass"
	exit 1
fi

if [[ "$EUID" -eq 0 ]]; then
	SUDO=()
else
	SUDO=(sudo)
fi

run_root() {
	"${SUDO[@]}" "$@"
}

if [[ -x /usr/sbin/nologin ]]; then
	NOLOGIN_SHELL="/usr/sbin/nologin"
elif [[ -x /sbin/nologin ]]; then
	NOLOGIN_SHELL="/sbin/nologin"
else
	NOLOGIN_SHELL="/bin/false"
fi

echo "Installing Pulse RSS binary to $BIN_DST"
run_root install -d -m 0750 "$APP_HOME"
run_root install -d -m 0750 "$(dirname "$ENV_DST")"
run_root install -o root -g root -m 0755 "$BIN_SRC" "$BIN_DST"

if id -u "$APP_USER" >/dev/null 2>&1; then
	echo "User $APP_USER already exists"
else
	echo "Creating system user $APP_USER"
	run_root useradd --system --home "$APP_HOME" --shell "$NOLOGIN_SHELL" "$APP_USER"
fi

echo "Setting ownership on $APP_HOME"
run_root chown "$APP_USER:$APP_GROUP" "$APP_HOME"

echo "Installing systemd unit to $SERVICE_DST"
run_root install -o root -g root -m 0644 "$SERVICE_SRC" "$SERVICE_DST"

if [[ -f "$ENV_DST" ]]; then
	echo "Keeping existing env file at $ENV_DST"
else
	echo "Installing env template to $ENV_DST"
	run_root install -o root -g root -m 0640 "$ENV_EXAMPLE_SRC" "$ENV_DST"
	echo "IMPORTANT: edit $ENV_DST with your real domain and secrets before first login."
fi

if [[ "$APPLY_CADDY" == "true" ]]; then
	echo "Installing Caddy config to $CADDY_DST"
	run_root install -d -m 0755 "$(dirname "$CADDY_DST")"
	run_root install -o root -g root -m 0644 "$CADDY_SRC" "$CADDY_DST"
fi

echo "Reloading systemd and restarting $APP_NAME"
run_root systemctl daemon-reload
run_root systemctl enable --now "$APP_NAME"
run_root systemctl restart "$APP_NAME"

if [[ "$APPLY_CADDY" == "true" ]]; then
	if run_root systemctl status caddy >/dev/null 2>&1; then
		echo "Reloading caddy"
		run_root systemctl reload caddy
	else
		echo "warning: caddy service is not available; skipped reload"
	fi
fi

echo
echo "Deployment complete."
echo "Next checks:"
echo "  sudo systemctl status $APP_NAME --no-pager"
if [[ "$APPLY_CADDY" == "true" ]]; then
	echo "  sudo systemctl status caddy --no-pager"
fi
