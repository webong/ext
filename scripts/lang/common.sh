# Shared by scripts/lang/<language>.sh. Each language script is the one place that says how that
# language is tested and packaged; CI workflows install a toolchain and call
#
#   scripts/lang/<language>.sh <command>
#
# so adding a language means adding a script and a matrix entry, not a workflow. Commands are
# language-specific; every script has "test", and lists its commands when run without one.
#
# Sourced, not run. It moves to the repository root and defines lang_main, which dispatches the
# first argument to a function named lang_<command>.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

# Sets LANG_TEMP to a scratch directory: under RUNNER_TEMP in CI, which the runner cleans up, or
# one the script removes when it exits. Call it directly, not in $(...), so the trap stays with
# the script.
lang_temp() {
  [ -n "${LANG_TEMP:-}" ] && return 0
  if [ -n "${RUNNER_TEMP:-}" ]; then
    LANG_TEMP="$(mktemp -d "$RUNNER_TEMP/ext-lang.XXXXXX")"
  else
    LANG_TEMP="$(mktemp -d "${TMPDIR:-/tmp}/ext-lang.XXXXXX")"
    trap 'rm -rf "$LANG_TEMP"' EXIT
  fi
}

lang_main() {
  local script="$1"; shift
  local commands
  commands="$(declare -F | awk '{print $3}' | sed -n 's/^lang_cmd_//p' | tr '\n' ' ')"
  if [ $# -eq 0 ] || ! declare -F "lang_cmd_$1" >/dev/null; then
    printf 'usage: %s <command>\ncommands: %s\n' "$script" "$commands" >&2
    exit 2
  fi
  local command="$1"; shift
  "lang_cmd_$command" "$@"
}
