#!/usr/bin/env bash
# Run one production NewsCheck under an OS-level non-blocking lock.
set -euo pipefail
umask 077

deploy_dir="${URBAN_RADAR_DEPLOY_DIR:-/opt/urban-radar}"
env_file="${URBAN_RADAR_ENV_FILE:-${deploy_dir}/.env}"

fail() {
  printf 'urban-radar news-check wrapper: %s\n' "$*" >&2
  exit 1
}

[[ -r "$env_file" ]] || fail "environment file is not readable: $env_file"

# The environment file is an operator-controlled shell assignment file. Keep it
# owner-readable only; do not place it in Git.
set -a
# shellcheck disable=SC1090
. "$env_file"
set +a

if [[ -n "${URBAN_RADAR_HOME:-}" ]]; then
  export HOME="$URBAN_RADAR_HOME"
fi
export PATH="${PATH:-/usr/local/bin:/usr/bin:/bin}"

binary="${URBAN_RADAR_BINARY:-${deploy_dir}/bin/urban-radar}"
run_dir="${URBAN_RADAR_RUN_DIR:-${deploy_dir}/run}"
log_dir="${URBAN_RADAR_LOG_DIR:-${deploy_dir}/logs}"
lock_file="${URBAN_RADAR_LOCK_FILE:-${run_dir}/news-check.lock}"
log_file="${URBAN_RADAR_LOG_FILE:-${log_dir}/news-check.log}"

[[ -n "${DATABASE_URL:-}" ]] || fail "DATABASE_URL is not set"
[[ -n "${ZAKUPKI_SEARCH_URL:-}" ]] || fail "ZAKUPKI_SEARCH_URL is not set"
[[ -x "$binary" ]] || fail "production binary is not executable: $binary"
command -v flock >/dev/null 2>&1 || fail "flock is required (install util-linux)"
command -v hermes >/dev/null 2>&1 || fail "hermes is not available on PATH"
if [[ -n "${ZAKUPKI_CA_FILE:-}" ]]; then
  [[ -r "$ZAKUPKI_CA_FILE" ]] || fail "ZAKUPKI_CA_FILE is not readable"
fi

mkdir -p "$run_dir" "$log_dir"

{
  exec 9>"$lock_file"
  if ! flock -n 9; then
    printf '%s news-check skipped: lock already held\n' "$(date -Is)"
    exit 75
  fi

  printf '%s news-check started\n' "$(date -Is)"
  set +e
  "$binary" news check
  exit_code=$?
  set -e
  printf '%s news-check finished: exit_code=%d\n' "$(date -Is)" "$exit_code"
  exit "$exit_code"
} >>"$log_file" 2>&1
