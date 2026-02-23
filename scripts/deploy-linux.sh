#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

APP_NAME="${APP_NAME:-pulse-rss}"
APP_USER="${APP_USER:-pulse-rss}"
APP_GROUP="${APP_GROUP:-$APP_USER}"
APP_HOME="${APP_HOME:-/var/lib/pulse-rss}"
APP_UNIT_BASE="${APP_NAME%.service}"

BIN_SRC="${BIN_SRC:-$REPO_ROOT/rss}"
BIN_DST="${BIN_DST:-/usr/local/bin/pulse-rss}"

SERVICE_SRC="${SERVICE_SRC:-$REPO_ROOT/deploy/systemd/pulse-rss.service}"
SERVICE_DST_DEFAULT="/etc/systemd/system/${APP_UNIT_BASE}.service"
SERVICE_DST_SET=false
if [[ -n "${SERVICE_DST+x}" ]]; then
  SERVICE_DST_SET=true
fi
SERVICE_DST="${SERVICE_DST:-$SERVICE_DST_DEFAULT}"

ENV_EXAMPLE_SRC="${ENV_EXAMPLE_SRC:-$REPO_ROOT/deploy/systemd/pulse-rss.env.example}"
ENV_DST="${ENV_DST:-/etc/pulse-rss/pulse-rss.env}"

CADDY_SRC="${CADDY_SRC:-$REPO_ROOT/Caddyfile.example}"
CADDY_DST="${CADDY_DST:-/etc/caddy/Caddyfile}"

GIT_PULL="${GIT_PULL:-false}"
GIT_REMOTE="${GIT_REMOTE:-origin}"
GIT_BRANCH="${GIT_BRANCH:-main}"
BUILD_BINARY="${BUILD_BINARY:-true}"
RUN_CHECKS="${RUN_CHECKS:-false}"
INSTALL_SERVICE_UNIT="${INSTALL_SERVICE_UNIT:-true}"
INSTALL_ENV_TEMPLATE="${INSTALL_ENV_TEMPLATE:-true}"
ENSURE_APP_USER="${ENSURE_APP_USER:-true}"
ENABLE_SERVICES="${ENABLE_SERVICES:-true}"
APPLY_CADDY="${APPLY_CADDY:-true}"
CADDY_ALLOW_PLACEHOLDER="${CADDY_ALLOW_PLACEHOLDER:-false}"
VALIDATE_INSTANCE_PORTS="${VALIDATE_INSTANCE_PORTS:-true}"

DEFAULT_SERVICE="${APP_UNIT_BASE}.service"
SERVICES_RAW="${SERVICES:-$DEFAULT_SERVICE}"

if [[ "$EUID" -eq 0 ]]; then
  SUDO=()
else
  SUDO=(sudo)
fi

run_root() {
  "${SUDO[@]}" "$@"
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1"
    exit 1
  fi
}

parse_bool() {
  case "$1" in
    true|false) ;;
    *)
      echo "error: expected true/false but got: $1"
      exit 1
      ;;
  esac
}

parse_bool "$GIT_PULL"
parse_bool "$BUILD_BINARY"
parse_bool "$RUN_CHECKS"
parse_bool "$INSTALL_SERVICE_UNIT"
parse_bool "$INSTALL_ENV_TEMPLATE"
parse_bool "$ENSURE_APP_USER"
parse_bool "$ENABLE_SERVICES"
parse_bool "$APPLY_CADDY"
parse_bool "$CADDY_ALLOW_PLACEHOLDER"
parse_bool "$VALIDATE_INSTANCE_PORTS"

read_env_key() {
  local env_file="$1"
  local env_key="$2"
  local raw_value=""
  if [[ ! -f "$env_file" ]]; then
    return 0
  fi
  raw_value="$(sed -nE "s/^[[:space:]]*${env_key}[[:space:]]*=[[:space:]]*(.*)[[:space:]]*$/\\1/p" "$env_file" | tail -n1)"
  if [[ -z "$raw_value" ]]; then
    return 0
  fi
  raw_value="${raw_value#\"}"
  raw_value="${raw_value%\"}"
  raw_value="${raw_value#\'}"
  raw_value="${raw_value%\'}"
  printf '%s' "$raw_value"
}

require_cmd install
require_cmd id
require_cmd systemctl
if [[ "${#SUDO[@]}" -gt 0 ]]; then
  require_cmd sudo
fi
if [[ "$GIT_PULL" == "true" ]]; then
  require_cmd git
fi
if [[ "$BUILD_BINARY" == "true" ]]; then
  require_cmd go
