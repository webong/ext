# res/credential

`github.com/webong/ctx/res/credential` holds CTX's portable credential code. It
depends only on the Go standard library, so a host can consume it without a
native store implementation.

| Package | Contents |
| --- | --- |
| `adapterkit` | Serves credential operations against a supplied `Store` |
| `client` | Client for CTX's credential process protocol |

An adapter executable parses its process envelope and passes an `Invocation` to
`adapterkit.Run`. The store itself is supplied by the adapter that owns the
native mechanism, such as `adapters/keychain`, `adapters/credman`, or
`adapters/secret_service`.

`client` talks to CTX's credential process protocol and has no dependency on a
native store; using it requires a CTX host.

See [native credentials](../../docs/credentials.md) and the
[adapter API](../../docs/adapter-api.md).
