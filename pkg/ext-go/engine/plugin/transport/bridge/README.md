# Ext transport bridges

`bridge.Open` turns a verified backend Session into an Endpoint that another
transport can serve. It preserves the Ext domain contract while translating
connection mechanics. Go, C, Rust, Zig and TypeScript implementations coexist.

See [interoperability](../../../../../../docs/plugin-interoperability.md) for ownership,
authorization, route planning, HashiCorp launch configuration, examples and
conformance coverage. `ext bridge --config <path>` is the command entry point:
Ext JSON lines ↔ HashiCorp gRPC/net/rpc. No full engine migration is required.
