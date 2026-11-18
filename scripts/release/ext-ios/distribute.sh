#!/usr/bin/env bash
# ext-ios release, distribute: push the package to dist/ext-ios, tagged with the bare version
# SwiftPM resolves (from: "X.Y.Z").
. "$(dirname "$0")/../apple.sh"

# (action actions/checkout@v5)

# (action actions/download-artifact@v7)

apple_distribute ext-ios "$VERSION"
