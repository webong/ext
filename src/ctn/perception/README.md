# Perception plugin bridge

`src/ctn/perception` exposes the `src/ctn/contract/perception` domain through the
language-neutral `intelligence.perception/v1` Ext plugin contract. Go guests
register a provider with `RegisterProvider` and serve it with `ServeStdio` or
`ServeCurrentExecutable`. Go hosts call `Info`, `Observe`, and `Interpret`
through `Client` after their own install, trust, and authorization checks.

The contract operations are `capabilities`, `observe`, `interpret`, and
`close`. Providers may implement only observation or interpretation; an
unsupported operation returns an explicit error. Every request names a model.
Responses preserve request IDs and source indexes, and are validated at both
guest and host boundaries. No tool proposal is executed by this bridge.

The JSON files in `testdata/v1` are transport-independent fixtures for other
host and guest languages. CTN's `perception` command exercises package,
install, trust, invoke, and revoke through a real subprocess.

For a guest executable `provider`, the CTN flow is:

```sh
ext ctn perception package example 1.0.0 ./provider ./example-package
ext ctn perception install ./example-package
ext ctn perception trust example
ext ctn perception capabilities --provider example
ext ctn perception observe --provider example --model model-id --input ./image-request.json
ext ctn perception untrust example
```

The request file contains an `image` byte array encoded as base64 and a
`mediaType` such as `image/png`. CTN fills `apiVersion` and the selected model.
`EXT_INT_PACKAGE_ROOT` selects a different package store; trust is scoped to
the perception protocol and the exact installed package digest.
