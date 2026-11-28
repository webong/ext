#!/usr/bin/env bash
# native/dotnet: the Windows .NET system and WebView2 libraries, and their NuGet packages.
# (Split out of native/native; the steps are unchanged.)
. "$(dirname "$0")/../lib.sh"

# Validate Windows system and WebView2 libraries
if [ "$RUNNER_OS" = Windows ]; then
(
ci_pwsh <<'PWSH'
dotnet build pkg/ext-windows/act/capture/audio/Ext.Audio.Windows.csproj
dotnet run --project pkg/ext-windows/act/capture/audio/Tests/Ext.Audio.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build pkg/ext-windows/act/clipboard/Ext.Clipboard.Windows.csproj
dotnet run --project pkg/ext-windows/act/clipboard/Tests/Ext.Clipboard.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build pkg/ext-windows/act/clipboard/Guest/Ext.System.Clipboard.Guest.csproj
dotnet build pkg/ext-windows/net/Guest/Ext.System.Network.Guest.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet run --project pkg/ext-windows/act/capture/surface/Tests/Ext.System.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet run --project pkg/ext-windows/net/Tests/Ext.Network.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build pkg/ext-windows/int/model/Ext.ML.Windows.csproj
dotnet run --project pkg/ext-windows/int/model/Tests/Ext.ML.Windows.Tests.csproj
dotnet build pkg/ext-windows/web/Ext.Web.Windows.csproj
dotnet run --project pkg/ext-windows/web/Tests/Ext.Web.Windows.Tests.csproj
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
    'pkg/ext-windows/act/capture/audio/Ext.Audio.Windows.csproj',
    'pkg/ext-windows/act/clipboard/Ext.Clipboard.Windows.csproj',
    'pkg/ext-windows/act/capture/surface/Ext.System.Windows.csproj',
    'pkg/ext-windows/net/Ext.Network.Windows.csproj',
    'pkg/ext-windows/web/Ext.Web.Windows.csproj',
    'pkg/ext-windows/int/model/Ext.ML.Windows.csproj')) {
  dotnet pack $project -c Release -p:Version=0.0.0-ci -o $out
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if ((Get-ChildItem $out -Filter *.nupkg).Count -ne 6) { throw 'expected six packages' }
PWSH
)
ci_import_env
fi
