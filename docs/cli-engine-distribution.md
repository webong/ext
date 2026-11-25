# Canonical engine in CLI distributions

Every CLI archive carries `bin/ext-lib/` with its canonical C plugin library,
`interface.json` and an `engine.json` record. Ext loads this copy through the
public described library interface. No C compiler or environment override is
needed to use source catalogs and dependency planning from an installed release.

The release catalog builds native engines on Linux amd64/arm64, macOS universal,
and Windows amd64/arm64 from the same checkout as the CLI. These jobs reuse the
existing native engine build and test script. `stage-cli-engine.py` copies the
actual library bytes, records their SHA-256, target, Git revision and interface
digest, and rejects inconsistent inputs before CLI archive assembly. No new
third-party library or downloaded unpinned engine is introduced.

`build-release-ext.sh` requires all six CLI targets by default, before compiling
or emitting archives. The release prepare job explicitly clears any target
subset override. A missing native job or artifact therefore fails preparation.
For local rehearsals, `EXT_RELEASE_TARGETS` may name a subset and
`EXT_RELEASE_NATIVE_DIR` may point to staged native artifacts. These overrides
publish nothing and are not a way to pass the full release gate.

Both installers require a bundled engine and run an empty package plan from the
staged executable in a temporary home before copying it. Existing package stores
are not used for this check. Source installation additionally requires CMake and
a native C compiler, builds the canonical library, and carries it in the same
layout. Root README installation instructions remain focused on the installer.

The macOS native archive and file-based installer path have been exercised
locally. Linux and Windows builds are configured in the native release matrix;
their platform execution remains CI evidence to collect before a release.
