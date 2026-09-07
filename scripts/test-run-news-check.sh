#!/usr/bin/env bash
# Minimal Ubuntu mechanics checks for scripts/run-news-check.sh. No network,
# database, Hermes, or Urban Radar binary is invoked.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
wrapper="$repo_dir/scripts/run-news-check.sh"

if ! command -v flock >/dev/null 2>&1; then
  printf 'SKIP: flock is unavailable on this host; run this test on Ubuntu.\n'
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
cp /usr/bin/true "$tmp/bin/hermes"
printf '%s\n' \
  "DATABASE_URL='postgresql://test.invalid/urban_radar'" \
  "ZAKUPKI_SEARCH_URL='https://example.invalid/zakupki'" \
  "PATH='$tmp/bin:/usr/bin:/bin'" > "$tmp/runtime.env"
env_file="$tmp/runtime.env"

run_wrapper() {
  env \
    URBAN_RADAR_DEPLOY_DIR="$tmp/deploy" \
    URBAN_RADAR_ENV_FILE="$env_file" \
    URBAN_RADAR_BINARY="$1" \
    URBAN_RADAR_RUN_DIR="$tmp/run" \
    URBAN_RADAR_LOG_DIR="$tmp/logs" \
    "$wrapper"
}

set +e
env URBAN_RADAR_ENV_FILE="$tmp/missing.env" "$wrapper" >/dev/null 2>&1
missing_env_status=$?
run_wrapper "$tmp/missing-binary" >/dev/null 2>&1
missing_binary_status=$?
set -e
[[ "$missing_env_status" -ne 0 ]]
[[ "$missing_binary_status" -ne 0 ]]

run_wrapper /usr/bin/true

set +e
run_wrapper /usr/bin/false
child_status=$?
set -e
[[ "$child_status" -eq 1 ]]

lock_file="$tmp/run/news-check.lock"
flock "$lock_file" sleep 2 &
holder=$!
sleep 0.1
set +e
run_wrapper /usr/bin/true
lock_status=$?
set -e
wait "$holder"
[[ "$lock_status" -eq 75 ]]
grep -q 'news-check started' "$tmp/logs/news-check.log"
grep -q 'news-check finished: exit_code=1' "$tmp/logs/news-check.log"
grep -q 'news-check skipped: lock already held' "$tmp/logs/news-check.log"
printf 'PASS: run-news-check wrapper mechanics\n'
