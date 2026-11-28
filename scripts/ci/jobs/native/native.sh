#!/usr/bin/env bash
# native/native: the Go host on each OS: library modules, intelligence contracts, native processes and the graph, ONNX guests through CTN when EXT_ADAPTERS is given, ctx and the Windows source installer.
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action actions/setup-dotnet@v5 if runner.os == 'Windows' || runner.os == 'Linux')

# Validate Windows system library
if [ "$RUNNER_OS" = Windows ]; then
(
go test -v ./res/system/... ./pkg/ext-go/cengine/ledger/... -timeout 5m
)
ci_import_env
fi

# Required package acceptance: builds the canonical engine; no optional module skips.
(
EXT_ACCEPTANCE_BUILD_DIR="$RUNNER_TEMP/ext-native-engines" EXT_ACCEPTANCE_ENV_FILE="$GITHUB_ENV" bash scripts/package-acceptance.sh
python3 -m unittest discover -s scripts -p 'test_stage_cli_engine.py'
)
ci_import_env

# Test library modules
(
for module in cmd/ext src/ctx src/ctn src/ctn/inference src/ctn/model src/ctn/perception src/ctn/realtime src/cto src/cto/sandbox res/system src/ctn/contract pkg/ext-go/engine pkg/ext-go/cgoengine pkg/ext-go/hashicorp pkg/ext-go/wasm pkg/ext-go/yaegi pkg/ext-go/core pkg/ext-go/cengine res/web/host; do
  (cd "$module" && go test ./...) || exit 1
done
)
ci_import_env

# Test intelligence contracts with race detector
(
go test -race ./src/ctn/contract/inference/... ./src/ctn/contract/perception/... ./src/ctn/perception/... ./src/ctn/realtime/... -timeout 5m
)
ci_import_env

# Native ledger collection and process projection are exercised by package-acceptance.sh.

# Exercise the ledger-owned native private-volume boundary on POSIX runners.
if [ "$RUNNER_OS" != Windows ]; then
(
cmake -S lib/ledger -B "$RUNNER_TEMP/ext-ledger-volume" -DEXT_LEDGER_COMMAND=OFF
cmake --build "$RUNNER_TEMP/ext-ledger-volume" --target ledger_volume
ctest --test-dir "$RUNNER_TEMP/ext-ledger-volume" -R '^ledger_volume$' --output-on-failure
)
ci_import_env
fi
if [ "$RUNNER_OS" = Linux ]; then
(
cmake -S pkg/ext-linux/net -B "$RUNNER_TEMP/ext-system-net"
cmake --build "$RUNNER_TEMP/ext-system-net"
ctest --test-dir "$RUNNER_TEMP/ext-system-net" --output-on-failure
(cd res/system/service/network/linux/guest && go build -o "$RUNNER_TEMP/network-linux" .)
EXT_NETWORK_GUEST_BINARY="$RUNNER_TEMP/network-linux" go test ./tests/crosslang -run '^TestNetworkResourceGuest$' -count=1
)
ci_import_env
fi

# Validate native ONNX guest through CTN
if [ "$RUNNER_OS" = Windows ]; then
(
ci_pwsh <<'PWSH'
if ($env:EXT_ADAPTERS) {
  Copy-Item -Recurse "$env:EXT_ADAPTERS/onnxruntime" "$env:RUNNER_TEMP/onnx-source"
  python "$env:RUNNER_TEMP/onnx-source/build.py" win-x64 --output "$env:RUNNER_TEMP/onnx-guest"
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  $env:EXT_ONNX_GUEST_BINARY = Join-Path $env:RUNNER_TEMP 'onnx-guest/Ext.ML.Windows.Guest.exe'
  go test ./src/ctn/internal/app -run '^TestModelRunThroughONNXGuest$' -count=1
}
dotnet publish res/system/capture/clipboard/windows/Guest/Ext.System.Clipboard.Guest.csproj -c Release -r win-x64 --self-contained true -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -o "$env:RUNNER_TEMP/clipboard-guest"
$env:EXT_CLIPBOARD_GUEST_BINARY = Join-Path $env:RUNNER_TEMP 'clipboard-guest/Ext.System.Clipboard.Guest.exe'
go test ./src/ctx/internal/app -run '^TestWindowsClipboardGuestHandshake$' -count=1
dotnet publish res/system/service/network/windows/Guest/Ext.System.Network.Guest.csproj -c Release -r win-x64 --self-contained true -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -o "$env:RUNNER_TEMP/network-guest"
$env:EXT_NETWORK_GUEST_BINARY = Join-Path $env:RUNNER_TEMP 'network-guest/Ext.System.Network.Guest.exe'
go test ./tests/crosslang -run '^TestNetworkResourceGuest$' -count=1
PWSH
)
ci_import_env
fi

# Validate Linux ONNX guest through CTN
if [ "$RUNNER_OS" = Linux ]; then
(
dotnet run --project pkg/ext-windows/int/model/Tests/Ext.ML.Windows.Tests.csproj
if [ -n "${EXT_ADAPTERS:-}" ]; then
  cp -RL "$EXT_ADAPTERS/onnxruntime" "$RUNNER_TEMP/onnx-source"
  python3 "$RUNNER_TEMP/onnx-source/build.py" linux-x64 --output "$RUNNER_TEMP/onnx-guest"
  EXT_ONNX_GUEST_BINARY="$RUNNER_TEMP/onnx-guest/Ext.ML.Linux.Guest" go test ./src/ctn/internal/app -run '^TestModelRunThroughONNXGuest$' -count=1
fi
)
ci_import_env
fi

# Build the sole public CLI and its private product package entrypoints.
(
go build ./cmd/ext ./src/ctx/plugin ./src/ctn/plugin ./src/cto/plugin
)
ci_import_env

# Test Windows source installer
if [ "$RUNNER_OS" = Windows ]; then
(
ci_pwsh <<'PWSH'
./tests/windows_native_test.ps1
PWSH
)
ci_import_env
fi

# Test Windows source installer (Windows PowerShell 5.1)
if [ "$RUNNER_OS" = Windows ]; then
(
ci_powershell <<'PS'
if ($PSVersionTable.PSEdition -ne 'Desktop') { throw "expected Windows PowerShell, got $($PSVersionTable.PSEdition) $($PSVersionTable.PSVersion)" }
"Windows PowerShell $($PSVersionTable.PSVersion)"
./tests/windows_native_test.ps1
PS
)
ci_import_env
fi
