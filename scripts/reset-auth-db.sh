#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/reset-auth-db.sh [DB_PATH]

Resets auth-only data:
  - auth_webauthn_credentials
  - auth_sessions
  - auth_webauthn_challenges
  - auth_recovery_codes

Examples:
  scripts/reset-auth-db.sh
  scripts/reset-auth-db.sh /path/to/rss.db

Set RESET_AUTH_DB_CONFIRM=yes to skip the interactive prompt.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

db_path="${1:-rss.db}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "error: sqlite3 is required but was not found in PATH" >&2
  exit 1
fi

if [[ ! -f "$db_path" ]]; then
  echo "error: database not found: $db_path" >&2
  exit 1
fi

if [[ "${RESET_AUTH_DB_CONFIRM:-}" != "yes" ]]; then
  echo "This will permanently delete auth credentials, sessions, recovery codes, and auth challenges in:"
  echo "  $db_path"
  read -r -p "Type RESET to continue: " confirm
  if [[ "$confirm" != "RESET" ]]; then
    echo "aborted"
    exit 1
  fi
fi

sqlite3 "$db_path" <<'SQL'
PRAGMA foreign_keys = ON;
BEGIN IMMEDIATE;
DELETE FROM auth_webauthn_credentials;
DELETE FROM auth_sessions;
DELETE FROM auth_webauthn_challenges;
DELETE FROM auth_recovery_codes;
COMMIT;
PRAGMA wal_checkpoint(TRUNCATE);
SQL

echo "auth reset complete: $db_path"
echo "next step: restart the app and open /auth/setup"
