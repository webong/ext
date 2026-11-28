#!/usr/bin/env bash
# Go plugin modules and embedding examples.
. "$(dirname "$0")/../lib.sh"

# Default Go hosts and package stores execute the canonical C libraries.
cmake -S lib/plugin -B "$RUNNER_TEMP/sdk-plugin" -DCMAKE_BUILD_TYPE=Release -DEXT_BUILD_SHARED=ON
cmake --build "$RUNNER_TEMP/sdk-plugin" --target ext_plugin_host_shared --config Release -j 4
cmake -S lib/ledger -B "$RUNNER_TEMP/sdk-ledger" -DCMAKE_BUILD_TYPE=Release -DEXT_LEDGER_SHARED=ON
cmake --build "$RUNNER_TEMP/sdk-ledger" --config Release -j 4
case "$(go env GOOS)" in
  linux) library_suffix=so ;;
  darwin) library_suffix=dylib ;;
  *) echo 'SDK job requires a POSIX shared-library build' >&2; exit 1 ;;
esac
export EXT_PLUGIN_LIBRARY="$RUNNER_TEMP/sdk-plugin/libext_plugin_host.$library_suffix"
export EXT_LEDGER_LIBRARY="$RUNNER_TEMP/sdk-ledger/libext_ledger.$library_suffix"
test -f "$EXT_PLUGIN_LIBRARY"
test -f "$EXT_LEDGER_LIBRARY"

for module in pkg/ext-go/engine pkg/ext-go/cgoengine pkg/ext-go/hashicorp pkg/ext-go/wasm pkg/ext-go/yaegi pkg/ext-go/core pkg/ext-go/cengine src/ctx/web examples/plugins/go examples/ledger/go examples/web/go; do
  (cd "$module" && go test -race ./...)
done
go test -race ./pkg/ext-go/hashicorp/cmd/ext-plugin

go run ./examples/plugins/go/plugin-sdk --backend jsonline
go run ./examples/plugins/go/plugin-sdk --backend inprocess
go run ./pkg/ext-go/hashicorp/cmd/ext-plugin inspect --root examples/plugins/go/plugin-package --entry main examples/plugins/go/plugin-package/plugin.json

# The default store must still execute C when the Go binary is built without cgo.
CGO_ENABLED=0 go test ./pkg/ext-go/cgoengine/plugin -run 'PackageStore|LayeredPackageStore|Trust|InstallArchive|ResolveEntrypoint'
