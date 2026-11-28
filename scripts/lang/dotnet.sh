#!/usr/bin/env bash
# The Windows .NET system libraries (audio, clipboard, surface, network, model, WebView2). Needs the .NET 8 SDK;
# the tests run on Windows.
#   test   every test project; tests that need a device or a clipboard skip and say why
#   pack   pack the NuGet libraries at 0.0.0-ci and check no package carries its tests
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  dotnet run --project pkg/ext-windows/act/capture/audio/Tests/Ext.Audio.Windows.Tests.csproj
  dotnet run --project pkg/ext-windows/act/clipboard/Tests/Ext.Clipboard.Windows.Tests.csproj
  dotnet run --project pkg/ext-windows/act/capture/surface/Tests/Ext.System.Windows.Tests.csproj
  dotnet run --project pkg/ext-windows/net/Tests/Ext.Network.Windows.Tests.csproj
  dotnet run --project pkg/ext-windows/int/model/Tests/Ext.ML.Windows.Tests.csproj
  dotnet run --project pkg/ext-windows/web/Tests/Ext.Web.Windows.Tests.csproj
  dotnet build res/system/capture/clipboard/windows/Guest/Ext.System.Clipboard.Guest.csproj
  dotnet build res/system/service/network/windows/Guest/Ext.System.Network.Guest.csproj
}

lang_cmd_pack() {
  local out project package
  lang_temp
  out="$LANG_TEMP/nupkg"
  for project in \
    pkg/ext-windows/act/capture/audio/Ext.Audio.Windows.csproj \
    pkg/ext-windows/act/clipboard/Ext.Clipboard.Windows.csproj \
    pkg/ext-windows/act/capture/surface/Ext.System.Windows.csproj \
    pkg/ext-windows/net/Ext.Network.Windows.csproj \
    pkg/ext-windows/web/Ext.Web.Windows.csproj \
    pkg/ext-windows/int/model/Ext.ML.Windows.csproj; do
    dotnet pack "$project" -c Release -p:Version=0.0.0-ci -o "$out"
  done
  # The packages hold the libraries only, not their tests.
  for package in "$out"/*.nupkg; do
    if unzip -l "$package" | grep -q Tests; then echo "$package includes its tests" >&2; exit 1; fi
  done
}

lang_main "$0" "$@"
