#!/usr/bin/env bash
# ext-android release, build: build, test and stage what the release ships.
# Moved from the package's own release workflow; the steps are unchanged.
. "$(dirname "$0")/../../ci/jobs/lib.sh"

# (action actions/checkout@v5)

# Version
(
export INPUT_VERSION="${INPUT_VERSION_DISPATCH:-}"
export REF_NAME="${GITHUB_REF_NAME}"
export REF_TYPE="${GITHUB_REF_TYPE}"
if [[ "$REF_TYPE" == tag ]]; then version="${REF_NAME#ext-android-v}"; else version="$INPUT_VERSION"; fi
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version '$version' must look like 0.1.0" >&2; exit 1; }
echo "VERSION=$version" >>"$GITHUB_ENV"
)
ci_import_env

# (action actions/setup-java@v5)

# (action dtolnay/rust-toolchain@stable)

# (action actions/cache@v5)

# (action gradle/actions/setup-gradle@v4)

# Web rules against the fixtures
(
scripts/web-android.sh
)
ci_import_env

# System clipboard contracts
(
scripts/clipboard-android.sh
)
ci_import_env

# System shell contracts
(
scripts/system-android.sh
)
ci_import_env

# System network observation contracts
(
scripts/network-android.sh
)
ci_import_env

# Build both AARs
(
export VERSION="${VERSION}"
scripts/ext-android-release.sh "$VERSION"
scripts/ext-android-release.sh --with-wamr "$VERSION"
(cd "dist/ext-android-$VERSION" && sha256sum ext-android-*.aar WAMR-LICENSE | tee checksums.txt)
)
ci_import_env

# Enable KVM
(
echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' | sudo tee /etc/udev/rules.d/99-kvm4all.rules
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=kvm
)
ci_import_env

# (action reactivecircus/android-emulator-runner@v2.33.0)

# (action actions/upload-artifact@v6)

# Keep what this build ships: the workflow uploads release-out/ for the publish job.
(
mkdir -p release-out
shopt -s nullglob
found=0
for f in dist/ext-android-${VERSION}/*; do cp -R "$f" release-out/; found=1; done
[ "$found" = 1 ] || { echo "nothing to ship matched the upload paths" >&2; exit 1; }
)
