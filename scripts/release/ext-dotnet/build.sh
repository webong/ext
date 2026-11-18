#!/usr/bin/env bash
# ext-dotnet release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# Version
(
export INPUT_VERSION="${INPUT_VERSION_DISPATCH:-}"
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
if [[ "$REF_TYPE" == tag ]]; then version="${REF_NAME#ext-dotnet-v}"; else version="$INPUT_VERSION"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# (action actions/setup-dotnet@v5)

# Tests
(
set -euo pipefail
# The audio and clipboard tests skip, and say why, what needs a device or a clipboard.
dotnet run --project res/system/capture/audio/windows/Tests/Ext.Audio.Windows.Tests.csproj
dotnet run --project res/system/capture/clipboard/windows/Tests/Ext.Clipboard.Windows.Tests.csproj
dotnet run --project res/int/model/windows/Tests/Ext.ML.Windows.Tests.csproj
dotnet run --project res/web/windows/Tests/Ext.Web.Windows.Tests.csproj
# Without EXT_HOST_LIBRARY and the guests the host tests check what they can and say so; the
# host cases run against the engine in host-cases.yml.
dotnet run --project pkg/ext-dotnet/ExtIO.Host.Tests/ExtIO.Host.Tests.csproj -c Release
)
ci_import_env

# Pack
(
export VERSION="${VERSION}"
set -euo pipefail
out="dist/ext-dotnet-$VERSION"
mkdir -p "$out"
for project in \
  res/system/capture/audio/windows/Ext.Audio.Windows.csproj \
  res/system/capture/clipboard/windows/Ext.Clipboard.Windows.csproj \
  res/web/windows/Ext.Web.Windows.csproj \
  res/int/model/windows/Ext.ML.Windows.csproj \
  pkg/ext-dotnet/ExtIO.Host/ExtIO.Host.csproj; do
  dotnet pack "$project" -c Release -p:Version="$VERSION" -o "$out"
done
[[ "$(ls "$out"/*.nupkg | wc -l)" -eq 5 ]] || { echo 'expected five packages' >&2; ls "$out" >&2; exit 1; }
)
ci_import_env

# Checksums
(
export VERSION="${VERSION}"
set -euo pipefail
cd "dist/ext-dotnet-$VERSION"
sha256sum *.nupkg *.snupkg | tee checksums.txt
)
ci_import_env

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-dotnet-${VERSION}/*; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
