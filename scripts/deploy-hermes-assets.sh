#!/usr/bin/env bash
# Synchronize versioned Urban Radar Hermes assets into the persistent runtime
# profile. This is a deploy-time operation, never a cron/runtime operation.
set -euo pipefail
umask 027

fail() {
  printf 'urban-radar Hermes asset sync: %s\n' "$*" >&2
  exit 1
}

[[ "${EUID}" -eq 0 ]] || fail 'run this deployment command as root (for UID/GID 10001 ownership)'

script_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repository_root="${URBAN_RADAR_REPOSITORY_ROOT:-$(cd -- "${script_dir}/.." && pwd -P)}"
hermes_home="${URBAN_RADAR_HERMES_HOME:-/opt/urban-radar/hermes}"
runtime_uid="${URBAN_RADAR_RUNTIME_UID:-10001}"
runtime_gid="${URBAN_RADAR_RUNTIME_GID:-10001}"
skill_name='urban-radar-editorial-style-v1'
plugin_name='urban-radar-telegram-review-experiment'

[[ "$repository_root" == /* && -d "$repository_root" ]] || fail "repository root is not an absolute directory"
[[ "$hermes_home" == /* && "$hermes_home" != / ]] || fail "Hermes home must be an absolute non-root directory"
[[ "$runtime_uid" =~ ^[0-9]+$ && "$runtime_gid" =~ ^[0-9]+$ ]] || fail 'runtime UID/GID must be numeric'

skill_source="${repository_root}/.hermes/skills/${skill_name}"
plugin_source="${repository_root}/hermes/telegram_review_plugin_experiment"
[[ -f "${skill_source}/SKILL.md" ]] || fail "missing managed skill source: ${skill_source}/SKILL.md"
for file in __init__.py plugin.yaml review_notify.py; do
  [[ -f "${plugin_source}/${file}" ]] || fail "missing managed plugin source: ${plugin_source}/${file}"
done

parent_dir="$(dirname "$hermes_home")"
[[ -d "$parent_dir" ]] || fail "Hermes home parent does not exist: ${parent_dir}"
stage="$(mktemp -d "${parent_dir}/.urban-radar-hermes-assets.XXXXXX")"
cleanup() { rm -rf "$stage"; }
trap cleanup EXIT

install -d -o "$runtime_uid" -g "$runtime_gid" -m 0750 "$hermes_home/skills" "$hermes_home/plugins"
install -d -m 0750 "$stage/skill" "$stage/plugin"
install -m 0640 "${skill_source}/SKILL.md" "$stage/skill/SKILL.md"
install -m 0640 "${plugin_source}/__init__.py" "$stage/plugin/__init__.py"
install -m 0640 "${plugin_source}/plugin.yaml" "$stage/plugin/plugin.yaml"
install -m 0750 "${plugin_source}/review_notify.py" "$stage/plugin/review_notify.py"
chown -R "$runtime_uid:$runtime_gid" "$stage"

replace_managed_directory() {
  local staged="$1" destination="$2" parent backup=''
  parent="$(dirname "$destination")"
  if [[ -e "$destination" || -L "$destination" ]]; then
    backup="${parent}/.$(basename "$destination").previous.$$"
    [[ ! -e "$backup" && ! -L "$backup" ]] || fail "unexpected deployment backup exists: ${backup}"
    mv "$destination" "$backup"
  fi
  if ! mv "$staged" "$destination"; then
    [[ -n "$backup" ]] && mv "$backup" "$destination"
    fail "could not install managed directory: ${destination}"
  fi
  [[ -z "$backup" ]] || rm -rf "$backup"
}

# Replacing only these two complete directories removes stale managed files,
# while preserving all other operator-owned Hermes skills, plugins and config.
replace_managed_directory "$stage/skill" "$hermes_home/skills/$skill_name"
replace_managed_directory "$stage/plugin" "$hermes_home/plugins/$plugin_name"

test -f "$hermes_home/skills/$skill_name/SKILL.md"
test -x "$hermes_home/plugins/$plugin_name/review_notify.py"
printf 'Urban Radar Hermes assets synchronized to %s\n' "$hermes_home"
