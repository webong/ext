# host v1

`cases.json` covers the snapshot of the local machine's resources in its wire form, the JSON every
binding exchanges. Each case is one snapshot object and whether it is valid. A binding decodes it into
its own model and validates it; a snapshot that does not decode (a field of the wrong type, or a number
out of range for its type) is invalid too.

**Wire form**: `os`, `architecture`, `observed_at`, `logical_cpus`, `cpu_capacity`, `cpu_status`,
`memory_bytes`, `memory_capacity_bytes`, `memory_status`, `storage`, `power`, `container`,
`uptime_seconds`, `boot_time` and `uptime_status`, in snake case throughout. A volume has `device`,
`mount_point`, `total_bytes`, `available_bytes`, `type` and `read_only`. `storage` may be absent (it is
empty), as may `power`, `type`, `read_only` (false) and the time remaining. Uptime is a number of
seconds, not a Go duration. Timestamps are RFC 3339.

**Rules**:

- `os` and `architecture` are not empty; `observed_at` and `boot_time` are RFC 3339 timestamps.
- `logical_cpus` is at least 1 and `cpu_capacity` is from 0 to `logical_cpus` (a container quota
  lowers it).
- `memory_bytes` is the physical memory and `memory_capacity_bytes` what is available to the process,
  never more than `memory_bytes`; both are 0 where memory is unsupported.
- Each status follows `capability/v1`: its reason must fit its state.
- A volume has a mount point (its device may be empty) and no more available bytes than total.
- Power, when present, has a state of `charging`, `discharging`, `charged` or `unknown`, a
  percentage from 0 to 100 and no negative time remaining.
- A container's state is `detected`, `none` or `unsupported`; `detected` is true exactly when the
  state is `detected`, and its evidence is non-empty exactly then, made of non-empty names sorted by
  bytes without repeats.
- `uptime_seconds` is not negative, and it and `boot_time` are both present or both absent.

Not covered, because it is each platform's own observation: the sources of every figure (cgroups,
`/proc`, sysctl, `ActivityManager`), container detection itself, and which volumes are listed.
