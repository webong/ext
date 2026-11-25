# C-to-Go generation

This development tool invokes ccgo with a built-in or caller-supplied recipe.
It is a separate module so ccgo and its dependencies stay out of the runtime
SDK's dependency graph. It does not publish releases or install packages.

## Built-in defaults

Run these commands from this directory with `GOWORK=off`:

```sh
GOWORK=off go run .                         # plugin recipe (the default)
GOWORK=off go run . -library ledger          # ledger recipe
GOWORK=off go run . -out-dir /tmp/my-plugin  # choose a generated output directory
GOWORK=off go run . -library ledger -out /tmp/my-ledger.go
```

The built-in recipes locate the Ext checkout from the working directory. Their
default outputs remain `plugin/internal/ccgo/generated_<goos>_<goarch>.go` and
`ledger/internal/ccgo/generated_<goos>_<goarch>.go` within `c2goengine`.
Generation runs for the current OS and architecture, as before.

`-out` chooses a file; `-out-dir` chooses a directory and keeps the platform
filename. They are mutually exclusive. `-publish <directory>` retains its
existing meaning: stage a local directory of Go files for the current platform,
including handwritten bindings. It uploads nothing. Staging always uses the
selected generated output, including an `-out` override.

## Your own C library

Supply a versioned JSON recipe, for example `translation.json`:

```json
{
  "version": 1,
  "source_root": "native",
  "package": "mylib",
  "sources": ["src/library.c"],
  "include_dirs": ["include"],
  "defines": ["MYLIB_STATIC=1"],
  "undefines": [],
  "force_includes": [],
  "build_tag": "mylib_c2go",
  "bindings": "go-bindings"
}
```

```sh
GOWORK=off go run . -recipe /path/to/translation.json \
  -out-dir /path/to/project/internal/mylib

# Optionally stage the translation and handwritten binding together:
GOWORK=off go run . -recipe /path/to/translation.json \
  -out-dir /tmp/mylib-generated -publish /path/to/project/internal/mylib
```

Custom recipes need no Ext checkout. `source_root` defaults to the recipe's
directory, and a relative `source_root` or `bindings` path resolves from that
directory. Sources, include directories and forced includes are relative paths
within `source_root`. The source tree is copied to temporary storage, without
following symbolic links. `defines`, `undefines` and `force_includes` become
ccgo's `-D`, `-U` and `-include` arguments; no shell recipe is executed.

`package` defaults to `ccgo`. An optional `build_tag` is one identifier. With no
output argument, a custom recipe writes into `generated/` beside the recipe.
`-bindings` overrides the handwritten binding directory when staging. Binding
files and the translation must declare the same Go package. Files named
`generated_*.go` in the binding directory are excluded so an older translation
cannot replace the newly generated file.

This generates C implementation code, not an application-facing API. Supply
handwritten Go wrappers in `bindings` when needed. To use the result through
`cengine/library`, supply a driver for that library's described ABI; arbitrary C
functions do not automatically implement Ext's library interface. The generated
code uses the generator's pinned `modernc.org/libc` version (`v1.79.0`); the
consuming module must provide that runtime. Generation still needs the tools ccgo
requires, including a C preprocessor and Go toolchain.

## Existing translations

Reuse a Go package containing a translation and its bindings without running ccgo:

```sh
GOWORK=off go run . -translations /path/to/existing-package \
  -tags mylib_c2go -publish /path/to/project/internal/mylib
```

`-tags` is an optional comma-separated list of build tags for selecting files.
Only files matching the current platform and tags are staged; test files are
excluded. Staged files get platform suffixes and no `//go:build` line, as in the
existing Ext archive workflow. The source package remains unchanged. This path
requires no C source tree or Ext checkout. The staging destination must differ
from the input directory.

`-recipe` and `-translations` are mutually exclusive. Explicit missing or invalid
inputs fail; they never silently select a built-in recipe. Omitting both keeps
the plugin default, or the library named by `-library`.

## Calling generation from Go

Import the new package in this existing **tools module**:

```go
import "github.com/webong/ext/pkg/ext-go/c2goengine/tools/generate"
```

A build-time program can provide its recipe directly:

```go
result, err := generate.Generate(ctx, generate.Config{
    BaseDir: "/path/to/project",
    Recipe: &generate.Recipe{
        SourceRoot: "native",
        Package:    "mylib",
        Sources:    []string{"src/library.c"},
        IncludeDirs: []string{"include"},
        Defines:    []string{"MYLIB_STATIC=1"},
    },
    OutputDir: "internal/mylib",
})
if err != nil {
    return err
}
fmt.Println(result.OutputFile)
```

For existing translations, use `Config{Translations: source, StageDir: output}`;
set `Tags` when files require build tags. For a JSON recipe use `RecipeFile`, or
call `LoadRecipe` to inspect it first. `Recipe`, `RecipeFile` and `Translations`
are mutually exclusive. The typed recipe defaults `Version` to 1; JSON files
still require an explicit version. `BindingsDir` overrides the recipe's binding
location when `StageDir` is set.

`BaseDir` defaults to the caller's working directory and resolves relative
configuration paths. Inline recipe paths resolve there too; paths within a JSON
recipe resolve from that file's directory. `Repository` optionally identifies
an Ext checkout for built-in recipes. `Result` reports absolute output/staging
paths, the staged-file count, and the plugin recipe's input fingerprint.

The API does not change the caller's working directory or mutate its config.
It runs the pinned v4 compiler in a private child process with context
cancellation. Concurrent calls may use separate output/staging directories;
a shared `Log` writer must support concurrent writes. A nil `Log` discards
diagnostics. Existing-translation staging does not invoke a compiler.

Import this API from generation programs, not runtime code. Generated Go is
compiled into the consuming application; it cannot be loaded from a directory
at runtime. Runtime consumers do not need the tools module or ccgo.

## Source layout and verification

- `main.go`: flags and delegation to the generation API.
- `generate/generate.go`: typed configuration and workflow selection.
- `generate/plugin.go`, `generate/ledger.go`: built-in recipes.
- `generate/recipe.go`: typed and JSON custom recipes.
- `generate/shared.go`, `generate/staging.go`: compiler execution and local staging.
- `generate/plugin_inputs.go`, `generate/digest.go`: input fingerprinting.
- `generate/shim`: the built-in compiler compatibility sources.

The package embeds `generate/dependencies.sum`, a copy of the tools module's
`go.sum`, so a compiler worker can verify cached dependencies outside the
checkout. Update that copy when tool dependencies change; tests check it against
the module's pins and checksums.

`GOWORK=off go test ./...` checks the API and CLI, concurrent typed recipes,
cancellation before generation, custom generation/reuse in a separate Go module,
invalid inputs, and output selection. The repository's
`scripts/c2goengine-translate.sh` and `scripts/ledger-c2go.sh` regenerate and test
the built-in translations against their fixtures.
