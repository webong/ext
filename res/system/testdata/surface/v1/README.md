# surface v1

`cases.json` covers the observation of the local desktop in its wire form, the JSON every binding
exchanges: a surface snapshot (displays and windows), a permission snapshot and an accessibility
snapshot, and the options that bound them. A case gives one snapshot object and whether it is valid; a
binding decodes it into its own model and validates it, and a snapshot that does not decode (a field of
the wrong type) is invalid too. Timestamps are RFC 3339.

**Surface snapshot** (`platform`, `coordinate_space`, `observed_at`, `displays`, `windows`,
`display_status`, `window_status`):

- `platform`, `coordinate_space` and `observed_at` are set.
- A display has an id, `bounds`, an optional `scale` (left out when unknown, never negative) and
  `primary`; display ids are unique and at most one display is primary.
- A window has an id, a `pid` of at least 1, `process_start_id` (which may be empty, where the OS
  exposes no process generation), `bounds` and `on_screen`; window ids are unique.
- Bounds have a positive width and height; coordinates may be negative.
- Each status follows `capability/v1`.

**Permission snapshot** (`platform`, `observed_at`, `accessibility`, `screen_recording`): a state is
`ready`, `denied`, `not-determined`, `not-required`, `unsupported` or `unavailable`, with an optional
`scope`. `ready`, `denied`, `not-determined` and `not-required` take no reason; `unsupported` takes
`unsupported` or `tool_missing`; `unavailable` takes none, `permission`, `no_device` or `tool_missing`.

**Accessibility snapshot** (`platform`, `observed_at`, `status`, `elements`): element ids are set and
unique; a `pid` is at least 1; a role is set; `process_start_id` may be empty; bounds are positive;
action names are not empty and `actions` may be left out; a `parent_id` (left out for a root) names
another element of the snapshot, and the parents have no cycle. A parent may come after its child.

**Options**: surface `maxDisplays` and `maxWindows` are 1 to 65,536 (defaults 64 and 4096);
accessibility `maxElements` is 1 to 65,536 and `maxDepth` 1 to 256 (defaults 1024 and 16). Go reads a zero
as the default, so zero is not a case.

Not covered, because it is each platform's own observation: which displays and windows exist, the
coordinate space a platform chooses, the accessibility tree a platform walks, and native input and
capture (the surface controller, pointer and media capture).
