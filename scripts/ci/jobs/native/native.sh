#!/usr/bin/env bash
# native/native: the Go host on each OS: library modules, intelligence contracts, native processes and the graph, ONNX guests through CTN, credential stores, ctx and the Windows source installer.
. "$(dirname "$0")/../lib.sh"

# (action actions/checkout@v5)

# (action actions/setup-go@v6)

# (action actions/setup-dotnet@v5 if runner.os == 'Windows' || runner.os == 'Linux')

# Validate Windows system library
if [ "$RUNNER_OS" = Windows ]; then
(
go test -v ./res/system/... ./pkg/ext-go/engine/ledger/... ./pkg/ext-go/engine/ledger/recorder/... -timeout 5m
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
for module in cmd/ext src/ctx src/ctn src/ctn/inference src/ctn/model src/ctn/perception src/ctn/realtime src/cto src/cto/sandbox res/system res/int pkg/ext-go pkg/ext-go/engine pkg/ext-go/engine/ledger/recorder pkg/ext-go/cgoengine pkg/ext-go/hashicorp pkg/ext-go/wasm pkg/ext-go/yaegi pkg/ext-go/core pkg/ext-go/cengine adapters/wasm adapters/evm adapters/systemd adapters/pulseaudio adapters/alsa adapters/x11 adapters/ffmpeg adapters/wayland adapters/pipewire adapters/openai adapters/ollama adapters/anthropic adapters/gemini res/web/host; do
  (cd "$module" && go test ./...) || exit 1
done
)
ci_import_env

# Test intelligence contracts with race detector
(
go test -race ./res/int/inference/... ./res/int/perception/... ./src/ctn/perception/... ./src/ctn/realtime/... -timeout 5m
)
ci_import_env

# Cross-build inference adapters for Windows
(
for module in adapters/openai adapters/ollama adapters/anthropic adapters/gemini; do
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "$RUNNER_TEMP/$(basename "$module").exe" "./$module"
done
)
ci_import_env

# Validate isolated native processes
(
export EXT_GRAPH_PROCESS_NATIVE_TESTS="1"
cd res/system
go test -race -v ./service/process -run '^(TestNativeProcess|TestProcess)' -count=3 -timeout=5m
)
ci_import_env

# Native ledger collection and process projection are required by package-acceptance.sh.

# Validate native ONNX guest through CTN
if [ "$RUNNER_OS" = Windows ]; then
(
ci_pwsh <<'PWSH'
dotnet publish res/int/model/windows/Guest/Ext.ML.Windows.Guest.csproj -c Release -r win-x64 --self-contained true -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -o "$env:RUNNER_TEMP/onnx-guest"
$env:EXT_ONNX_GUEST_BINARY = Join-Path $env:RUNNER_TEMP 'onnx-guest/Ext.ML.Windows.Guest.exe'
go test ./src/ctn/internal/app -run '^TestModelRunThroughONNXGuest$' -count=1
PWSH
)
ci_import_env
fi

# Validate Linux ONNX guest through CTN
if [ "$RUNNER_OS" = Linux ]; then
(
dotnet run --project res/int/model/windows/Tests/Ext.ML.Windows.Tests.csproj
dotnet publish res/int/model/linux/Guest/Ext.ML.Linux.Guest.csproj -c Release -r linux-x64 --self-contained true -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true -o "$RUNNER_TEMP/onnx-guest"
EXT_ONNX_GUEST_BINARY="$RUNNER_TEMP/onnx-guest/Ext.ML.Linux.Guest" go test ./src/ctn/internal/app -run '^TestModelRunThroughONNXGuest$' -count=1
)
ci_import_env
fi

# Validate isolated native cookie credentials
if [ "$RUNNER_OS" = macOS ] || [ "$RUNNER_OS" = Windows ]; then
(
export EXT_COOKIE_NATIVE_CREDENTIAL_TESTS="1"
go test -v ./adapters/chromium/engine -run '^TestNativeCookieCredentials' -count=1
)
ci_import_env
fi

# Validate isolated native credential store
if [ "$RUNNER_OS" = macOS ] || [ "$RUNNER_OS" = Windows ]; then
(
export EXT_CREDENTIAL_NATIVE_TESTS="1"
go test -v ./adapters/keychain/store ./adapters/credman/store -run '^TestNative(KeychainCredential|CredentialManager)RoundTrip$' -count=1
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
