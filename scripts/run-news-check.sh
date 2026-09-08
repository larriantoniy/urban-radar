#!/usr/bin/env bash
# Run one Dockerized production NewsCheck under an OS-level non-blocking lock.
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

export PATH="${PATH:-/usr/local/bin:/usr/bin:/bin}"

project_dir="${URBAN_RADAR_PROJECT_DIR:-/srv/urban-radar}"
compose_file="${URBAN_RADAR_COMPOSE_FILE:-${project_dir}/compose.yaml}"
run_dir="${URBAN_RADAR_RUN_DIR:-${deploy_dir}/run}"
log_dir="${URBAN_RADAR_LOG_DIR:-${deploy_dir}/logs}"
lock_file="${URBAN_RADAR_LOCK_FILE:-${run_dir}/news-check.lock}"
log_file="${URBAN_RADAR_LOG_FILE:-${log_dir}/news-check.log}"

mkdir -p "$run_dir" "$log_dir"
# From this point, include configuration and Docker diagnostics with the run
# transcript. The missing-env-file case cannot know a trusted log destination.
exec >>"$log_file" 2>&1

[[ -n "${DATABASE_URL:-}" ]] || fail "DATABASE_URL is not set"
[[ -n "${ZAKUPKI_SEARCH_URL:-}" ]] || fail "ZAKUPKI_SEARCH_URL is not set"
[[ -n "${POSTGRES_DB:-}" ]] || fail "POSTGRES_DB is not set"
[[ -n "${POSTGRES_USER:-}" ]] || fail "POSTGRES_USER is not set"
[[ -n "${POSTGRES_PASSWORD:-}" ]] || fail "POSTGRES_PASSWORD is not set"
[[ -n "${HERMES_HOME_HOST_DIR:-}" ]] || fail "HERMES_HOME_HOST_DIR is not set"
[[ -d "$HERMES_HOME_HOST_DIR" ]] || fail "HERMES_HOME_HOST_DIR is not a directory: $HERMES_HOME_HOST_DIR"
[[ -f "$compose_file" ]] || fail "Compose file is not readable: $compose_file"
command -v flock >/dev/null 2>&1 || fail "flock is required (install util-linux)"
command -v docker >/dev/null 2>&1 || fail "docker is required"
docker compose version >/dev/null 2>&1 || fail "Docker Compose plugin is required"
if [[ -n "${ZAKUPKI_CA_FILE:-}" ]]; then
  [[ "$ZAKUPKI_CA_FILE" == /run/secrets/* ]] || fail "ZAKUPKI_CA_FILE must be a container path under /run/secrets"
  [[ -n "${ZAKUPKI_CA_HOST_PATH:-}" ]] || fail "ZAKUPKI_CA_HOST_PATH is required with ZAKUPKI_CA_FILE"
  [[ -r "$ZAKUPKI_CA_HOST_PATH" ]] || fail "ZAKUPKI_CA_HOST_PATH is not readable"
fi

{
  exec 9>"$lock_file"
  if ! flock -n 9; then
    printf '%s news-check skipped: lock already held\n' "$(date -Is)"
    exit 75
  fi

  printf '%s news-check started\n' "$(date -Is)"
  set +e
  docker compose --project-directory "$project_dir" --env-file "$env_file" -f "$compose_file" run --rm urban-radar news check
  exit_code=$?
  set -e
  printf '%s news-check finished: exit_code=%d\n' "$(date -Is)" "$exit_code"
  exit "$exit_code"
}
