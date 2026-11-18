#!/usr/bin/env bash
# ext-dotnet release, build: test and pack ExtIO.Host, the .NET plugin host (the .NET counterpart of
# ext-rust, ext-ts and ext-python). It is cross-platform (net8.0), so it builds on Linux; the
# Windows libraries of res/ are the ext-windows package.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# Version
(
if [[ "${GITHUB_REF_TYPE}" == tag ]]; then version="${GITHUB_REF_NAME#ext-dotnet-v}"; else version="${INPUT_VERSION_DISPATCH:-}"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# Tests: without EXT_HOST_LIBRARY and the guests the host tests check what they can and say so; the
# host cases run against the engine in core.yml (host-cases).
(
dotnet run --project pkg/ext-dotnet/ExtIO.Host.Tests/ExtIO.Host.Tests.csproj -c Release
)
ci_import_env

# Pack
(
set -euo pipefail
out="dist/ext-dotnet-$VERSION"
mkdir -p "$out"
dotnet pack pkg/ext-dotnet/ExtIO.Host/ExtIO.Host.csproj -c Release -p:Version="$VERSION" -o "$out"
[[ "$(ls "$out"/*.nupkg | wc -l)" -eq 1 ]] || { echo 'expected one package' >&2; ls "$out" >&2; exit 1; }
)
ci_import_env

# Checksums
(
set -euo pipefail
cd "dist/ext-dotnet-$VERSION"
sha256sum *.nupkg *.snupkg | tee checksums.txt
)
ci_import_env

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
cp -R "dist/ext-dotnet-$VERSION"/. release-out/
)
