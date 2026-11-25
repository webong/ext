# HashiCorp plugin backend

`github.com/webong/ext/pkg/ext-go/hashicorp` is the HashiCorp go-plugin backend
of the shared plugin library. It owns the native go-plugin launch handshake and
the net/rpc or gRPC client/server bridge.

The shared `core/plugin.Endpoint` and `core/plugin.Backend` contracts are
transport-independent; Go guests and sessions are implementations of those contracts. This backend supplies one
transport implementation. It is a separate Go module so consumers that do not
need go-plugin never download it.

## Guest

Create a `plugin.Guest` with your descriptor and domain handler, then register
it in a native go-plugin server:

```go
guest, err := plugin.NewGuest(descriptor, plugin.GuestOptions{
    Handler: authorizeAndHandle,
})
if err != nil {
    return err
}
goplugin.Serve(&goplugin.ServeConfig{
    HandshakeConfig: hashicorp.HandshakeConfig(),
    Plugins: goplugin.PluginSet{
        hashicorp.PluginName: &hashicorp.Plugin{Guest: guest},
    },
    GRPCServer: hashicorp.GRPCServer,
})
```

Omit `GRPCServer` to serve net/rpc. The handler checks domain authorization,
honors its context, and supports concurrent calls. Both transports share the
same CTX descriptor and request/response validation.

## Host

Use `plugin.Open` with the consumer's trust verifier and per-call authorizer.
Inside its `Connect` callback, create a dedicated `goplugin.Client` with the
same native handshake and `hashicorp.Plugin{}` in its `Plugins` map, then call
`hashicorp.Connect(ctx, client)`. The supplied runnable example shows the full
configuration, including executable checksums and automatic TLS.

Set `AllowedProtocols` explicitly to `ProtocolGRPC` or `ProtocolNetRPC`, and
bound `StartTimeout` to the remaining connection deadline. Configure process
environment and logging deliberately: go-plugin's defaults are not CTX's
domain policy. The native magic cookie is not an authorization credential.

`Connect` owns the client lifecycle. Close it when the endpoint closes so the
child process is reaped.

## Relationship to other modules

Route resolution across backends lives in `core/plugin/interop`, with the old
`engine/plugin/host/interop` API preserved by aliases and forwarding. The relay
between this backend and JSON-line transports remains in
`engine/plugin/transport/bridge`.

See the [plugin contract decision record](../../../docs/adr-plugin-contract.md)
and the [interoperability guide](../../../docs/plugin-interoperability.md).

## Source boundaries

The root library imports shared `core/plugin` contracts and
`core/plugin/interop` route metadata; it has no transitive dependency on the
`engine` implementation. `JSONLineBridge` uses the shared JSON-line capability
profile without linking the JSON-line client. Its return type is identical to
the existing `engine/plugin/host/interop.Bridge` alias.

The module still requires `engine` for its example command, guest helpers and
test oracles. The `ctx` dispense name, handshake cookie, deadlines and process
cleanup behavior are unchanged. This module is a plugin backend for guest
execution, not a driver for accessing Ext's canonical C libraries.
