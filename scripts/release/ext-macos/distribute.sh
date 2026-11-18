#!/usr/bin/env bash
# ext-macos release, distribute: push the package to dist/ext-macos, tagged ext-macos-X.Y.Z. The bare
# X.Y.Z tags belong to dist/ext-ios and tags are repository-wide, so SwiftPM cannot resolve an
# ext-macos version: consumers pin the branch or a revision (docs/module-versioning.md).
. "$(dirname "$0")/../apple.sh"

# (action actions/checkout@v5)

# (action actions/download-artifact@v7)

apple_distribute ext-macos "ext-macos-$VERSION"