fi
if [[ "$RUN_CHECKS" == "true" ]]; then
  if [[ ! -x "$REPO_ROOT/scripts/check.sh" ]]; then
    echo "error: RUN_CHECKS=true but $REPO_ROOT/scripts/check.sh is not executable"
    exit 1
  fi
fi

if [[ "$INSTALL_SERVICE_UNIT" == "true" ]] && [[ ! -f "$SERVICE_SRC" ]]; then
  echo "error: service template not found at $SERVICE_SRC"
  exit 1
fi

if [[ "$INSTALL_ENV_TEMPLATE" == "true" ]] && [[ ! -f "$ENV_EXAMPLE_SRC" ]]; then
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

SERVICES_RAW="${SERVICES_RAW//,/ }"
read -r -a SERVICE_UNITS <<<"$SERVICES_RAW"
if [[ "${#SERVICE_UNITS[@]}" -eq 0 ]]; then
  echo "error: no systemd service units configured"
  echo "hint: set SERVICES env var, for example:"
  echo "      SERVICES='pulse-rss.service'"
  exit 1
fi

USES_INSTANCE_UNITS=false
INSTANCE_NAMES=()
for unit in "${SERVICE_UNITS[@]}"; do
  if [[ "$unit" == *"@"* ]]; then
    instance_name="${unit#*@}"
    instance_name="${instance_name%.service}"
    if [[ -z "$instance_name" ]]; then
      continue
    fi
    USES_INSTANCE_UNITS=true
    seen_instance=false
    for existing_instance in "${INSTANCE_NAMES[@]}"; do
      if [[ "$existing_instance" == "$instance_name" ]]; then
        seen_instance=true
        break
      fi
    done
    if [[ "$seen_instance" == "false" ]]; then
      INSTANCE_NAMES+=("$instance_name")
    fi
  fi
done

if [[ "$USES_INSTANCE_UNITS" == "true" ]] && [[ "$SERVICE_DST_SET" == "false" ]]; then
  SERVICE_DST="/etc/systemd/system/${APP_UNIT_BASE}@.service"
fi

if [[ "${#SUDO[@]}" -gt 0 ]]; then
  echo "Checking sudo access..."
  run_root -v
fi

if [[ "$GIT_PULL" == "true" ]]; then
  echo "Pulling latest code from $GIT_REMOTE/$GIT_BRANCH"
  git -C "$REPO_ROOT" pull --ff-only "$GIT_REMOTE" "$GIT_BRANCH"
fi

if [[ "$BUILD_BINARY" == "true" ]]; then
  echo "Building binary at $BIN_SRC"
  (
    cd "$REPO_ROOT"
    go build -o "$BIN_SRC" .
  )
fi

if [[ ! -f "$BIN_SRC" ]]; then
  echo "error: binary not found at $BIN_SRC"
  echo "hint: build it first with: go build -o ./rss . or set BUILD_BINARY=true"
  exit 1
fi

if [[ "$RUN_CHECKS" == "true" ]]; then
  echo "Running quality checks"
  (
    cd "$REPO_ROOT"
    ./scripts/check.sh
  )
fi

echo "Installing Pulse RSS binary to $BIN_DST"
run_root install -d -m 0750 "$APP_HOME"
run_root install -d -m 0750 "$(dirname "$ENV_DST")"
run_root install -o root -g root -m 0755 "$BIN_SRC" "$BIN_DST"

if [[ "$ENSURE_APP_USER" == "true" ]]; then
  if [[ -x /usr/sbin/nologin ]]; then
    NOLOGIN_SHELL="/usr/sbin/nologin"
  elif [[ -x /sbin/nologin ]]; then
    NOLOGIN_SHELL="/sbin/nologin"
  else
    NOLOGIN_SHELL="/bin/false"
  fi

  if id -u "$APP_USER" >/dev/null 2>&1; then
    echo "User $APP_USER already exists"
  else
    echo "Creating system user $APP_USER"
    run_root useradd --system --home "$APP_HOME" --shell "$NOLOGIN_SHELL" "$APP_USER"
  fi

  echo "Setting ownership on $APP_HOME"
  run_root chown "$APP_USER:$APP_GROUP" "$APP_HOME"
fi

if [[ "$USES_INSTANCE_UNITS" == "true" ]]; then
  for instance_name in "${INSTANCE_NAMES[@]}"; do
    instance_home="$APP_HOME/$instance_name"
    echo "Ensuring instance data dir $instance_home"
    run_root install -d -m 0750 "$instance_home"
    if [[ "$ENSURE_APP_USER" == "true" ]]; then
      run_root chown "$APP_USER:$APP_GROUP" "$instance_home"
    fi
  done
