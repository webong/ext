# systemd service adapter

`github.com/webong/ext/adapters/systemd` implements the portable
`res/system/service` contract for a per-user systemd manager. It owns
`systemctl --user` invocation, unit naming, status parsing, and registration
ordering; the caller supplies the unit directory, service name, executable,
arguments, environment, working directory, restart policy, and any readiness
probe.

It is an adapter rather than part of `res/system` because `systemctl` is a
selectable native tool: a Linux host may run another service manager. No shared
library selects this backend by name.

The package never chooses a product identity, installation layout, port, key
provider, endpoint, or health protocol. The companion ctx adapter entry point
only validates and reports whether `systemctl` is installed.
