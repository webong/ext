# capability v1

How a package says what this platform cannot do and why, so a consumer asks once and never branches
on an OS name (`docs/adr-architecture.md`, rule 2). Every collection status has a `state`:

| State | Meaning | Reason |
| --- | --- | --- |
| `complete` | everything was observed | none |
| `partial` | some of it was observed, or it was bounded | none |
| `permission-denied` | the platform asked for a permission that was not granted | `permission`, or none |
| `unsupported` | this platform cannot observe it | `unsupported` or `tool_missing` |
| `unavailable` | it can be observed here but not now | `permission`, `no_device`, `tool_missing`, or none |

The reasons are fixed: `unsupported` on this platform, `permission` not granted, `no_device`, and
`tool_missing` (the native tool is not installed; the adapter that owns the tool reports it).

A failed native read is `unavailable` and says why with one of the same reasons: `permission` when
the system refused the read, `tool_missing` when a program or library it needs is not installed, and
`no_device` otherwise, which covers a device, session or source that is absent or did not answer (no
display to open, no focused application, no resolver configuration, a process that went away during
the read). Go's observers always give the reason (`internal/capability.Unavailable`). The empty
reason stays valid for `unavailable` until the Swift, Java and C# read sites give one too.
`cases.json` lists each pair and whether it is valid. A package runs it against its
own status type: network has Go `CollectionStatus.Validate`, Swift `NetworkCollectionStatus.validate`
and the Java `NetworkCollectionStatus` constructor; host and surface share Go's
`process.CollectionStatus.Validate` and have Swift `ResourceCollectionStatus.validate` and the Java
`ResourceCollectionStatus` constructor (host), and Swift `SurfaceCollectionStatus.validate` with the
Java `SurfaceCollectionStatus` constructor (surface). The C# Windows libraries run it through the
`ClipboardStatus` constructor (`SystemClipboard.Status`) and the `AudioStatus` constructor
(`SystemAudio.Status`). A permission state has its own list in
`surface/v1`: `ready`, `denied`, `not-determined` and `not-required` take no reason, and
`unsupported` and `unavailable` follow the table above.
