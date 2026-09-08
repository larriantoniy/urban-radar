#!/usr/bin/env bash
# Minimal Docker-wrapper mechanics checks for scripts/run-news-check.sh. No
# Docker daemon, network, database, Hermes, or Urban Radar binary is invoked.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
wrapper="$repo_dir/scripts/run-news-check.sh"

if ! command -v flock >/dev/null 2>&1; then
  printf 'SKIP: flock is unavailable on this host; run this test on Ubuntu.\n'
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/no-docker-bin" "$tmp/project" "$tmp/hermes-home"
ln -s "$(command -v flock)" "$tmp/no-docker-bin/flock"
touch "$tmp/project/compose.yaml"
cat >"$tmp/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "compose" && "${2:-}" == "version" ]]; then
  printf 'Docker Compose version vtest\n'
  exit 0
fi
printf '%s\n' "$*" >>"${FAKE_DOCKER_CALLS:?}"
exit "${FAKE_DOCKER_EXIT:-0}"
EOF
chmod +x "$tmp/bin/docker"
printf '%s\n' \
  "DATABASE_URL='postgresql://test.invalid/urban_radar'" \
  "POSTGRES_DB='urban_radar'" \
  "POSTGRES_USER='urban_radar'" \
  "POSTGRES_PASSWORD='test-password'" \
  "ZAKUPKI_SEARCH_URL='https://example.invalid/zakupki'" \
  "HERMES_HOME_HOST_DIR='$tmp/hermes-home'" \
  "PATH='$tmp/bin:/usr/bin:/bin'" > "$tmp/runtime.env"
sed "s|PATH='$tmp/bin:/usr/bin:/bin'|PATH='$tmp/no-docker-bin'|" "$tmp/runtime.env" > "$tmp/no-docker.env"
env_file="$tmp/runtime.env"

run_wrapper() {
  env \
    URBAN_RADAR_DEPLOY_DIR="$tmp/deploy" \
    URBAN_RADAR_ENV_FILE="$env_file" \
    URBAN_RADAR_PROJECT_DIR="$tmp/project" \
    URBAN_RADAR_RUN_DIR="$tmp/run" \
    URBAN_RADAR_LOG_DIR="$tmp/logs" \
    FAKE_DOCKER_CALLS="$tmp/docker.calls" \
    FAKE_DOCKER_EXIT="${1:-0}" \
    "$wrapper"
}

set +e
env URBAN_RADAR_ENV_FILE="$tmp/missing.env" "$wrapper" >/dev/null 2>&1
missing_env_status=$?
env URBAN_RADAR_ENV_FILE="$env_file" URBAN_RADAR_PROJECT_DIR="$tmp/missing-project" "$wrapper" >/dev/null 2>&1
missing_compose_status=$?
env URBAN_RADAR_ENV_FILE="$tmp/no-docker.env" URBAN_RADAR_PROJECT_DIR="$tmp/project" "$wrapper" >/dev/null 2>&1
missing_docker_status=$?
set -e
[[ "$missing_env_status" -ne 0 ]]
[[ "$missing_compose_status" -ne 0 ]]
[[ "$missing_docker_status" -ne 0 ]]

run_wrapper 0

set +e
run_wrapper 23
child_status=$?
set -e
[[ "$child_status" -eq 23 ]]

lock_file="$tmp/run/news-check.lock"
flock "$lock_file" sleep 2 &
holder=$!
sleep 0.1
set +e
run_wrapper 0
lock_status=$?
set -e
wait "$holder"
[[ "$lock_status" -eq 75 ]]
grep -q 'news-check started' "$tmp/logs/news-check.log"
grep -q 'news-check finished: exit_code=23' "$tmp/logs/news-check.log"
grep -q 'news-check skipped: lock already held' "$tmp/logs/news-check.log"
grep -q 'run --rm urban-radar news check' "$tmp/docker.calls"
printf 'PASS: run-news-check wrapper mechanics\n'
