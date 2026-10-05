# adapter

`github.com/webong/ctx/pkg/adapter` provides the public process protocol used by
Go adapters: the argv invocation envelope, the API version, and validation for
requests and responses.

An adapter remains a separate executable. CTX never loads Go plugins, so this
package describes the wire protocol rather than an in-process interface.

See [the adapter API](../../docs/adapter-api.md) and
[adapter authoring](../../docs/adapter-authoring.md).
