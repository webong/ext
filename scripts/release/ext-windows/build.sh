#!/usr/bin/env bash
# ext-windows release, build: test and pack the Windows native .NET libraries (Ext.Audio.Windows,
# Ext.Clipboard.Windows, Ext.System.Windows, Ext.Network.Windows, Ext.Web.Windows, Ext.ML.Windows), the Windows counterpart of the native
# code ext-ios and ext-android ship, plus private Ext clipboard, network and surface resource guests. Runs on Windows.
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
dotnet run --project lib/system/act/capture/audio/windows/Tests/Ext.Audio.Windows.Tests.csproj
dotnet run --project lib/system/act/clipboard/windows/Tests/Ext.Clipboard.Windows.Tests.csproj
dotnet run --project lib/system/act/capture/surface/windows/Tests/Ext.System.Windows.Tests.csproj
dotnet run --project lib/system/net/windows/Tests/Ext.Network.Windows.Tests.csproj
dotnet run --project lib/system/int/model/windows/Tests/Ext.ML.Windows.Tests.csproj
dotnet run --project lib/system/web/windows/Tests/Ext.Web.Windows.Tests.csproj
)
ci_import_env

# Pack
(
set -euo pipefail
out="dist/ext-windows-$VERSION"
mkdir -p "$out"
for project in \
  lib/system/act/capture/audio/windows/Ext.Audio.Windows.csproj \
  lib/system/act/clipboard/windows/Ext.Clipboard.Windows.csproj \
  lib/system/act/capture/surface/windows/Ext.System.Windows.csproj \
  lib/system/net/windows/Ext.Network.Windows.csproj \
  lib/system/web/windows/Ext.Web.Windows.csproj \
  lib/system/int/model/windows/Ext.ML.Windows.csproj; do
  dotnet pack "$project" -c Release -p:Version="$VERSION" -o "$out"
done
[[ "$(ls "$out"/*.nupkg | wc -l)" -eq 6 ]] || { echo 'expected six packages' >&2; ls "$out" >&2; exit 1; }

# The clipboard mechanism also ships as a private Ext resource guest. The
# package has one self-contained executable and no public CLI entry point.
publish_dir="$out/clipboard-guest-publish"
dotnet publish res/system/capture/clipboard/windows/Guest/Ext.System.Clipboard.Guest.csproj \
  -c Release -r win-x64 --self-contained true \
  -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true \
  -o "$publish_dir"
python3 scripts/release/pack-resource-guest.py \
  res/system/capture/clipboard/windows/Guest/ext.json \
  "$publish_dir/Ext.System.Clipboard.Guest.exe" "$out/system.clipboard.windows" windows amd64
rm -rf "$out/system.clipboard.windows"
rm -rf "$publish_dir"

publish_dir="$out/network-guest-publish"
dotnet publish res/system/service/network/windows/Guest/Ext.System.Network.Guest.csproj \
  -c Release -r win-x64 --self-contained true \
  -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true \
  -o "$publish_dir"
python3 scripts/release/pack-resource-guest.py \
  res/system/service/network/windows/Guest/ext.json \
  "$publish_dir/Ext.System.Network.Guest.exe" "$out/system.network.windows" windows amd64
rm -rf "$out/system.network.windows"
rm -rf "$publish_dir"

publish_dir="$out/surface-guest-publish"
dotnet publish res/system/capture/surface/windows/Guest/Ext.System.Surface.Guest.csproj \
  -c Release -r win-x64 --self-contained true \
  -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true \
  -o "$publish_dir"
python3 scripts/release/pack-resource-guest.py \
  res/system/capture/surface/windows/Guest/ext.json \
  "$publish_dir/Ext.System.Surface.Guest.exe" "$out/system.surface.windows" windows amd64
rm -rf "$out/system.surface.windows"
rm -rf "$publish_dir"
)
ci_import_env

# Checksums
(
set -euo pipefail
cd "dist/ext-windows-$VERSION"
sha256sum *.nupkg *.snupkg *.zip | tee checksums.txt
)
ci_import_env

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
cp -R "dist/ext-windows-$VERSION"/. release-out/
)
