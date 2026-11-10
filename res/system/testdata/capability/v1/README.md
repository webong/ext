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
`tool_missing` (the native tool is not installed; the adapter that owns the tool reports it). A
failed native read has no reason in this set, so an `unavailable` status may leave it out until a
cause is known. `cases.json` lists each pair and whether it is valid. A package runs it against its
own status type: network has Go `CollectionStatus.Validate`, Swift `NetworkCollectionStatus.validate`
and the Java `NetworkCollectionStatus` constructor; host and surface share Go's
`process.CollectionStatus.Validate` and have Swift `ResourceCollectionStatus.validate` and the Java
`ResourceCollectionStatus` constructor (host).
