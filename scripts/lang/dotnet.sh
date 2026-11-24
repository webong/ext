#!/usr/bin/env bash
# The Windows .NET libraries of res/ (audio, clipboard, surface, model, WebView2). Needs the .NET 8 SDK;
# the tests run on Windows.
#   test   every test project; tests that need a device or a clipboard skip and say why
#   pack   pack the NuGet libraries at 0.0.0-ci and check no package carries its tests
. "$(dirname "$0")/common.sh"

lang_cmd_test() {
  dotnet run --project res/system/capture/audio/windows/Tests/Ext.Audio.Windows.Tests.csproj
  dotnet run --project res/system/capture/clipboard/windows/Tests/Ext.Clipboard.Windows.Tests.csproj
  dotnet run --project res/system/capture/surface/windows/Tests/Ext.System.Windows.Tests.csproj
  dotnet run --project res/int/model/windows/Tests/Ext.ML.Windows.Tests.csproj
  dotnet run --project res/web/windows/Tests/Ext.Web.Windows.Tests.csproj
}

lang_cmd_pack() {
  local out project package
  lang_temp
  out="$LANG_TEMP/nupkg"
  for project in \
    res/system/capture/audio/windows/Ext.Audio.Windows.csproj \
    res/system/capture/clipboard/windows/Ext.Clipboard.Windows.csproj \
    res/system/capture/surface/windows/Ext.System.Windows.csproj; do
    dotnet pack "$project" -c Release -p:Version=0.0.0-ci -o "$out"
  done
  # The packages hold the libraries only, not their tests.
  for package in "$out"/*.nupkg; do
    if unzip -l "$package" | grep -q Tests; then echo "$package includes its tests" >&2; exit 1; fi
  done
}

lang_main "$0" "$@"
