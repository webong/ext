# res/system fixtures

The language-neutral contract of `res/system`: behaviour meant to be identical in Go, Swift, Java
and C# is written here as versioned data, and every binding runs it. Where a binding and a fixture
disagree, the fixture is right and the binding changes. The layout follows `res/web/testdata`.

```
testdata/
  codes.json              the error codes the fixtures use
  <area>/v<N>/cases.json  one file of cases per area and version
```

A change that alters an existing case makes a new `v<N+1>` directory, so a binding that has not
caught up keeps running the old one. A new case that old bindings already satisfy may join the
current version.

## Running them

| Language | Where | How |
| --- | --- | --- |
| Go | `fixtures_test.go` of each package, helper in `internal/fixtures` | `go test ./res/system/...` finds `testdata` by walking up |
| Swift | `Tests/*/FixtureTests.swift` of each `ios` package | `swift test --package-path res/system/<area>/ios` (macOS) |
| Java | `src/test/java/...` of each `android` module, helper in `internal/fixtures/java` | `scripts/system-android.sh`, from the repository root |
| C# | the `Ext.<Area>.Windows` tests | read the files the same way |

`EXT_SYSTEM_TESTDATA` points a runner at a copy elsewhere.

## Codes

Every case states the error code it expects, or `null` when the input is accepted. Each binding
keeps its native error type (a Go sentinel, a Swift enum, a Java exception, a C# kind) and maps it
to the code: `clipboard.Code(err)` in Go, `ClipboardError.code` in Swift, `ClipboardException.code`
in Java and `ClipboardException.Code` in C# (`ShareError.code`, `ShareException.kind()` and so on in
the other areas). The codes are the engine's vocabulary
(`docs/adr-architecture.md`, rule 3) plus the ones `codes.json` adds. Error text is never part of a
case.

## Areas

| Area | Versions | Run by |
| --- | --- | --- |
| `clipboard` | v1 | Go, Swift, Java, C# |
| `share` | v1 | Go, Swift, Java |
| `keyboard` | v1 | Swift, Java |
| `network` | v1 | Go, Swift, Java |
| `host` | v1 | Go, Swift, Java |
| `surface` | v1 | Go, Swift, Java |
| `capability` | v1 | the status of each package, so far network, host and surface |
