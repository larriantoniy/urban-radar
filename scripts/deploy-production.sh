#!/usr/bin/env bash
# Normal-user production deployment. Only managed Hermes asset ownership crosses
# the explicit sudo boundary in deploy-hermes-assets.sh.
set -euo pipefail

fail() {
  printf 'urban-radar production deploy: %s\n' "$*" >&2
  exit 1
}

[[ "${EUID}" -ne 0 ]] || fail 'run make deploy as the normal deploy user, not root'

script_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repository_root="$(cd -- "${script_dir}/.." && pwd -P)"
env_file="${URBAN_RADAR_ENV_FILE:-/opt/urban-radar/.env}"
compose_file="${URBAN_RADAR_COMPOSE_FILE:-${repository_root}/compose.yaml}"
pull=true

if [[ "${1:-}" == '--no-pull' ]]; then
  pull=false
  shift
fi
[[ "$#" -eq 0 ]] || fail 'usage: deploy-production.sh [--no-pull]'
[[ -r "$env_file" ]] || fail "production environment file is not readable: ${env_file}"
[[ -f "$compose_file" ]] || fail "Compose file is not readable: ${compose_file}"
command -v git >/dev/null 2>&1 || fail 'git is required'
command -v docker >/dev/null 2>&1 || fail 'docker is required'
command -v sudo >/dev/null 2>&1 || fail 'sudo is required for managed Hermes asset ownership'
git -C "$repository_root" rev-parse --is-inside-work-tree >/dev/null || fail 'repository root is not a Git checkout'

compose=(docker compose --project-directory "$repository_root" --env-file "$env_file" -f "$compose_file")

if [[ "$pull" == true ]]; then
  [[ -z "$(git -C "$repository_root" status --porcelain)" ]] || fail 'working tree is dirty; commit or stash before deploy'
  git -C "$repository_root" pull --ff-only
fi

"${compose[@]}" config -q
sudo "$repository_root/scripts/deploy-hermes-assets.sh"
"${compose[@]}" build

# postgres is long-running persisted state: start it if absent, but never
# recreate a running database container as part of an application deploy.
"${compose[@]}" up -d --no-recreate postgres

postgres_id="$("${compose[@]}" ps -q postgres)"
[[ -n "$postgres_id" ]] || fail 'postgres container was not created'
for _ in $(seq 1 30); do
  if [[ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$postgres_id")" == 'healthy' ]]; then
    break
  fi
  sleep 2
done
[[ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$postgres_id")" == 'healthy' ]] || fail 'postgres did not become healthy'

# This is the only long-running application service. It must reload the new
# image and the synchronized plugin bind mount; urban-radar stays one-shot.
"${compose[@]}" up -d --no-deps --force-recreate hermes-gateway
URBAN_RADAR_HOST_ASSETS_ALREADY_VERIFIED=1 "$repository_root/scripts/verify-production.sh"