fi

if [[ "$INSTALL_SERVICE_UNIT" == "true" ]]; then
  echo "Installing systemd unit to $SERVICE_DST"
  run_root install -o root -g root -m 0644 "$SERVICE_SRC" "$SERVICE_DST"
fi

if [[ "$INSTALL_ENV_TEMPLATE" == "true" ]]; then
  if [[ "$USES_INSTANCE_UNITS" == "true" ]]; then
    for instance_name in "${INSTANCE_NAMES[@]}"; do
      instance_env_dst="$(dirname "$ENV_DST")/$instance_name.env"
      if [[ -f "$instance_env_dst" ]]; then
        echo "Keeping existing env file at $instance_env_dst"
      else
        echo "Installing env template to $instance_env_dst"
        run_root install -o root -g root -m 0640 "$ENV_EXAMPLE_SRC" "$instance_env_dst"
      fi
    done
  fi

  if [[ -f "$ENV_DST" ]]; then
    echo "Keeping existing env file at $ENV_DST"
  else
    echo "Installing env template to $ENV_DST"
    run_root install -o root -g root -m 0640 "$ENV_EXAMPLE_SRC" "$ENV_DST"
    echo "IMPORTANT: edit $ENV_DST with your real domain and secrets before first login."
  fi
fi

if [[ "$USES_INSTANCE_UNITS" == "true" ]] && [[ "$VALIDATE_INSTANCE_PORTS" == "true" ]]; then
  shared_port="$(read_env_key "$ENV_DST" "PORT")"
  seen_ports=()
  seen_units=()
  port_collisions=()

  for unit in "${SERVICE_UNITS[@]}"; do
    if [[ "$unit" != *"@"* ]]; then
      continue
    fi

    instance_name="${unit#*@}"
    instance_name="${instance_name%.service}"
    instance_env_dst="$(dirname "$ENV_DST")/$instance_name.env"
    instance_port="$(read_env_key "$instance_env_dst" "PORT")"
    effective_port="$instance_port"
    if [[ -z "$effective_port" ]]; then
      effective_port="$shared_port"
    fi
    if [[ -z "$effective_port" ]]; then
      continue
    fi

    for idx in "${!seen_ports[@]}"; do
      if [[ "${seen_ports[$idx]}" == "$effective_port" ]]; then
        port_collisions+=("PORT=$effective_port -> ${seen_units[$idx]} and $unit")
      fi
    done
    seen_ports+=("$effective_port")
    seen_units+=("$unit")
  done

  if [[ "${#port_collisions[@]}" -gt 0 ]]; then
    echo "error: duplicate PORT values detected across instance services:"
    for collision in "${port_collisions[@]}"; do
      echo "  - $collision"
    done
    echo "hint: set unique PORT values in /etc/pulse-rss/<instance>.env before restarting."
    echo "hint: use VALIDATE_INSTANCE_PORTS=false to bypass this guard if intentional."
    exit 1
  fi
fi

if [[ "$APPLY_CADDY" == "true" ]]; then
  echo "Installing Caddy config to $CADDY_DST"
  run_root install -d -m 0755 "$(dirname "$CADDY_DST")"
  run_root install -o root -g root -m 0644 "$CADDY_SRC" "$CADDY_DST"
fi

echo "Reloading systemd"
run_root systemctl daemon-reload

for unit in "${SERVICE_UNITS[@]}"; do
  if [[ "$ENABLE_SERVICES" == "true" ]]; then
    echo "Enabling $unit"
    run_root systemctl enable "$unit"
  fi
  echo "Restarting $unit"
  run_root systemctl restart "$unit"
done

if [[ "$APPLY_CADDY" == "true" ]]; then
  if run_root systemctl status caddy >/dev/null 2>&1; then
    echo "Reloading caddy"
    run_root systemctl reload caddy
  else
    echo "warning: caddy service is not available; skipped reload"
  fi
fi

echo
for unit in "${SERVICE_UNITS[@]}"; do
  if run_root systemctl is-active --quiet "$unit"; then
    echo "OK: $unit is active"
  else
    echo "ERROR: $unit is not active"
    run_root systemctl status "$unit" --no-pager || true
    exit 1
  fi
done

echo
echo "Deployment complete."
echo "Next checks:"
for unit in "${SERVICE_UNITS[@]}"; do
  echo "  sudo systemctl status $unit --no-pager"
done
if [[ "$APPLY_CADDY" == "true" ]]; then
  echo "  sudo systemctl status caddy --no-pager"
fi
