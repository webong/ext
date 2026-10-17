#!/usr/bin/env bash
# Prints the dependency layers of a release round.
#
# Purpose: modules that require each other must be tagged in order, each layer
# verified before the next, because a layer's go.sum records the hashes of the
# layer below (see release-layer.sh). A module sits one layer above the highest
# listed module its go.mod requires; layer 0 requires no other listed module.
#
# Usage: scripts/release-layers.sh VERSIONS
#   VERSIONS   file of "module version" lines (see release-common.sh)
# Prints "layer module version" lines in order. Checks that the versions file is
# valid and the listed modules do not require each other in a cycle.
# Needs: bash, git, awk, sort.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-common.sh
. "$here/release-common.sh"

[ $# -eq 1 ] || release_die "usage: $0 VERSIONS"
release_init
release_layers "$1"
