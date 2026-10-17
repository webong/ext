#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT HUP INT TERM

# Preserve the caller's Go caches before isolating installation state. Module
# downloads are read-only and cannot be removed by the temporary-home cleanup.
GOCACHE=$(go env GOCACHE)
GOMODCACHE=$(go env GOMODCACHE)
export GOCACHE GOMODCACHE

export HOME="$TEST_ROOT/home"
export CTX_HOME="$TEST_ROOT/config"
export CTX_BIN_DIR="$TEST_ROOT/bin"
mkdir -p "$HOME" "$CTX_HOME/adapters" "$CTX_BIN_DIR" "$TEST_ROOT/project"

# Simulate an existing 0.7 shell installation. The native installer must replace
# the core while preserving configuration, projects, external adapters, and
# ctx-managed shims.
cat > "$CTX_BIN_DIR/ctx" <<'SH'
#!/usr/bin/env sh
[ "${1:-}" = version ] && { printf 'ctx 0.7.0\n'; exit 0; }
exit 2
SH
chmod +x "$CTX_BIN_DIR/ctx"
for engine in docker podman nerdctl; do
  cp "$ROOT/adapters/$engine/$engine" "$CTX_BIN_DIR/$engine"
  chmod +x "$CTX_BIN_DIR/$engine"
done
cp -R "$ROOT/examples/adapters/echo" "$CTX_HOME/adapters/echo"
cp -R "$ROOT/adapters/kube" "$CTX_HOME/adapters/kube"
cat > "$CTX_HOME/config.toml" <<'TOML'
docker_default = "alpha"

[profiles."client-a"]
aws_profile = "client-a"
TOML
cp "$CTX_HOME/config.toml" "$TEST_ROOT/config.before.toml"
printf '%s\n' 'profile = "client-a"' > "$TEST_ROOT/project/.ctx"

output=$("$ROOT/install.sh" </dev/null)
printf '%s\n' "$output" | grep -Fq 'no adapter catalog downloaded'

test -x "$CTX_BIN_DIR/ctx"
test "$("$CTX_BIN_DIR/ctx" version)" = 'ctx 0.9.0-dev'
for engine in docker podman nerdctl; do
  test -x "$CTX_BIN_DIR/$engine"
  test ! -e "$CTX_HOME/adapters/$engine"
done
test -d "$CTX_HOME/adapters/kube"
test -d "$CTX_HOME/adapters/echo"
test ! -e "$CTX_HOME/catalog/adapters/podman"
cmp "$TEST_ROOT/config.before.toml" "$CTX_HOME/config.toml"
grep -Fxq 'profile = "client-a"' "$TEST_ROOT/project/.ctx"

"$ROOT/install.sh" --adapters docker,git >/dev/null
for engine in docker podman nerdctl; do
  test -d "$CTX_HOME/adapters/$engine"
  CTX_HOME="$CTX_HOME" "$CTX_BIN_DIR/ctx" adapter ls manager | grep -Eq "^${engine}[[:space:]]+trusted"
done
test -d "$CTX_HOME/catalog/adapters/podman"
test -x "$CTX_HOME/adapters/git/ctx-git"
test ! -e "$CTX_BIN_DIR/git"
CTX_HOME="$CTX_HOME" "$CTX_BIN_DIR/ctx" adapter available | grep -Eq '^podman[[:space:]]+manager[[:space:]]+installed'
"$ROOT/install.sh" --minimal >/dev/null
test -d "$CTX_HOME/adapters/docker"
test -d "$CTX_HOME/catalog/adapters/docker"

# A non-interactive install keeps the complete catalog but selects no adapters.
minimal_home="$TEST_ROOT/minimal-home"
minimal_config="$TEST_ROOT/minimal-config"
minimal_bin="$TEST_ROOT/minimal-bin"
mkdir -p "$minimal_home"
output=$(HOME="$minimal_home" CTX_HOME="$minimal_config" CTX_BIN_DIR="$minimal_bin" \
  "$ROOT/install.sh" </dev/null)
printf '%s\n' "$output" | grep -Fq 'no adapter catalog downloaded'
test -x "$minimal_bin/ctx"
test ! -e "$minimal_config/catalog/adapters/docker"
test ! -e "$minimal_config/adapters/docker"

# Exercise the same checksum-verified bundle path used by the public curl
# installer without depending on a published GitHub release.
release_dir="$TEST_ROOT/release"
bundle_stage="$TEST_ROOT/bundle-stage"
mkdir -p "$release_dir" "$bundle_stage/ctx/bin"
cp "$CTX_BIN_DIR/ctx" "$bundle_stage/ctx/bin/ctx"
os=$(uname -s)
case "$os" in Darwin) os=darwin;; Linux) os=linux;; *) exit 1;; esac
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) exit 1;; esac
asset="ctx-$os-$arch.tar.gz"
tar -C "$bundle_stage" -czf "$release_dir/$asset" ctx
mkdir -p "$bundle_stage/ctx/adapters"
for adapter in docker podman nerdctl apple rancher_desktop orbstack docker_desktop firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium kube aws gcloud postgres mysql php jvm wasm evm claude_code codex git; do
  cp -R "$ROOT/adapters/$adapter" "$bundle_stage/ctx/adapters/$adapter"
