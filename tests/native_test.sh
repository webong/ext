#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf "$TEST_ROOT"' EXIT HUP INT TERM

export HOME="$TEST_ROOT/home"
export CTX_HOME="$HOME/.config/ctx"
export CTX_BIN_DIR="$TEST_ROOT/bin"
export CTX_PLATFORM=Darwin
mkdir -p "$HOME" "$CTX_BIN_DIR" "$TEST_ROOT/fake-bin" "$TEST_ROOT/project" \
  "$TEST_ROOT/mapped" "$HOME/Library/Application Support/Google/Chrome/Default" \
  "$HOME/Library/Application Support/Google/Chrome/Profile 1"
printf '{}\n' > "$HOME/Library/Application Support/Google/Chrome/Default/Preferences"
printf '{}\n' > "$HOME/Library/Application Support/Google/Chrome/Profile 1/Preferences"

for tool in docker podman nerdctl container kubectl aws gcloud open shell; do
  cp "$ROOT/tests/fake-$tool" "$TEST_ROOT/fake-bin/$tool"
done
cp "$ROOT/tests/fake-docker-manager" "$TEST_ROOT/fake-bin/docker-manager"
cp "$ROOT/tests/fake-rdctl" "$TEST_ROOT/fake-bin/rdctl"
cp "$ROOT/tests/fake-orbctl" "$TEST_ROOT/fake-bin/orbctl"
cp "$ROOT/tests/fake-docker-desktop-cli" "$TEST_ROOT/fake-bin/docker-desktop-cli"
mv "$TEST_ROOT/fake-bin/shell" "$TEST_ROOT/fake-bin/custom-shell"
cp "$ROOT/tests/fake-postgres" "$TEST_ROOT/fake-bin/psql"
cp "$ROOT/tests/fake-mysql" "$TEST_ROOT/fake-bin/mysql"
chmod +x "$TEST_ROOT/fake-bin"/*
export PATH="$CTX_BIN_DIR:$TEST_ROOT/fake-bin:$PATH"
export CTX_RANCHER_RDCTL="$TEST_ROOT/fake-bin/rdctl"
export CTX_ORBSTACK_CLI="$TEST_ROOT/fake-bin/orbctl"
export CTX_DOCKER_DESKTOP_CLI="$TEST_ROOT/fake-bin/docker-desktop-cli"

GOCACHE=${GOCACHE:-/tmp/ctx-go-build-cache} GOMODCACHE=${GOMODCACHE:-/tmp/ctx-go-mod-cache} \
  go build -o "$CTX_BIN_DIR/ctx" "$ROOT/src/ctx/cmd/ctx"

mkdir -p "$CTX_HOME/catalog/adapters"
for adapter in docker podman nerdctl apple rancher_desktop orbstack docker_desktop firefox chrome kube aws gcloud postgres mysql git; do
  cp -R "$ROOT/adapters/$adapter" "$CTX_HOME/catalog/adapters/$adapter"
done
GOCACHE=${GOCACHE:-/tmp/ctx-go-build-cache} GOMODCACHE=${GOMODCACHE:-/tmp/ctx-go-mod-cache} \
  go build -o "$CTX_HOME/catalog/adapters/git/ctx-git" "$ROOT/adapters/git/native"
case "$(uname -s)" in
  Darwin) credential_adapter=keychain; credential_executable=ctx-keychain ;;
  Linux) credential_adapter=secret_service; credential_executable=ctx-secret-service ;;
  *) credential_adapter= ;;
esac
if [ -n "$credential_adapter" ]; then
  cp -R "$ROOT/adapters/$credential_adapter" "$CTX_HOME/catalog/adapters/$credential_adapter"
  GOCACHE=${GOCACHE:-/tmp/ctx-go-build-cache} GOMODCACHE=${GOMODCACHE:-/tmp/ctx-go-mod-cache} \
    go build -o "$CTX_HOME/catalog/adapters/$credential_adapter/$credential_executable" "$ROOT/adapters/$credential_adapter/native"
fi
ctx adapter available | grep -Eq '^docker[[:space:]]+manager[[:space:]]+available'
ctx setup --adapters docker,podman,nerdctl,apple,rancher_desktop,orbstack,docker_desktop,firefox,chrome,kube,aws,gcloud,postgres,mysql,git >/dev/null
for adapter in docker podman nerdctl apple rancher_desktop orbstack docker_desktop firefox chrome kube aws gcloud postgres mysql git; do
  ctx adapter ls | grep -Eq "^${adapter}[[:space:]]+trusted"
done
if [ -n "$credential_adapter" ]; then
  ctx adapter ls | grep -Eq "^${credential_adapter}[[:space:]]+trusted"
fi
for engine in docker podman nerdctl; do test -x "$CTX_BIN_DIR/$engine"; done
ctx adapter available | grep -Eq '^docker[[:space:]]+manager[[:space:]]+installed'
ctx adapter remove nerdctl >/dev/null
test ! -e "$CTX_BIN_DIR/nerdctl"
ctx adapter add nerdctl >/dev/null
test -x "$CTX_BIN_DIR/nerdctl"
ctx adapter refresh >/dev/null

ctx adapter test "$ROOT/examples/adapters/echo" | grep -Fq 'adapter echo satisfies ctx adapter API v2.0'
ctx adapter install "$ROOT/examples/adapters/echo" >/dev/null
if ctx adapter doctor echo >/dev/null 2>&1; then
  printf 'native core executed an untrusted adapter\n' >&2
  exit 1
fi
ctx adapter trust echo >/dev/null
ctx adapter inspect echo | grep -Fq 'api:          2.0'
ctx adapter inspect echo | grep -Fq 'state:        trusted'

mkdir -p "$HOME/Library/Application Support/Firefox"
printf '%s\n' '[Profile0]' 'Name=client-a' > "$HOME/Library/Application Support/Firefox/profiles.ini"

git init -q "$TEST_ROOT/project"
cd "$TEST_ROOT/project"
ctx run git hooks install pre-commit -- true >/dev/null
ctx run git hooks status pre-commit | grep -Fq 'pre-commit: ctx-managed'
test -x .git/hooks/pre-commit
ctx run git hooks remove pre-commit >/dev/null
test ! -e .git/hooks/pre-commit
ctx set docker alpha >/dev/null
ctx set podman red >/dev/null
ctx set nerdctl k8s.io >/dev/null
ctx set apple local >/dev/null
ctx set kube production --namespace payments >/dev/null
ctx set aws client-a >/dev/null
ctx set gcloud client-a >/dev/null
ctx set browser firefox:client-a >/dev/null
ctx set postgres client-a-dev >/dev/null
ctx set mysql client-a >/dev/null
ctx set echo staging >/dev/null
grep -Fxq 'docker = "alpha"' .ctx
grep -Fxq 'podman = "red"' .ctx
grep -Fxq 'nerdctl = "k8s.io"' .ctx
git check-ignore -q .ctx

test "$(ctx real docker)" = "$TEST_ROOT/fake-bin/docker"
test "$(CTX_SHELL=custom-shell ctx shell)" = 'custom shell'
test "$(ctx shell --shell custom-shell)" = 'custom shell'
ctx completion powershell | grep -Fq 'Register-ArgumentCompleter'
test "$(ctx run docker ps)" = '--context alpha ps'
test "$(docker ps)" = '--context alpha ps'
test "$(DOCKER_CONTEXT=environment ctx status docker)" = 'docker: environment (DOCKER_CONTEXT)'
test "$(DOCKER_CONTEXT=environment docker ps)" = 'ps'
test "$(docker --context beta ps)" = '--context beta ps'
test "$(ctx run podman ps)" = '--connection red ps'
test "$(podman ps)" = '--connection red ps'
test "$(CONTAINER_CONNECTION=blue podman ps)" = 'ps'
test "$(podman --connection blue ps)" = '--connection blue ps'
test "$(ctx run nerdctl ps)" = '--namespace k8s.io ps'
test "$(nerdctl ps)" = '--namespace k8s.io ps'
test "$(CONTAINERD_NAMESPACE=default nerdctl ps)" = 'ps'
test "$(nerdctl --namespace default ps)" = '--namespace default ps'
test "$(ctx run container list)" = 'list'
test "$(ctx run apple list)" = 'list'
ctx clear docker >/dev/null
test "$(ctx run docker ps)" = 'ps'
test "$(docker ps)" = 'ps'
ctx set docker alpha >/dev/null
ctx ls browser | grep -Fxq 'firefox:client-a'
ctx ls browser | grep -Fxq 'chrome:Profile 1'
test "$(ctx run kubectl get pods)" = '--context production --namespace payments get pods'
test "$(ctx run kubectl --context staging -n operations get pods)" = '--context staging -n operations get pods'
test "$(ctx run aws sts get-caller-identity)" = '--profile client-a sts get-caller-identity'
test "$(AWS_PROFILE=default ctx run aws sts get-caller-identity)" = 'sts get-caller-identity'
test "$(ctx run gcloud projects list)" = '--configuration client-a projects list'
test "$(CLOUDSDK_ACTIVE_CONFIG_NAME=default ctx run gcloud projects list)" = 'projects list'
test "$(ctx run psql app)" = 'PGSERVICE=client-a-dev app'
test "$(PGSERVICE=override ctx run psql app)" = 'PGSERVICE=override app'
test "$(ctx run mysql app)" = '--login-path=client-a app'
test "$(ctx run mysql --login-path=override app)" = '--login-path=override app'
test "$(ctx run echo hello)" = 'run[staging] hello'
test "$(ctx open https://example.test)" = '-na Firefox --args -P client-a https://example.test'
ctx set browser chrome:'Profile 1' >/dev/null
test "$(ctx open https://example.test)" = '-na Google Chrome --args --profile-directory=Profile 1 https://example.test'
ctx set browser firefox:client-a >/dev/null
ctx adapter inspect firefox | grep -Fq 'runtime:      browser'
ctx adapter inspect firefox | grep -Fq 'surfaces:     web'
ctx adapter inspect docker | grep -Fq 'runtime:      manager'
ctx adapter inspect docker | grep -Fq 'surfaces:     shell'
ctx adapter inspect docker | grep -Fq 'default:      true'
ctx adapter ls manager | grep -Eq '^apple[[:space:]]+trusted'
ctx adapter ls manager | grep -Eq '^docker[[:space:]]+trusted'
ctx ls manager | grep -Fq 'docker:alpha'
ctx ls manager | grep -Fq 'apple:local'
ctx manager apps | grep -Fxq rancher_desktop
ctx manager apps | grep -Fxq orbstack
ctx manager apps | grep -Fxq docker_desktop
if ctx manager apps | grep -Fxq podman; then
  printf 'engine provider was listed as a desktop app\n' >&2
  exit 1
fi
test "$(ctx manager app rancher_desktop status)" = 'Rancher Desktop is running'
ctx manager app rancher_desktop doctor | grep -Fq 'containerd engine'
test "$(ctx manager app orbstack status)" = 'Running'
ctx manager app orbstack doctor | grep -Fq 'OrbStack doctor clean'
test "$(ctx manager app docker_desktop status)" = 'Running'
ctx manager app docker_desktop doctor | grep -Fq 'Docker Desktop status: Running'
ctx manager doctor rancher_desktop | grep -Fq 'rancher_desktop:'
mkdir -p "$HOME/Library/Application Support/rancher-desktop/lima/0"
printf 'EXT4-fs (vda1): potential data loss error -5\n' > "$HOME/Library/Application Support/rancher-desktop/lima/0/serialv.log"
printf '{"status":{"vsock":{"type":"failed","reason":"Failed to wait for guest SSH server"}}}\n' > "$HOME/Library/Application Support/rancher-desktop/lima/0/ha.stdout.log"
mkdir -p "$HOME/Library/Logs/rancher-desktop"
printf 'Rancher Desktop was unable to start:\nlimactl shell 0 sudo /sbin/rc-service --ifnotstarted k3s start\nSegmentation fault\n * ERROR: k3s failed to start\n' > "$HOME/Library/Logs/rancher-desktop/background.log"
mkdir -p "$HOME/Library/Preferences/rancher-desktop"
printf '{"containerEngine":{"name":"containerd"}}\n' > "$HOME/Library/Preferences/rancher-desktop/settings.json"
if FAKE_RDCTL_BROKEN=1 FAKE_RDCTL_SETTINGS_FAIL=1 ctx manager app rancher_desktop doctor > "$TEST_ROOT/rancher-diagnostic.txt"; then
  printf 'Rancher Desktop adapter missed a broken VM\n' >&2
  exit 1
fi
grep -Fq 'EXT4 filesystem errors' "$TEST_ROOT/rancher-diagnostic.txt"
grep -Fq 'did not reach guest SSH' "$TEST_ROOT/rancher-diagnostic.txt"
grep -Fq 'k3s segfaulted while starting' "$TEST_ROOT/rancher-diagnostic.txt"
grep -Fq 'containerd engine' "$TEST_ROOT/rancher-diagnostic.txt"
printf 'Rancher Desktop was unable to start:\nlimactl start failed before guest SSH\n' >> "$HOME/Library/Logs/rancher-desktop/background.log"
FAKE_RDCTL_BROKEN=1 FAKE_RDCTL_SETTINGS_FAIL=1 ctx manager app rancher_desktop doctor > "$TEST_ROOT/rancher-latest-diagnostic.txt" || :
if grep -Fq 'k3s segfaulted while starting' "$TEST_ROOT/rancher-latest-diagnostic.txt"; then
  printf 'Rancher Desktop adapter attributed an earlier k3s crash to a later failure\n' >&2
  exit 1
fi
mkdir -p "$TEST_ROOT/manager-plugins" "$HOME/.docker/cli-plugins"
cp "$ROOT/tests/fake-docker" "$TEST_ROOT/manager-plugins/docker-buildx"
cp "$ROOT/tests/fake-docker" "$HOME/.docker/cli-plugins/docker-buildx"
ctx manager add managed --provider docker --selection alpha \
  --command "$TEST_ROOT/fake-bin/docker-manager" \
  --plugin-dir "$TEST_ROOT/manager-plugins" >/dev/null
ctx manager doctor managed | grep -Fq 'ok   @managed toolchain configured'
ctx manager add offline --provider docker --selection unavailable --offline >/dev/null
ctx manager show offline | grep -Fq 'selection:   unavailable'
ctx manager remove offline >/dev/null
ctx set docker @managed >/dev/null
test "$(ctx run docker ps)" = 'managed --context alpha ps'
test "$(docker ps)" = 'managed --context alpha ps'
test "$(docker buildx version)" = "managed --context alpha buildx version plugin=$TEST_ROOT/manager-plugins/docker-buildx"
ctx set docker alpha >/dev/null
mv "$TEST_ROOT/fake-bin/container" "$TEST_ROOT/fake-bin/container-disabled"
ctx ls manager | grep -Fq 'docker:alpha'
mv "$TEST_ROOT/fake-bin/container-disabled" "$TEST_ROOT/fake-bin/container"
ctx doctor | grep -Fq 'ok   computer kube production'
ctx doctor | grep -Fq 'ok   manager docker alpha'
ctx doctor | grep -Fq 'ok   browser firefox:client-a'

test "$(ctx build --cache-ref registry.example/app:buildcache -- --tag registry.example/app:dev .)" = '--context alpha buildx build --cache-from type=registry,ref=registry.example/app:buildcache --cache-to type=registry,ref=registry.example/app:buildcache,mode=max --tag registry.example/app:dev .'
ctx clear docker >/dev/null
test "$(ctx build --cache-ref registry.example/app:buildcache -- --tag registry.example/app:dev .)" = 'buildx build --cache-from type=registry,ref=registry.example/app:buildcache --cache-to type=registry,ref=registry.example/app:buildcache,mode=max --tag registry.example/app:dev .'
ctx set docker alpha >/dev/null
test "$(ctx build podman --cache-ref registry.example/app:podman-cache -- --tag registry.example/app:dev .)" = '--connection red build --layers --cache-from registry.example/app:podman-cache --cache-to registry.example/app:podman-cache --tag registry.example/app:dev .'
test "$(ctx build nerdctl --cache-ref registry.example/app:nerdctl-cache -- --tag registry.example/app:dev .)" = '--namespace k8s.io build --cache-from type=registry,ref=registry.example/app:nerdctl-cache --cache-to type=registry,ref=registry.example/app:nerdctl-cache,mode=max --tag registry.example/app:dev .'
test "$(ctx share:manager image sync alpha beta registry.example/app:dev)" = "$(printf '%s\n%s' '--context alpha image push registry.example/app:dev' '--context beta image pull registry.example/app:dev')"
ctx share:manager image sync --tar alpha beta registry.example/app:dev >/dev/null
image_copy_output=$(ctx share:manager image copy docker:alpha podman:red registry.example/app:dev)
printf '%s\n' "$image_copy_output" | grep -Eq '^--context alpha image save -o .+ registry.example/app:dev$'
printf '%s\n' "$image_copy_output" | grep -Eq '^--connection red image load -i .+$'
image_copy_output=$(ctx share:manager image copy podman:red docker:beta registry.example/app:dev)
printf '%s\n' "$image_copy_output" | grep -Eq '^--connection red image save -o .+ registry.example/app:dev$'
printf '%s\n' "$image_copy_output" | grep -Eq '^--context beta image load -i .+$'
image_copy_output=$(ctx share:manager image copy docker:alpha nerdctl:k8s.io registry.example/app:dev)
printf '%s\n' "$image_copy_output" | grep -Eq '^--context alpha image save -o .+ registry.example/app:dev$'
printf '%s\n' "$image_copy_output" | grep -Eq '^--namespace k8s.io load -i .+$'
image_copy_output=$(ctx share:manager image copy nerdctl:k8s.io apple:local registry.example/app:dev)
printf '%s\n' "$image_copy_output" | grep -Eq '^--namespace k8s.io save -o .+ registry.example/app:dev$'
printf '%s\n' "$image_copy_output" | grep -Eq '^image load --input .+$'
if CTX_TEST_APPLE_VERSION=1.2.0 ctx share:manager image copy docker:alpha apple:local registry.example/app:dev >/dev/null 2>&1; then
  printf 'native core allowed an unsafe Apple Container image import\n' >&2
  exit 1
fi
test "$(ctx share:manager volume export alpha data 2>/dev/null)" = '--context alpha run --rm -v data:/volume:ro alpine:3.21 tar -C /volume -cf - .'
test "$(ctx share:manager volume import alpha restored </dev/null)" = "$(printf '%s\n%s' '--context alpha volume create restored' '--context alpha run --rm -i -v restored:/volume alpine:3.21 tar -C /volume -xf -')"
test "$(ctx share:manager volume copy docker:alpha podman:red data restored-podman 2>/dev/null)" = "$(printf '%s\n%s' '--connection red volume create restored-podman' '--connection red run --rm -i -v restored-podman:/volume alpine:3.21 tar -C /volume -xf -')"
test "$(ctx share:manager volume copy podman:red docker:beta data restored-docker 2>/dev/null)" = "$(printf '%s\n%s' '--context beta volume create restored-docker' '--context beta run --rm -i -v restored-docker:/volume alpine:3.21 tar -C /volume -xf -')"
test "$(ctx share:manager volume copy docker:alpha nerdctl:k8s.io data restored-nerdctl 2>/dev/null)" = "$(printf '%s\n%s' '--namespace k8s.io volume create restored-nerdctl' '--namespace k8s.io run --rm -i -v restored-nerdctl:/volume alpine:3.21 tar -C /volume -xf -')"
test "$(ctx share:manager volume copy nerdctl:k8s.io apple:local data restored-apple 2>/dev/null)" = "$(printf '%s\n%s' 'volume create restored-apple' 'run --rm -i -v restored-apple:/volume alpine:3.21 tar -C /volume -xf -')"
if ctx share:manager volume import alpha exists </dev/null >/dev/null 2>&1; then
  printf 'native core imported into an existing volume\n' >&2
  exit 1
fi
if ctx share:manager volume copy docker:alpha podman:red data exists </dev/null >/dev/null 2>&1; then
  printf 'native core copied into an existing volume\n' >&2
  exit 1
fi

ctx profile set client-b aws_profile default >/dev/null
ctx profile set client-b shell_path /client/bin >/dev/null
ctx profile env client-b APP_ENV development >/dev/null
ctx profile env client-b CTX_SHELL custom-shell >/dev/null
ctx profile show client-b | grep -Fq 'aws_profile = "default"'
ctx profile show client-b | grep -Fq '[env]'
ctx profile use client-b >/dev/null
ctx clear aws >/dev/null
test "$(ctx shell)" = 'custom shell'
test "$(ctx run aws sts get-caller-identity)" = '--profile default sts get-caller-identity'
test "$(ctx run docker print-env)" = 'development'
test "$(ctx run -- sh -c 'printf %s "$APP_ENV"')" = 'development'
case "$(ctx env)" in *'PATH=/client/bin:'*) ;; *) exit 1 ;; esac
ctx profile env-unset client-b APP_ENV >/dev/null
ctx profile unset client-b shell_path >/dev/null
ctx profile clear >/dev/null

# Central mappings and global fallbacks remain compatible with the shell-era
# configuration format.
printf '\n[projects."%s"]\ndocker = "beta"\npodman = "blue"\nnerdctl = "default"\n' "$TEST_ROOT/mapped" >> "$CTX_HOME/config.toml"
cd "$TEST_ROOT/mapped"
test "$(docker ps)" = '--context beta ps'
test "$(podman ps)" = '--connection blue ps'
test "$(nerdctl ps)" = '--namespace default ps'

cd "$HOME"
test "$(docker ps)" = 'ps'
test "$(podman ps)" = 'ps'
test "$(nerdctl ps)" = 'ps'
ctx set docker alpha --global >/dev/null
ctx set podman red --global >/dev/null
ctx set nerdctl default --global >/dev/null
test "$(docker ps)" = '--context alpha ps'
test "$(podman ps)" = '--connection red ps'
test "$(nerdctl ps)" = '--namespace default ps'
if ctx set docker missing >/dev/null 2>&1; then
  printf 'native core accepted an invalid Docker context\n' >&2
  exit 1
fi
if ctx set podman missing >/dev/null 2>&1; then
  printf 'native core accepted an invalid Podman connection\n' >&2
  exit 1
fi
if ctx set nerdctl missing >/dev/null 2>&1; then
  printf 'native core accepted an invalid nerdctl namespace\n' >&2
  exit 1
fi

ctx set echo local >/dev/null
printf '\n# changed after trust\n' >> "$CTX_HOME/adapters/echo/ctx-echo"
if ctx run echo changed >/dev/null 2>&1; then
  printf 'changed adapter retained trust\n' >&2
  exit 1
fi
ctx adapter trust echo >/dev/null
test "$(ctx run echo trusted-again)" = 'run[local] trusted-again'
ctx clear echo >/dev/null
ctx adapter remove echo >/dev/null
test ! -e "$CTX_HOME/adapters/echo"

printf 'ctx native adapter tests passed\n'
