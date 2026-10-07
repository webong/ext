# res/browser

`github.com/webong/ext/res/browser` holds CTX's portable browser contracts and
workflows. It depends only on the Go standard library, so a host can consume it
without pulling an adapter, a browser engine, or any CTX application code.

| Package | Contents |
| --- | --- |
| `contract` | Request, response, cookie, policy and session types with their validation |
| `guest` | Portable browser workflows served through adapter-provided backends |
| `extension` | Extension artifact discovery, packaging and install targets |
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
