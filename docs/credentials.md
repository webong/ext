# Native credentials

CTX can read, write, or copy one explicitly identified item in an installed,
trusted native credential-store adapter. The core dispatches the request and
handles private input/output; it does not know a store's paths, names, API, or
permissions. No adapter is bundled or activated by a core-only install.

| Platform | Maintained adapter | Native store | Item reference |
| --- | --- | --- | --- |
| macOS | `keychain` | default Keychain, generic-password items | `'keychain:service=<name>&account=<name>'` |
| Linux | `secret_service` | login-session Secret Service via `secret-tool` | `'secret_service:service=<name>&account=<name>'` or other URL-encoded lookup attributes |
| Windows | `credman` | current user's Credential Manager generic credentials | `'credman:target=<name>'` |

Percent-encode special characters within an item reference and quote the whole
reference in a shell. These are item selectors, never secret values. `ctx` does
not enumerate a vault or extract Windows domain credentials. Native access
rules, prompts, account scope, and Linux session-bus availability still apply.

When a caller reports a trusted adapter ID with a declared credential
dependency, CTX writes an informational notice such as `reported requester
Google Chrome (chrome) is accessing credentials through keychain` to stderr.
The adapter supplies the human-readable `display_name` in its trusted
manifest; otherwise CTX uses its adapter ID. The inherited requester ID is
only a hint for this notice, not proof of the calling process or a permission
grant. Check the actual system prompt
separately: macOS identifies the native process that accesses Keychain, not an
arbitrary display name supplied by the browser adapter. Linux Secret Service
prompts are displayed by the service. Windows CredMan's direct generic-
credential operations do not add a CTX-controlled prompt.

The Linux adapter requires `secret-tool` and a running Secret Service provider
in the user's desktop session. The macOS adapter uses Security.framework and
needs a native macOS build when installing from source; prebuilt releases carry
that executable separately from the core. `EXT_KEYCHAIN_PATH` can select an
absolute path to a caller-owned non-default keychain; otherwise the default
keychain is used.
Windows DPAPI browser-cookie decryption is separate from Credential Manager;
this adapter does not bypass Chromium App-Bound encryption.

## Commands

```sh
ctx credential get '<adapter:item>' --to-file ./new-private-file
ctx credential get '<adapter:item>' --stdout | consumer
ctx credential put '<adapter:item>' --from-file ./private-input
producer | ctx credential put '<adapter:item>' --stdin
ctx credential copy '<source-adapter:item>' '<target-adapter:item>'
ctx credential copy '<source>' '<target>' --replace
```

`ctx share:credential` is an alias for `ctx credential`. `get --stdout` refuses
a terminal and requires a pipe. `--to-file` creates a new file rather than
overwriting an existing one; on Unix CTX creates it with mode 0600. On Windows,
file access also depends on the containing directory's ACL, so use a private
directory or a pipe. On Unix, `--from-file` requires a regular file inaccessible
to group and other users. `--stdin` refuses a terminal to avoid echoed typing.
The maximum item size is 1 MiB. Copy retains the source, buffers the value only
for that operation, and requires `--replace` to overwrite a target. It is not
live synchronization or an atomic two-store transaction. CTX does not save
values to `.ctx`, its global configuration, or the system graph.
Native stores may impose lower limits: Windows Credential Manager generic
credential blobs are limited to 2,560 bytes.

For two machines, use an authenticated transport and an adapter on each end.
For example, from a Mac to a Linux host with SSH access:

```sh
ctx credential get 'keychain:service=ctx&account=work' --stdout |
  ssh linux-host "ctx credential put 'secret_service:service=ctx&account=work' --stdin"
```

This is a point-in-time plaintext stream inside the SSH connection. Confirm
that the destination is trusted, its native store is unlocked, and the item
name is right before running it. An untrusted adapter can execute with your
permissions; review and trust only packages you intend to use.

Chromium-family browser adapters declare `credential:keychain@2.0` on macOS
and `credential:secret_service@2.0` on Linux. Catalog installation brings in
the declared store adapter, and browser cookie operations request keys through
CTX's trusted credential protocol at runtime. A manually installed browser
adapter needs its dependency installed and trusted separately; unrelated
browser operations can still work if it is missing. Updating the store adapter
does not require rebuilding the browser adapter. Browser-specific identities
and policy stay in each browser adapter. KWallet remains a browser-specific
Linux fallback; a standalone KWallet write adapter is not yet provided.
