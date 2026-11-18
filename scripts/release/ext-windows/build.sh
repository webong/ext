#!/usr/bin/env bash
# ext-windows release, build: test and pack the Windows .NET libraries of res/ (Ext.Audio.Windows,
# Ext.Clipboard.Windows, Ext.Web.Windows, Ext.ML.Windows), the Windows counterpart of the native
# code ext-ios and ext-android ship. Runs on Windows.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# Version
(
if [[ "${GITHUB_REF_TYPE}" == tag ]]; then version="${GITHUB_REF_NAME#ext-windows-v}"; else version="${INPUT_VERSION_DISPATCH:-}"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Tests
(
set -euo pipefail
# The audio and clipboard tests skip, and say why, what needs a device or a clipboard.
dotnet run --project res/system/capture/audio/windows/Tests/Ext.Audio.Windows.Tests.csproj
dotnet run --project res/system/capture/clipboard/windows/Tests/Ext.Clipboard.Windows.Tests.csproj
dotnet run --project res/int/model/windows/Tests/Ext.ML.Windows.Tests.csproj
dotnet run --project res/web/windows/Tests/Ext.Web.Windows.Tests.csproj
)
ci_import_env

# Pack
(
set -euo pipefail
out="dist/ext-windows-$VERSION"
mkdir -p "$out"
for project in \
  res/system/capture/audio/windows/Ext.Audio.Windows.csproj \
  res/system/capture/clipboard/windows/Ext.Clipboard.Windows.csproj \
  res/web/windows/Ext.Web.Windows.csproj \
  res/int/model/windows/Ext.ML.Windows.csproj; do
  dotnet pack "$project" -c Release -p:Version="$VERSION" -o "$out"
done
[[ "$(ls "$out"/*.nupkg | wc -l)" -eq 4 ]] || { echo 'expected four packages' >&2; ls "$out" >&2; exit 1; }
)
ci_import_env

# Checksums
(
set -euo pipefail
cd "dist/ext-windows-$VERSION"
sha256sum *.nupkg *.snupkg | tee checksums.txt
)
ci_import_env

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
cp -R "dist/ext-windows-$VERSION"/. release-out/
)
