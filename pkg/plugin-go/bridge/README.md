# CTX transport bridges

`bridge.Open` turns a verified backend Session into an Endpoint that another
transport can serve. It preserves the CTX domain contract while translating
connection mechanics. Go, C, Rust, Zig and TypeScript implementations coexist.

See [interoperability](../../docs/plugin-interoperability.md) for ownership,
authorization, route planning, HashiCorp launch configuration, examples and
conformance coverage. `pkg/plugin-hashicorp/cmd/ext-plugin-bridge` is the first packaged bridge:
CTX JSON lines ↔ HashiCorp gRPC/net/rpc. No full engine migration is required.
