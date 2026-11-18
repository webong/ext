#!/usr/bin/env bash
# The .NET language package (pkg/ext-dotnet): the guest SDK ExtIO. Needs the .NET 8 SDK and go.
# The host binding, ExtIO.Host, runs the shared host cases in scripts/host-cases.sh; the Windows
# libraries of res/ are scripts/lang/dotnet.sh.
#   test   the fixtures and unit tests, the guest under the Go process host (the conformance cases
#          and the frames suite), and the ExtIO package packed at 0.0.0-ci
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  local guest dotnet
  dotnet="$(command -v dotnet)"
  lang_temp
  dotnet run --project pkg/ext-dotnet/ExtIO.Tests -c Release
  dotnet build pkg/ext-dotnet/ExtIO.Conformance -c Release -o "$LANG_TEMP/guest"
  guest="$LANG_TEMP/guest/ExtIO.Conformance.dll"
  (cd pkg/ext-go/engine && EXT_DOTNET="$dotnet" EXT_DOTNET_GUEST="$guest" go test -count=1 -run 'TestDotnet' -v ./plugintest)
  (cd tests/crosslang && EXT_DOTNET="$dotnet" EXT_DOTNET_GUEST="$guest" EXT_FRAMES_REQUIRED=Dotnet go test -count=1 -v -run 'TestDotnetFramesGuest' .)
  dotnet pack pkg/ext-dotnet/ExtIO/ExtIO.csproj -c Release -p:Version=0.0.0-ci -o "$LANG_TEMP/nupkg"
  if unzip -l "$LANG_TEMP"/nupkg/ExtIO.0.0.0-ci.nupkg | grep -q -e Tests -e Conformance; then
    echo "ExtIO.0.0.0-ci.nupkg includes its tests" >&2
    exit 1
  fi
}

lang_main "$0" "$@"
