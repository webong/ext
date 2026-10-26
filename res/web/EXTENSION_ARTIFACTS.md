# Mobile extension artifacts

Swift `ExtWeb.ExtensionArtifacts` and Java
`io.github.webong.ext.web.ExtensionArtifacts` implement the portable artifact
workflows of Go's `res/web/extension`. They ship through the existing mobile
SDKs; their implementation lives in `res/web`.

| Operation | Swift | Java |
| --- | --- | --- |
| Inspect directory, ZIP or XPI | `inspect(source)` | `inspect(source)` |
| Require a reviewed revision | `checkedSource(source, revision:)` | `checkedSource(source, revision)` |
| Produce an unsigned ZIP | `packageArchive(source, output:)` | `packageArchive(source, destination)` |
| Stage in a new directory | `stage(source, destination:, expectedRevision:)` | `stage(source, destination)`, `stageWithRevision(source, destination, expected)` |
| Check output location/suffix | `outsideSource(source, output:)`, `unusedArtifact(destination, extension:)` | `outsideSource(source, output)`, `unusedArtifact(destination, suffix)` |
| Copy an artifact exclusively | `copyArtifactExclusive(source, destination:)` | `copyArtifactExclusive(source, destination)` |
| Hash an opaque artifact | `fileSHA256(source)` | `fileSHA256(source)` |

Swift paths are file URLs; Java paths are Files. Inspection returns the manifest
name/version, manifest version (2 or 3), key-derived extension ID when present,
sorted unique permissions and host permissions, file count, byte count and
source revision. A single wrapper directory is removed when necessary to reach
the manifest.

The revision is Go-compatible: SHA-256 of files in UTF-8 filename order, each
encoded as filename, NUL, eight-byte big-endian content length, then content.
ZIP compression, timestamps and permissions do not affect it. Packaging uses
deterministic uncompressed ZIP entries, with fixed timestamps and regular-file
modes. Its ZIP bytes differ from Go's compressed output; the inspected source
revision is identical.

```swift
let reviewed = try ExtensionArtifacts.inspect(source)
let staged = try ExtensionArtifacts.stage(source, destination: staging,
                                         expectedRevision: reviewed.revision)
let artifact = try ExtensionArtifacts.packageArchive(staging, output: outputZIP)
```

```java
ExtensionArtifacts.Description reviewed = ExtensionArtifacts.inspect(source);
ExtensionArtifacts.stageWithRevision(source, staging, reviewed.revision);
ExtensionArtifacts.Description artifact =
    ExtensionArtifacts.packageArchive(staging, outputZIP);
```

## Boundaries and limits

Sources and output directories must be private to the caller and unchanged by
other writers during an operation. Sources are read into a bounded snapshot.
Outputs must be outside the source tree, including paths through directory
aliases. Source symlinks, symlinks inside a source, special files, unsafe paths,
duplicate names, file/directory collisions, encrypted ZIPs and invalid checksums
are rejected. Staging verifies the copied revision and removes its new directory
on failure. Existing destinations are never replaced; failed writes remove the
new output. File creation is exclusive, but output publication is not atomic:
another reader can observe the file while it is being written. Hosts should
publish or expose an output only after the call succeeds.

Limits shared with Go are 2,048 files, 16 MiB per file, 64 MiB total contents,
1 MiB manifest, 4,096 ZIP directory entries, and 256 entries in each manifest
permission list. Mobile additionally caps archive input/output at 80 MiB and
directory traversal at 128 levels / 4,096 directories. Classic single-disk ZIP
with UTF-8 names and stored or deflated files is supported; ZIP64 and other
compression methods are rejected. Mobile requires canonical JSON member names,
as elsewhere in the mobile rules. These APIs read complete bounded snapshots,
so hosts should budget memory and run large operations off their UI thread.

Unsigned packaging does not install an extension into WKWebView or Android
WebView. Native browser installation, CRX signing, Safari conversion, profile
selection, credentials and tool discovery remain host/adapter responsibilities.
The standalone copy/hash helpers treat artifacts as opaque files and stream
their contents without the extension archive size limits. Copies use ordinary
file creation permissions; the caller owns any subsequent access restrictions.

## Verification

`testdata/extension` contains deterministic stored, deflated and wrapped fixtures,
expected descriptions, and malformed archives. Go, Swift, JVM and Android device
tests consume the same corpus. The mobile tests additionally check revision
changes, overwrite refusal, symlinks, output aliases, staging cleanup, and
reproducible packaging.

- `go test ./...` from `res/web`
- `scripts/web-ios.sh`
- `scripts/web-android.sh`
- iOS simulator: `xcodebuild -scheme ExtWeb-Package -destination 'platform=iOS Simulator,id=<id>' -only-testing:ExtWebTests/ExtensionArtifactTests test` from `res/web/ios`
- Android device: `scripts/web-workspace-android.sh --without-androidx`; the same checks also run with `--aar <SDK AAR> --minify`.

The Swift codec uses the platform zlib library; Java uses `java.util.zip`.
Neither introduces a third-party package or a dependency on the plugin engine.
