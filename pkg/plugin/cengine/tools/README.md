# Generated case mapping

`unicode_lower.go` enumerates the Go standard library's Unicode simple-lowercase
mapping. `../src/unicode_lower.h` pins Unicode 17.0.0. Regenerate with a toolchain
whose `unicode.Version` is `17.0.0`:

```sh
go run pkg/plugin/cengine/tools/unicode_lower.go > pkg/plugin/cengine/src/unicode_lower.h
```

The generated table supports locale-independent package-path comparisons.
Changing its Unicode version is a contract decision: older Go SDK toolchains
may otherwise accept different case-equivalent paths. The Go distribution's
license is retained in `Go-LICENSE`.
