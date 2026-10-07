# Hosting a WebAssembly plugin in the system web engine

A proof of concept: a plugin guest compiled to WASI runs inside a browser or
webview, and a page speaks `ext.plugin/v1` to it. It exists to answer whether the
web engine every phone already has can host ext plugins. See
[the decision record](../../docs/adr-mobile-wasm.md) for what it showed.

```sh
# Build the repository's Go test guest as a WASI command (about 5.6 MB).
(cd ../../pkg/plugin-wasm && GOOS=wasip1 GOARCH=wasm go build \
  -o ../../examples/plugin-webview/site/guest.wasm ./crosslang/testdata/go-guest)

go run . -target Safari            # or "Google Chrome", Firefox, ios-simulator, android
```

`site/worker.js` is a small WASI Preview 1 host. The guest reads JSON lines from
standard input and blocks while it waits, which a browser's main thread cannot
do, so the guest runs in a Worker and its standard input is a ring buffer in a
`SharedArrayBuffer` read with `Atomics.wait`. Browsers allow that only on
cross-origin-isolated pages, hence `CrossOriginIsolation` in the harness.
`site/index.html` sends the handshake, an `echo` call and an unknown operation,
and checks the answers.

It is a proof of concept, not a library: the WASI shim covers only the sixteen
calls this guest imports, and everything else answers "not implemented".
