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
VALIDATE_INSTANCE_DB_PATH="${VALIDATE_INSTANCE_DB_PATH:-true}"
VALIDATE_RP_ID_PLACEHOLDER="${VALIDATE_RP_ID_PLACEHOLDER:-true}"
ALLOW_BASE_SERVICE_WITH_INSTANCES="${ALLOW_BASE_SERVICE_WITH_INSTANCES:-false}"

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
parse_bool "$VALIDATE_INSTANCE_DB_PATH"
parse_bool "$VALIDATE_RP_ID_PLACEHOLDER"
parse_bool "$ALLOW_BASE_SERVICE_WITH_INSTANCES"

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

for idx in "${!SERVICE_UNITS[@]}"; do
  unit="${SERVICE_UNITS[$idx]}"
  if [[ "$unit" != *.service ]]; then
    SERVICE_UNITS[$idx]="${unit}.service"
  fi
done

USES_INSTANCE_UNITS=false
INSTANCE_NAMES=()
TARGETS_BASE_SERVICE=false
BASE_SERVICE_UNIT="${APP_UNIT_BASE}.service"
for unit in "${SERVICE_UNITS[@]}"; do
  if [[ "$unit" == "$BASE_SERVICE_UNIT" ]]; then
    TARGETS_BASE_SERVICE=true
  fi

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

echo "Target services:"
for unit in "${SERVICE_UNITS[@]}"; do
  echo "  - $unit"
done

if [[ "$USES_INSTANCE_UNITS" == "true" ]] && [[ "$ALLOW_BASE_SERVICE_WITH_INSTANCES" == "false" ]] && [[ "$TARGETS_BASE_SERVICE" == "false" ]]; then
  base_active=false
  base_enabled=false
  if run_root systemctl is-active --quiet "$BASE_SERVICE_UNIT"; then
    base_active=true
  fi
  if run_root systemctl is-enabled --quiet "$BASE_SERVICE_UNIT" >/dev/null 2>&1; then
    base_enabled=true
  fi
  if [[ "$base_active" == "true" ]] || [[ "$base_enabled" == "true" ]]; then
    echo "error: $BASE_SERVICE_UNIT is active or enabled while deploying instance services."
    echo "hint: disable it to avoid a third running service:"
    echo "      sudo systemctl disable --now $BASE_SERVICE_UNIT"
    echo "hint: set ALLOW_BASE_SERVICE_WITH_INSTANCES=true to bypass this guard."
    exit 1
  fi
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

if [[ "$USES_INSTANCE_UNITS" == "true" ]] && [[ "$VALIDATE_INSTANCE_DB_PATH" == "true" ]]; then
  shared_db_path="$(read_env_key "$ENV_DST" "DB_PATH")"
  invalid_db_paths=()

  for unit in "${SERVICE_UNITS[@]}"; do
    if [[ "$unit" != *"@"* ]]; then
      continue
    fi

    instance_name="${unit#*@}"
    instance_name="${instance_name%.service}"
    instance_env_dst="$(dirname "$ENV_DST")/$instance_name.env"
    instance_db_path="$(read_env_key "$instance_env_dst" "DB_PATH")"
    effective_db_path="$instance_db_path"
    if [[ -z "$effective_db_path" ]]; then
      effective_db_path="$shared_db_path"
    fi
    if [[ -z "$effective_db_path" ]]; then
      continue
    fi

    if [[ "$effective_db_path" == /* ]]; then
      allowed_db_prefix="$APP_HOME/$instance_name/"
      if [[ "$effective_db_path" != "$APP_HOME/$instance_name" ]] && [[ "$effective_db_path" != "$allowed_db_prefix"* ]]; then
        invalid_db_paths+=("$unit -> DB_PATH=$effective_db_path (must be relative or under $APP_HOME/$instance_name)")
      fi
    fi
  done

  if [[ "${#invalid_db_paths[@]}" -gt 0 ]]; then
    echo "error: invalid DB_PATH values detected for instance services:"
    for invalid_db in "${invalid_db_paths[@]}"; do
      echo "  - $invalid_db"
    done
    echo "hint: set DB_PATH=rss.db (recommended) or /var/lib/pulse-rss/<instance>/rss.db."
    echo "hint: use VALIDATE_INSTANCE_DB_PATH=false to bypass this guard if intentional."
    exit 1
  fi
fi

if [[ "$VALIDATE_RP_ID_PLACEHOLDER" == "true" ]]; then
  shared_rp_id="$(read_env_key "$ENV_DST" "AUTH_RP_ID")"
  placeholder_rp_id_hits=()

  for unit in "${SERVICE_UNITS[@]}"; do
    effective_rp_id="$shared_rp_id"
    if [[ "$unit" == *"@"* ]]; then
      instance_name="${unit#*@}"
      instance_name="${instance_name%.service}"
      instance_env_dst="$(dirname "$ENV_DST")/$instance_name.env"
      instance_rp_id="$(read_env_key "$instance_env_dst" "AUTH_RP_ID")"
      if [[ -n "$instance_rp_id" ]]; then
        effective_rp_id="$instance_rp_id"
      fi
    fi

    if [[ "$effective_rp_id" == "rss.example.com" ]]; then
      placeholder_rp_id_hits+=("$unit -> AUTH_RP_ID=rss.example.com")
    fi
  done

  if [[ "${#placeholder_rp_id_hits[@]}" -gt 0 ]]; then
    echo "error: placeholder AUTH_RP_ID detected in deployed configuration:"
    for hit in "${placeholder_rp_id_hits[@]}"; do
      echo "  - $hit"
    done
    echo "hint: set AUTH_RP_ID to your real domain in /etc/pulse-rss/pulse-rss.env or /etc/pulse-rss/<instance>.env."
    echo "hint: use VALIDATE_RP_ID_PLACEHOLDER=false to bypass this guard if intentional."
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
