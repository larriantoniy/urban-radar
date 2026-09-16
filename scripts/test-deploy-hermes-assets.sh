#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  printf 'SKIP: deploy-hermes-assets test requires root to verify runtime ownership.\n'
  exit 0
fi

repo_root="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
sync_script="$repo_root/scripts/deploy-hermes-assets.sh"
tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

source_root="$tmp/repository"
runtime_home="$tmp/hermes"
numeric_uid=424242
numeric_gid=424242
if command -v getent >/dev/null 2>&1; then
  ! getent passwd "$numeric_uid" >/dev/null
  ! getent group "$numeric_gid" >/dev/null
fi
mkdir -p "$source_root/.hermes/skills/urban-radar-editorial-style-v1" "$source_root/hermes/telegram_review_plugin_experiment"
cp "$repo_root/.hermes/skills/urban-radar-editorial-style-v1/SKILL.md" "$source_root/.hermes/skills/urban-radar-editorial-style-v1/SKILL.md"
cp "$repo_root/hermes/telegram_review_plugin_experiment/__init__.py" "$repo_root/hermes/telegram_review_plugin_experiment/plugin.yaml" "$repo_root/hermes/telegram_review_plugin_experiment/review_notify.py" "$source_root/hermes/telegram_review_plugin_experiment/"

# This deliberately uses numeric identities without corresponding host account
# names. It regresses the production failure from `install -o 10001`.
URBAN_RADAR_REPOSITORY_ROOT="$source_root" URBAN_RADAR_HERMES_HOME="$runtime_home" URBAN_RADAR_RUNTIME_UID="$numeric_uid" URBAN_RADAR_RUNTIME_GID="$numeric_gid" "$sync_script"
test -f "$runtime_home/skills/urban-radar-editorial-style-v1/SKILL.md"
test -x "$runtime_home/plugins/urban-radar-telegram-review-experiment/review_notify.py"
test ! -e "$runtime_home/plugins/urban-radar-telegram-review-experiment/test_contract.py"
[[ "$(stat -c '%u:%g' "$runtime_home/skills/urban-radar-editorial-style-v1")" == "$numeric_uid:$numeric_gid" ]]
[[ "$(stat -c '%u:%g' "$runtime_home/plugins/urban-radar-telegram-review-experiment")" == "$numeric_uid:$numeric_gid" ]]

mkdir -p "$runtime_home/skills/operator-skill" "$runtime_home/plugins/operator-plugin"
printf operator >"$runtime_home/skills/operator-skill/keep"
printf operator >"$runtime_home/plugins/operator-plugin/keep"
printf stale >"$runtime_home/plugins/urban-radar-telegram-review-experiment/stale.py"
printf updated >"$source_root/.hermes/skills/urban-radar-editorial-style-v1/SKILL.md"

URBAN_RADAR_REPOSITORY_ROOT="$source_root" URBAN_RADAR_HERMES_HOME="$runtime_home" URBAN_RADAR_RUNTIME_UID="$numeric_uid" URBAN_RADAR_RUNTIME_GID="$numeric_gid" "$sync_script"
grep -qx 'updated' "$runtime_home/skills/urban-radar-editorial-style-v1/SKILL.md"
test ! -e "$runtime_home/plugins/urban-radar-telegram-review-experiment/stale.py"
test -f "$runtime_home/skills/operator-skill/keep"
test -f "$runtime_home/plugins/operator-plugin/keep"
printf 'PASS: deploy-hermes-assets mechanics\n'
