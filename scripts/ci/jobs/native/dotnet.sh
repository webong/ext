#!/usr/bin/env bash
# native/dotnet: the Windows .NET system and WebView2 libraries, and their NuGet packages.
# (Split out of native/native; the steps are unchanged.)
. "$(dirname "$0")/../lib.sh"

# Validate Windows system and WebView2 libraries
if [ "$RUNNER_OS" = Windows ]; then
(
ci_pwsh <<'PWSH'
dotnet build res/system/capture/audio/windows/Ext.Audio.Windows.csproj
dotnet run --project res/system/capture/audio/windows/Tests/Ext.Audio.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build res/system/capture/clipboard/windows/Ext.Clipboard.Windows.csproj
dotnet run --project res/system/capture/clipboard/windows/Tests/Ext.Clipboard.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet run --project res/system/capture/surface/windows/Tests/Ext.System.Windows.Tests.csproj
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
dotnet build res/int/model/windows/Ext.ML.Windows.csproj
dotnet run --project res/int/model/windows/Tests/Ext.ML.Windows.Tests.csproj
dotnet build res/web/windows/Ext.Web.Windows.csproj
dotnet run --project res/web/windows/Tests/Ext.Web.Windows.Tests.csproj
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
    'res/system/capture/audio/windows/Ext.Audio.Windows.csproj',
    'res/system/capture/clipboard/windows/Ext.Clipboard.Windows.csproj',
    'res/system/capture/surface/windows/Ext.System.Windows.csproj',
    'res/web/windows/Ext.Web.Windows.csproj',
    'res/int/model/windows/Ext.ML.Windows.csproj')) {
  dotnet pack $project -c Release -p:Version=0.0.0-ci -o $out
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if ((Get-ChildItem $out -Filter *.nupkg).Count -ne 5) { throw 'expected five packages' }
PWSH
)
ci_import_env
fi
