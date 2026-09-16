#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck source=verify-production.sh
source "$repo_root/scripts/verify-production.sh"

tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

# Exercise the non-root branch without requiring sudo or a host account for a
# numeric UID/GID. The fake sudo executes exactly the positional shell form
# used by host_test.
sudo() { "$@"; }
skill="$tmp/SKILL.md"
sender="$tmp/review_notify.py"
printf skill >"$skill"
printf '#!/opt/hermes-venv/bin/python\n' >"$sender"
chmod 0640 "$skill"
chmod 0750 "$sender"

host_test -f "$skill"
host_test -x "$sender"

chmod 0640 "$sender"
if host_test -x "$sender"; then
  printf 'expected non-executable sender to fail host_test\n' >&2
  exit 1
fi

# Numeric ownership has no effect on executable semantics. Full ownership
# regression is additionally covered by test-deploy-hermes-assets.sh when run
# as root because chown requires privileges.
if [[ "${EUID}" -eq 0 ]]; then
  chown 424242:424242 "$sender"
  [[ "$(stat -c '%u:%g' "$sender")" == '424242:424242' ]]
  chmod 0750 "$sender"
  host_test -x "$sender"
else
  printf 'SKIP: numeric ownership assertion requires root.\n'
fi

printf 'PASS: verify-production host_test semantics\n'
