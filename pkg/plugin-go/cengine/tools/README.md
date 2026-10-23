# Generated case mapping

`unicode_lower.go` enumerates the Go standard library's Unicode simple-lowercase
mapping for the C engine. `lib/plugin/src/unicode_lower.h` pins Unicode 17.0.0.
Regenerate with a toolchain whose `unicode.Version` is `17.0.0`:

```sh
go run pkg/plugin-go/cengine/tools/unicode_lower.go > lib/plugin/src/unicode_lower.h
```

Then run `scripts/cengine-sync.sh`, which refreshes the copy of the engine this
module is built from.

The generated table supports locale-independent package-path comparisons.
Changing its Unicode version is a contract decision: older Go SDK toolchains
may otherwise accept different case-equivalent paths. The Go distribution's
license, which the table is derived from, is kept with the engine as
`lib/plugin/licenses/Go-LICENSE`, and CMake installs it.
