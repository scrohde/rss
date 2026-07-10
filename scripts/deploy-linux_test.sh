#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$TMP_DIR/bin"
cat >"$TMP_DIR/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$TMP_DIR/bin/sudo" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-v" ]]; then
  exit 0
fi
exec "$@"
EOF
cat >"$TMP_DIR/bin/install" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
args=("$@")
last="${args[${#args[@]} - 1]}"
for arg in "${args[@]}"; do
  if [[ "$arg" == "-d" ]]; then
    mkdir -p "$last"
    exit 0
  fi
done
source="${args[${#args[@]} - 2]}"
mkdir -p "$(dirname "$last")"
cp "$source" "$last"
EOF
chmod +x "$TMP_DIR/bin/systemctl" "$TMP_DIR/bin/sudo" "$TMP_DIR/bin/install"

BIN_SRC="$TMP_DIR/rss"
touch "$BIN_SRC"

run_deploy() {
  local env_dst="$1"
  shift

  env \
    PATH="$TMP_DIR/bin:$PATH" \
    APP_HOME="$TMP_DIR/app" \
    BIN_SRC="$BIN_SRC" \
    BIN_DST="$TMP_DIR/bin/rss" \
    ENV_DST="$env_dst" \
    BUILD_BINARY=false \
    INSTALL_SERVICE_UNIT=false \
    INSTALL_ENV_TEMPLATE=false \
    ENSURE_APP_USER=false \
    ENABLE_SERVICES=false \
    APPLY_CADDY=false \
    VALIDATE_INSTANCE_PORTS=false \
    VALIDATE_INSTANCE_DB_PATH=false \
    VALIDATE_RP_ID_PLACEHOLDER=false \
    "$@" \
    "$REPO_ROOT/scripts/deploy-linux.sh"
}

assert_rejected_without_secret_echo() {
  local env_dst="$1"
  local secret="$2"
  shift 2
  local output

  if output="$(run_deploy "$env_dst" "$@" 2>&1)"; then
    echo "error: deployment unexpectedly accepted an unsafe setup token"
    exit 1
  fi
  if [[ "$output" != *"missing, weak, or placeholder AUTH_SETUP_TOKEN"* ]]; then
    echo "error: deployment did not report setup-token validation failure"
    exit 1
  fi
  if [[ "$output" == *"$secret"* ]]; then
    echo "error: deployment validation echoed the setup token"
    exit 1
  fi
}

shared_env="$TMP_DIR/shared.env"
cat >"$shared_env" <<'EOF'
AUTH_ENABLED=true
AUTH_SETUP_TOKEN=replace-with-long-random-secret
EOF
assert_rejected_without_secret_echo "$shared_env" "replace-with-long-random-secret"

valid_token="S2RvaDBoVlRPNVNqUU1UMHdXMkVWN2FYU3lEVkFrZ0I"
cat >"$shared_env" <<EOF
AUTH_ENABLED=true
AUTH_SETUP_TOKEN=$valid_token
EOF
run_deploy "$shared_env" >/dev/null

instance_env="$TMP_DIR/first.env"
cat >"$instance_env" <<'EOF'
AUTH_SETUP_TOKEN=short-instance-secret
EOF
assert_rejected_without_secret_echo \
  "$instance_env" \
  "short-instance-secret"

echo "deploy-linux_test.sh complete"
