#!/usr/bin/env bash
# Safe production checks: no collection, content generation, Telegram, or VK.
set -euo pipefail

fail() {
  printf 'urban-radar production verification: %s\n' "$*" >&2
  exit 1
}

# Run a test builtin in a privileged shell when the managed 0750 host profile
# is not traversable by the normal deploy user. Predicate and path are passed
# as positional arguments, never interpolated into shell source.
host_test() {
  local predicate="$1" path="$2"
  if [[ "${EUID}" -eq 0 ]]; then
    test "$predicate" "$path"
  else
    sudo sh -c 'test "$1" "$2"' sh "$predicate" "$path"
  fi
}

service_status() {
  local service="$1" required_health="$2" container state
  container="$("${compose[@]}" ps -q "$service")" || {
    printf '%s: unable to inspect container\n' "$service" >&2
    return 1
  }
  if [[ -z "$container" ]]; then
    printf '%s: container absent\n' "$service" >&2
    return 1
  fi
  state="$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' "$container")" || {
    printf '%s: unable to inspect container state\n' "$service" >&2
    return 1
  }
  printf '%s: %s\n' "$service" "$state"
  [[ "$state" == running* ]] || return 1
  [[ "$required_health" != true || "$state" == *' healthy' ]]
}

main() {
  local script_dir repository_root env_file compose_file hermes_home mode status_failed=0 assets_failed=0
  script_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
  repository_root="$(cd -- "${script_dir}/.." && pwd -P)"
  env_file="${URBAN_RADAR_ENV_FILE:-/opt/urban-radar/.env}"
  compose_file="${URBAN_RADAR_COMPOSE_FILE:-${repository_root}/compose.yaml}"
  hermes_home="${URBAN_RADAR_HERMES_HOME:-/opt/urban-radar/hermes}"
  mode="${1:-verify}"
  [[ "$mode" == verify || "$mode" == --assets-only || "$mode" == --status ]] || fail 'usage: verify-production.sh [--assets-only|--status]'
  [[ -r "$env_file" ]] || fail "production environment file is not readable: ${env_file}"
  [[ -f "$compose_file" ]] || fail "Compose file is not readable: ${compose_file}"

  if [[ "${URBAN_RADAR_HOST_ASSETS_ALREADY_VERIFIED:-}" == 1 ]]; then
    printf 'managed Hermes assets: already verified by deploy sync\n'
  else
    if ! host_test -f "$hermes_home/skills/urban-radar-editorial-style-v1/SKILL.md"; then
      printf 'managed Hermes skill: missing\n' >&2
      assets_failed=1
    else
      printf 'managed Hermes skill: OK\n'
    fi
    if ! host_test -x "$hermes_home/plugins/urban-radar-telegram-review-experiment/review_notify.py"; then
      printf 'managed Hermes sender: missing or non-executable\n' >&2
      assets_failed=1
    else
      printf 'managed Hermes sender: executable\n'
    fi
    if [[ "$assets_failed" -ne 0 ]]; then
      [[ "$mode" == --status ]] || fail 'managed Hermes asset verification failed'
      status_failed=1
    fi
  fi

  if [[ "$mode" == --assets-only ]]; then
    exit "$status_failed"
  fi

  command -v docker >/dev/null 2>&1 || fail 'docker is required'
  compose=(docker compose --project-directory "$repository_root" --env-file "$env_file" -f "$compose_file")
  if ! "${compose[@]}" config -q; then
    [[ "$mode" == --status ]] || fail 'Compose configuration is invalid'
    printf 'compose configuration: invalid\n' >&2
    exit 1
  fi

  if [[ "$mode" == --status ]]; then
    printf 'Compose services:\n'
    "${compose[@]}" ps || status_failed=1
    service_status postgres true || status_failed=1
    service_status hermes-gateway false || status_failed=1
    if ! "${compose[@]}" exec hermes-gateway /opt/hermes-venv/bin/hermes gateway status; then
      printf 'hermes-gateway: gateway status unavailable\n' >&2
      status_failed=1
    fi
    exit "$status_failed"
  fi

  service_status postgres true || fail 'postgres is not running and healthy'
  service_status hermes-gateway false || fail 'hermes-gateway is not running'
  "${compose[@]}" run --rm --no-deps --entrypoint sh urban-radar -lc '
    test -f /var/lib/hermes/skills/urban-radar-editorial-style-v1/SKILL.md &&
    test -x /var/lib/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py &&
    test "$(head -n 1 /var/lib/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py)" = "#!/opt/hermes-venv/bin/python"
  '
  "${compose[@]}" exec hermes-gateway /opt/hermes-venv/bin/hermes gateway status
  printf 'production verification: OK\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
