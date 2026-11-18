# Sourced by every scripts/ci/jobs/<area>/<job>.sh. Each of those scripts is the body of one CI job:
# ci.yml checks out the repository, runs .github/actions/setup for the job's toolchains, exports the
# job's matrix cell as MATRIX_<KEY> variables, and runs the script with bash. A script is a list of
# steps, each in a subshell, in the order the job used to run them.
#
# A step may write to $GITHUB_ENV or $GITHUB_PATH, as a workflow step does; ci_import_env, run
# after each step, applies what it wrote to the steps after it, as the runner would.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

# ci.yml passes the cell's values as one line of export statements (scripts/ci/plan.py writes it).
eval "${CI_CELL_EXPORTS:-}"

: "${RUNNER_TEMP:=$(mktemp -d "${TMPDIR:-/tmp}/ext-ci.XXXXXX")}"
if [ -z "${RUNNER_OS:-}" ]; then
  case "$(uname -s)" in
    Linux) RUNNER_OS=Linux ;;
    Darwin) RUNNER_OS=macOS ;;
    *) RUNNER_OS=Windows ;;
  esac
fi
: "${GITHUB_ENV:=$RUNNER_TEMP/github-env}"
: "${GITHUB_PATH:=$RUNNER_TEMP/github-path}"
: "${GITHUB_WORKSPACE:=$PWD}"
# On Windows the runner gives these as D:\a\... paths. The mixed form (D:/a/...) works both in Git
# Bash or MSYS2 and for the Windows programs the steps start.
if command -v cygpath >/dev/null 2>&1; then
  RUNNER_TEMP="$(cygpath -m "$RUNNER_TEMP")"
  GITHUB_ENV="$(cygpath -m "$GITHUB_ENV")"
  GITHUB_PATH="$(cygpath -m "$GITHUB_PATH")"
  GITHUB_WORKSPACE="$(cygpath -m "$GITHUB_WORKSPACE")"
fi
export RUNNER_TEMP RUNNER_OS GITHUB_ENV GITHUB_PATH GITHUB_WORKSPACE
touch "$GITHUB_ENV" "$GITHUB_PATH"
_ci_env_seen=$(wc -l <"$GITHUB_ENV")
_ci_path_seen=$(wc -l <"$GITHUB_PATH")

# Applies the lines a step appended to $GITHUB_ENV (NAME=value, or NAME<<DELIMITER ... DELIMITER)
# and $GITHUB_PATH (one directory per line, prepended).
ci_import_env() {
  local total line name delimiter value
  total=$(wc -l <"$GITHUB_ENV")
  if [ "$total" -gt "$_ci_env_seen" ]; then
    name=""
    while IFS= read -r line || [ -n "$line" ]; do
      if [ -n "$name" ]; then
        if [ "$line" = "$delimiter" ]; then
          export "$name=$value"
          name=""
        else
          value="${value:+$value$'\n'}$line"
        fi
      elif [[ "$line" == *"<<"* && "$line" != *=* ]]; then
        name="${line%%<<*}"; delimiter="${line#*<<}"; value=""
      elif [[ "$line" == *=* ]]; then
        export "${line%%=*}=${line#*=}"
      fi
    done < <(tail -n "+$((_ci_env_seen + 1))" "$GITHUB_ENV")
    _ci_env_seen=$total
  fi
  total=$(wc -l <"$GITHUB_PATH")
  if [ "$total" -gt "$_ci_path_seen" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
      if [ -n "$line" ]; then
        command -v cygpath >/dev/null 2>&1 && line="$(cygpath -u "$line")"
        PATH="$line:$PATH"
      fi
    done < <(tail -n "+$((_ci_path_seen + 1))" "$GITHUB_PATH")
    export PATH
    _ci_path_seen=$total
  fi
}

# Runs the script on stdin in the MSYS2 shell that .github/actions/setup installed (msys2.cmd),
# in the current directory, with bash's -e and pipefail, as a step with "shell: msys2 {0}" does.
ci_msys2() {
  # Already in the MSYS2 shell: ci.yml runs a whole MSYS2 cell's script there.
  if [ "${CI_IN_MSYS2:-}" = 1 ]; then
    bash -eo pipefail
    return
  fi
  local script
  script="$(mktemp "$RUNNER_TEMP/msys2-step.XXXXXX.sh")"
  { echo 'set -eo pipefail'; cat; } >"$script"
  CHERE_INVOKING=1 msys2.cmd "$(cygpath -w "$script")"
}

# Runs the script on stdin with PowerShell 7 (pwsh) or Windows PowerShell 5.1 (powershell), as a
# step with "shell: pwsh" or "shell: powershell" does: stop on the first error, and fail with the
# last native command's exit code.
_ci_powershell() {
  local exe="$1" script
  script="$(mktemp "$RUNNER_TEMP/ps-step.XXXXXX").ps1"
  {
    echo '$ErrorActionPreference = "Stop"'
    cat
    echo 'if ((Test-Path -LiteralPath variable:\LASTEXITCODE)) { exit $LASTEXITCODE }'
  } >"$script"
  "$exe" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$(cygpath -w "$script" 2>/dev/null || echo "$script")"
}
ci_pwsh() { _ci_powershell pwsh; }
ci_powershell() { _ci_powershell powershell; }
