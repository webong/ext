#!/usr/bin/env bash
# native/dotnet: the Windows .NET system and WebView2 libraries, and their NuGet packages.
# (Split out of native/native; the steps are unchanged.)
. "$(dirname "$0")/../lib.sh"

# Validate Windows system and WebView2 libraries
if [ "$RUNNER_OS" = Windows ]; then
(
ci_pwsh <<'PWSH'
dotnet build lib/system/act/capture/audio/windows/Ext.Audio.Windows.csproj
dotnet run --project lib/system/act/capture/audio/windows/Tests/Ext.Audio.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build lib/system/act/clipboard/windows/Ext.Clipboard.Windows.csproj
dotnet run --project lib/system/act/clipboard/windows/Tests/Ext.Clipboard.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build res/system/capture/clipboard/windows/Guest/Ext.System.Clipboard.Guest.csproj
dotnet build res/system/service/network/windows/Guest/Ext.System.Network.Guest.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet run --project lib/system/act/capture/surface/windows/Tests/Ext.System.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet run --project lib/system/net/windows/Tests/Ext.Network.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build lib/system/int/model/windows/Ext.ML.Windows.csproj
dotnet run --project lib/system/int/model/windows/Tests/Ext.ML.Windows.Tests.csproj
dotnet build lib/system/web/windows/Ext.Web.Windows.csproj
dotnet run --project lib/system/web/windows/Tests/Ext.Web.Windows.Tests.csproj
PWSH
)
ci_import_env
fi

# Pack the .NET libraries
if [ "$RUNNER_OS" = Windows ]; then
(
ci_pwsh <<'PWSH'
$out = Join-Path $env:RUNNER_TEMP 'nupkg'
foreach ($project in @(
    'lib/system/act/capture/audio/windows/Ext.Audio.Windows.csproj',
    'lib/system/act/clipboard/windows/Ext.Clipboard.Windows.csproj',
    'lib/system/act/capture/surface/windows/Ext.System.Windows.csproj',
    'lib/system/net/windows/Ext.Network.Windows.csproj',
    'lib/system/web/windows/Ext.Web.Windows.csproj',
    'lib/system/int/model/windows/Ext.ML.Windows.csproj')) {
  dotnet pack $project -c Release -p:Version=0.0.0-ci -o $out
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if ((Get-ChildItem $out -Filter *.nupkg).Count -ne 6) { throw 'expected six packages' }
PWSH
)
ci_import_env
fi
