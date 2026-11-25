# C shared-library guests

All C guest support lives in the `pkg/ext-go/cengine` module:

| Package | Responsibility |
| --- | --- |
| `cguest` | Loads shared libraries exposing `ext_plugin_guest_*`, owns native handles and exchanges bytes. |
| `cguest/abi` | Shared C ABI constants and handshake envelope. |
| `cguest/backend` | Implements `core/plugin.Backend`: Ext frames, descriptor and response validation, deadlines and error mapping. |
| `cguest/guest` | Exports Go plugin guests through the ABI using `-buildmode=c-shared`. |

The loader uses cgo or purego. It is specific to the Ext guest ABI; arbitrary C
libraries use `cengine/library`. The backend and guest export packages use the shared `core/plugin` contracts. On darwin/amd64 both loaders refuse Go c-shared guests because
two Go runtimes share the TLS slot for g.

`scripts/cshared-purego.sh` checks the backend and shared host cases with and
without cgo. Building a Go c-shared guest still requires cgo.
