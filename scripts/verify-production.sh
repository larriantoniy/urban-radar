#!/usr/bin/env bash
# Safe production checks: no collection, content generation, Telegram, or VK.
set -euo pipefail

fail() {
  printf 'urban-radar production verification: %s\n' "$*" >&2
  exit 1
}

script_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repository_root="$(cd -- "${script_dir}/.." && pwd -P)"
env_file="${URBAN_RADAR_ENV_FILE:-/opt/urban-radar/.env}"
compose_file="${URBAN_RADAR_COMPOSE_FILE:-${repository_root}/compose.yaml}"
hermes_home="${URBAN_RADAR_HERMES_HOME:-/opt/urban-radar/hermes}"
mode="${1:-verify}"
[[ "$mode" == verify || "$mode" == --assets-only || "$mode" == --status ]] || fail 'usage: verify-production.sh [--assets-only|--status]'
[[ -r "$env_file" ]] || fail "production environment file is not readable: ${env_file}"
[[ -f "$compose_file" ]] || fail "Compose file is not readable: ${compose_file}"

host_test() {
  if [[ "${EUID}" -eq 0 ]]; then
    test "$@"
  else
    sudo test "$@"
  fi
}

if [[ "${URBAN_RADAR_HOST_ASSETS_ALREADY_VERIFIED:-}" == 1 ]]; then
  printf 'managed Hermes assets: already verified by deploy sync\n'
else
  host_test -f "$hermes_home/skills/urban-radar-editorial-style-v1/SKILL.md"
  host_test -x "$hermes_home/plugins/urban-radar-telegram-review-experiment/review_notify.py"
  printf 'managed Hermes assets: OK\n'
fi

if [[ "$mode" == --assets-only ]]; then
  exit 0
fi

command -v docker >/dev/null 2>&1 || fail 'docker is required'
compose=(docker compose --project-directory "$repository_root" --env-file "$env_file" -f "$compose_file")
"${compose[@]}" config -q

service_state() {
  local service="$1" required_health="$2" container state
  container="$("${compose[@]}" ps -q "$service")"
  [[ -n "$container" ]] || fail "$service container is absent"
  state="$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container")"
  [[ "$state" == running* ]] || fail "$service is not running: ${state}"
  if [[ "$required_health" == true ]]; then
    [[ "$state" == *' healthy' ]] || fail "$service is not healthy: ${state}"
  fi
}

if [[ "$mode" == --status ]]; then
  "${compose[@]}" ps
  service_state postgres true
  service_state hermes-gateway false
  "${compose[@]}" exec hermes-gateway /opt/hermes-venv/bin/hermes gateway status
  exit 0
fi

service_state postgres true
service_state hermes-gateway false
"${compose[@]}" run --rm --no-deps --entrypoint sh urban-radar -lc '
  test -f /var/lib/hermes/skills/urban-radar-editorial-style-v1/SKILL.md &&
  test -x /var/lib/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py &&
  test "$(head -n 1 /var/lib/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py)" = "#!/opt/hermes-venv/bin/python"
'
"${compose[@]}" exec hermes-gateway /opt/hermes-venv/bin/hermes gateway status
printf 'production verification: OK\n'