done
go build -o "$bundle_stage/ctx/adapters/git/ctx-git" "$ROOT/adapters/git/native"
go build -o "$bundle_stage/ctx/adapters/wasm/ctx-wasm" "$ROOT/adapters/wasm/native"
go build -o "$bundle_stage/ctx/adapters/evm/ctx-evm" "$ROOT/adapters/evm/native"
case "$os" in
  darwin) cp -R "$ROOT/adapters/keychain" "$bundle_stage/ctx/adapters/keychain" ;;
  linux) cp -R "$ROOT/adapters/secret_service" "$bundle_stage/ctx/adapters/secret_service" ;;
esac
catalog_asset="ctx-adapters-$os-$arch.tar.gz"
tar -C "$bundle_stage" -czf "$release_dir/$catalog_asset" ctx/adapters
if command -v shasum >/dev/null 2>&1; then
  (cd "$release_dir" && shasum -a 256 "$asset" "$catalog_asset" > checksums.txt)
else
  (cd "$release_dir" && sha256sum "$asset" "$catalog_asset" > checksums.txt)
fi

release_home="$TEST_ROOT/release-home"
release_config="$TEST_ROOT/release-config"
release_bin="$TEST_ROOT/release-bin"
mkdir -p "$release_home"
HOME="$release_home" CTX_HOME="$release_config" CTX_BIN_DIR="$release_bin" \
  CTX_RELEASE_BASE="file://$release_dir" "$ROOT/install.sh" --minimal >/dev/null
test "$("$release_bin/ctx" version)" = 'ctx 0.9.0-dev'
test ! -e "$release_config/catalog/adapters/docker"
test ! -e "$release_config/adapters/docker"
HOME="$release_home" CTX_HOME="$release_config" CTX_BIN_DIR="$release_bin" \
  CTX_RELEASE_BASE="file://$release_dir" "$ROOT/install.sh" --adapters docker >/dev/null
test -d "$release_config/catalog/adapters/docker"
test -d "$release_config/adapters/docker"

# The installer must refuse a release whose archives do not match checksums.txt,
# or whose checksum entry is missing, and must leave nothing installed. Each case
# starts from a pristine copy of the good release so one failure cannot mask another.
expect_refusal() {
  label=$1
  expected=$2
  release=$3
  shift 3
  refuse_home="$TEST_ROOT/refuse-$label-home"
  refuse_config="$TEST_ROOT/refuse-$label-config"
  refuse_bin="$TEST_ROOT/refuse-$label-bin"
  mkdir -p "$refuse_home"
  if HOME="$refuse_home" CTX_HOME="$refuse_config" CTX_BIN_DIR="$refuse_bin" \
    CTX_RELEASE_BASE="file://$release" "$ROOT/install.sh" "$@" >"$TEST_ROOT/refuse-$label.out" 2>&1; then
    printf 'installer accepted a bad release (%s)\n' "$label" >&2
    exit 1
  fi
  grep -Fq "$expected" "$TEST_ROOT/refuse-$label.out" || {
    printf 'installer refused %s for the wrong reason:\n' "$label" >&2
    cat "$TEST_ROOT/refuse-$label.out" >&2
    exit 1
  }
  test ! -e "$refuse_bin/ctx"
  test ! -e "$refuse_config/adapters"
  test ! -e "$refuse_config/catalog"
}

tampered_core="$TEST_ROOT/release-tampered-core"
cp -R "$release_dir" "$tampered_core"
printf 'x' >> "$tampered_core/$asset"
expect_refusal tampered-core 'release checksum verification failed' "$tampered_core" --minimal

tampered_catalog="$TEST_ROOT/release-tampered-catalog"
cp -R "$release_dir" "$tampered_catalog"
printf 'x' >> "$tampered_catalog/$catalog_asset"
expect_refusal tampered-catalog 'release checksum verification failed' "$tampered_catalog" --adapters docker

missing_core="$TEST_ROOT/release-missing-core"
cp -R "$release_dir" "$missing_core"
grep -v " $asset\$" "$release_dir/checksums.txt" > "$missing_core/checksums.txt" || true
expect_refusal missing-core-checksum 'release checksum is missing' "$missing_core" --minimal

missing_catalog="$TEST_ROOT/release-missing-catalog"
cp -R "$release_dir" "$missing_catalog"
grep -v " $catalog_asset\$" "$release_dir/checksums.txt" > "$missing_catalog/checksums.txt" || true
expect_refusal missing-catalog-checksum 'release checksum is missing' "$missing_catalog" --adapters docker

# The old installer URL remains a functional streamed compatibility redirect.
compat_home="$TEST_ROOT/compat-home"
compat_config="$TEST_ROOT/compat-config"
compat_bin="$TEST_ROOT/compat-bin"
mkdir -p "$compat_home"
HOME="$compat_home" CTX_HOME="$compat_config" CTX_BIN_DIR="$compat_bin" \
  CTX_RELEASE_BASE="file://$release_dir" CTX_INSTALL_URL="file://$ROOT/install.sh" \
  sh -s -- --minimal < "$ROOT/install-native.sh" >/dev/null
test "$("$compat_bin/ctx" version)" = 'ctx 0.9.0-dev'

printf 'ctx native installer tests passed\n'
