# res/web

`github.com/webong/ext/res/web` holds ext's portable web contracts and workflows:
browsers (cookies, extensions, userscripts, sessions) today, and the web engine
that runs web content through them. Any ext product or other program can use it. It depends only on the Go standard library, so a host can consume it
without pulling an adapter, a browser engine, or any CTX application code.

| Package | Contents |
| --- | --- |
| `contract` | Request, response, cookie, policy and session types with their validation |
| `guest` | Portable browser workflows served through adapter-provided backends |
| `extension` | Extension artifact discovery, packaging and install targets |
| `bundle` | The web engine: serves a web bundle from loopback under a content security policy and runs it in a browser the host opens, reporting console output and the exit code |
| `userscript` | Userscript metadata, storage and replay |
| `bookmarklet` | Bookmarklet generation |

`browser` itself parses and combines cookies. `Get` and `Normalize` require an
injected `browser.Backend` for source operations; `InlineOnly`, the parsers and
the output helpers work without one.

Native storage, discovery and tooling belong under `adapters/<name>/`. No
package here chooses a browser implementation by name — hosts supply selection
and authorization policy.

See [browser profile and page management](../../docs/browser-management.md),
[browser cookies and automation input](../../docs/browser-cookies.md), and the
[adapter API](../../docs/adapter-api.md).
