# Host discovery

Host inventory belongs to the public `github.com/webong/ext/pkg/graph/system`
package. It works in a standalone CTX installation without adapter packages.
Adapter inventory describes provider capabilities and contexts separately.

```sh
ctx graph shells
ctx graph filesystems
ctx graph webviews
ctx graph scan
ctx graph vertices shell
ctx graph vertices filesystem
ctx graph vertices webview
ctx graph edges has-shell
ctx graph edges has-filesystem
ctx graph edges has-webview
```

`shells`, `filesystems`, and `webviews` discover the host, emit JSON, and refresh
only their corresponding graph records. `scan` refreshes all host inventories and adapter
inventory. Removed executables and mounts are removed from the next observation.
Stored snapshots are observations, not authorization or a guarantee that an
executable or mount will still be available when another command uses it.

## Shells

Discovery records each available executable path separately, its resolved
symlink target, and the sources that identified it. It checks executable files
and Unix execute permissions without starting the shell or reading startup
scripts or history.

On macOS and Linux, sources include `/etc/shells`, standard shell names across
absolute PATH entries, and the executable declared by `SHELL`. Registered or
configured shells can have arbitrary names. PATH discovery recognizes standard
names including Bash, Zsh, Fish, PowerShell, Nushell, Elvish, Xonsh, and common
POSIX shells; it cannot identify an arbitrarily named executable as a shell.

On Windows, discovery includes standard shell executables on PATH, `ComSpec`,
the system Command Prompt and Windows PowerShell locations, and installed
PowerShell versions under Program Files. This does not inspect shells inside
WSL distributions or remote machines.

`configured_default` reflects `SHELL` or `ComSpec`. It does not claim to identify
the process actually invoking CTX. Existing `shell-session` records continue to
describe the session separately. Versions are not collected by executing tools.

## Mounted filesystems

- macOS uses the native mount table and its cached space statistics.
- Linux uses `/proc/self/mountinfo` and filesystem statistics, including escaped
  mount paths, bind mounts, and the calling process's mount namespace.
- Windows enumerates volume mount paths, including mounted folders, and logical
  drives including mapped network drives. Removable drives without media are
  omitted. Filesystem names, volume labels, read-only status, and disk space come
  from native Windows APIs.

Each entry includes its mount point, source, filesystem type and read-only
status. `space` contains `total_bytes`, `free_bytes`, and `available_bytes` when
the OS provides them. Available bytes reflect the calling user's ability to
allocate space. Windows total bytes can also reflect a user quota. Missing
space or volume details produce `detail_error`; absent type details also mean
read-only status could not be determined. Network-source URL user information
is removed from output and stored records. Mount options and file contents are
not collected.

This lists mounted filesystems visible to CTX, including pseudo-filesystems on
Linux. It does not list unmounted disks, every filesystem format the OS could
support, or mounts outside a container's visible namespace. Statistics are a
point-in-time view; shared storage pools must not be summed as independent disks.
Network mount queries can depend on the availability of the remote server.

## Shared web engines and webviews

`ctx graph webviews` inventories web embedding runtimes independently of CTX
adapters. Each record identifies its name, rendering engine (`webkit` or
`blink` for the built-in collectors), embedding API, scope, source, and evidence
location. Runtime versions, library ABI generations, resolved paths, and ELF
architecture are included where available. An ABI generation is not a WebKit
or Chromium release version.

| Platform | Discovery | Evidence |
| --- | --- | --- |
| macOS | System WebKit / WKWebView | System framework and its bundle version; supports frameworks whose binaries live in the dyld shared cache |
| Windows | Evergreen WebView2 / Blink | Microsoft's version registration in machine and user registry locations, across both registry views |
| Linux | Shared WebKitGTK / WebKit and Qt WebEngine / Blink | ELF shared libraries in standard, multiarch, `ld.so.conf`, and absolute `LD_LIBRARY_PATH` directories |

Linux recognizes WebKitGTK API generations 4.0, 4.1, and 6.0, and Qt WebEngine
Core generations 5 and 6. Aliases of one library are reported once; distinct
libraries and ABI generations remain separate. Architecture is the ELF machine
identifier. Windows reports one entry per scope/version when registry views
repeat a registration; a registry view does not establish runtime architecture.
No WebView2 registration produces an empty inventory rather than assuming that
Edge or Chrome provides a shared runtime.

These are installation observations. Discovery does not load native libraries,
create a webview, validate dependent libraries or GUI availability, or promise
that an arbitrary app can use a runtime. Windows registry evidence can be stale.
macOS version metadata is read with the system `plutil` utility; unavailable
metadata retains the framework observation with `detail_error`.

App-private Electron/CEF/Chromium bundles, fixed-version WebView2 bundles,
arbitrary Qt SDK installations, sandbox-private runtimes, and remote/mobile
devices are outside this host scan. An installed browser alone does not count
as a shared engine. Cookies, sessions, policies, profiles, and permissions remain
owned by each embedding app; engine inventory provides no access to them.
App-specific observations and operations can be supplied by their adapters,
and library consumers can project additional runtimes through `ObserveHost`.

Reference contracts: [Apple WKWebView](https://developer.apple.com/documentation/webkit/wkwebview/),
[Microsoft WebView2 runtime detection](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution),
[WebKitGTK API generations](https://webkitgtk.org/reference/webkit2gtk/2.39.1/migrating-to-webkitgtk-6.0.html),
and [Qt WebEngine](https://doc.qt.io/qt-6/qtwebengine-overview.html).

## Library API

Use discovery without a store, or persist the result in an existing system
graph:

```go
inventory, err := systemgraph.DiscoverHost(ctx)
// inventory.Shells, inventory.Filesystems, and inventory.Webviews are typed observations.

inventory, err = system.ScanHost(ctx)
// ScanHost discovers and atomically projects all host inventories into the graph.
```

`DiscoverShells`, `DiscoverFilesystems`, and `DiscoverWebviews` can be called
independently.
`Graph.ObserveHost` accepts a `HostInventory`; a nil slice preserves that part
of the existing inventory, while a non-nil empty slice removes its old entries.
The schema uses `shell`, `filesystem`, `webview`, and `host-inventory` vertices
linked to `machine/local`. The generic graph store owns persistence and transactions;
`pkg/graph/system` owns host discovery and its vocabulary.

## Operation requirements and preparation

Host operations declare typed `OperationRequirements`. An operation can require
any combination of a shell, filesystem, and webview. Requirements are
conjunctive: every requested resource must match.

```sh
ctx graph resolve shell --name zsh
ctx graph resolve shell --select /bin/zsh
ctx graph resolve filesystem --path ./export.json --writable --min-free 1048576
ctx graph resolve filesystem --type apfs --type ext4 --writable
ctx graph resolve webview --engine webkit --api WKWebView
ctx graph resolve webview --engine webkit --api WebKitWebView --abi 4.1
```

The commands emit JSON candidates with per-category observation timestamps and
return an error when the requirement cannot be satisfied. `--select` pins an
executable, mount point, or runtime location; an unavailable or incompatible
explicit choice produces an error. It does not select a different resource.
`--type` can be repeated for alternative filesystem types. Webview engine, API,
ABI, architecture, and known runtime version requirements match exactly.

`Graph.ReadHost` reads the typed stored inventory without probing. `ResolveHost`
refreshes only requested stale categories, with a default maximum age of five
seconds. CLI `--max-age` and library `OperationRequirements.MaxAge` change that
policy; negative durations force discovery. Changes to the shell's effective
PATH/default environment or the shared-library search environment invalidate
the corresponding cache. Host/user identity and Linux mount/user namespace
fingerprints also invalidate cached observations; raw environment values are
not stored in these fingerprints. `ShellRequirement.SearchPath` supports an effective
PATH without changing the calling process's environment.

When a filesystem requirement includes a path that does not exist yet, CTX
uses its nearest existing ancestor and resolves symlinks. The OS determines
the actual mount: macOS filesystem statistics handle volume mappings, Linux
uses the path handle's mount ID, and Windows queries the volume mount path.
These use the [Linux mount identity contract](https://docs.kernel.org/filesystems/proc.html)
and [Windows volume path API](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getvolumepathnamew).
`ResolveHost` with `Writable` filters mount read-only status; it does not prove
directory access or allocate a file.

Before using resources, call `PrepareHost`. It discovers current candidates,
selects one per requirement, checks the executable and its resolved path,
rechecks the selected filesystem and available bytes, and checks directory
write access when required. It creates no filesystem probe files. Named shell
choices follow the effective PATH; otherwise an unambiguous configured default
can be selected. Multiple remaining mounts, runtimes, or shells require an
explicit selection or narrower requirements.

```go
requirements := systemgraph.OperationRequirements{
    Operation: "report.export",
    Shell: &systemgraph.ShellRequirement{Name: "zsh"},
    Filesystem: &systemgraph.FilesystemRequirement{
        Path: outputPath,
        Writable: true,
        MinimumAvailableBytes: uint64(len(report)),
    },
}
prepared, err := system.PrepareHost(ctx, requirements, nil)
if err != nil {
    return err
}
// Launch prepared.Shell.Path and write to the requested outputPath.
```

Webview preparation requires a `WebviewValidator` from the embedding backend:

```go
prepared, err := system.PrepareHost(ctx, systemgraph.OperationRequirements{
    Operation: "ui.open",
    Webview: &systemgraph.WebviewRequirement{
        Engine: supportedEngine,
        API: supportedAPI,
        ABIVersion: supportedABI,
        Select: selectedRuntimeLocation,
    },
}, backend.ValidateWebview)
```

The backend owns loading, native compatibility, dependencies, and application
permissions. CTX does not infer those from an engine name. A missing validator,
ambiguous runtime, or failed backend validation blocks preparation. Additional
host observations can be projected with `ObserveHost`; `PrepareHost` performs
local built-in discovery, so a private runtime outside that discovery remains
the owning adapter/backend's responsibility.

CTX now uses preparation before interactive `ctx shell` launches, protected
cookie/policy/credential file outputs, and manager image archive staging.
Shell selection preserves `--shell`, `CTX_SHELL`, profile configuration, and
the existing platform default precedence, and resolves names using the effective
profile PATH. Explicit output paths and `TMPDIR`/platform temporary directories
remain the selected destinations. JSON exports declare their encoded byte
size; streaming outputs and image archives can check only a minimum because
their final size is unknown.

`HostRequirementError` identifies the operation and resource kind and preserves
underlying errors for `errors.Is` and `errors.As`. Preparation is a point-in-time
check, not a lock or capacity reservation. The eventual executable launch and
exclusive output open still enforce native errors, permissions, and overwrite
protection. Direct `ctx shell -- <command>` execution retains its command mode.

## Running processes and resources

Process discovery is another capability of the graph library itself. It works
without adapters and does not contain product-name dispatch or a browser list.
These commands take one snapshot and exit:

```sh
ctx graph processes
ctx graph processes --limit 2048 --timeout 15s
ctx graph process <pid> --resource-limit 512 --timeout 10s
ctx graph vertices process
ctx graph vertices application
ctx graph vertices executable
ctx graph vertices process-resource
ctx graph edges parent-of
ctx graph edges uses-resource
```

`processes` collects caller-visible process identities, parents, executable
paths, and owners. `process <pid>` additionally collects resource evidence and
usage. Process names are supplied by the OS; arbitrary applications can be
identified without installing their CTX adapters. On macOS, an executable's
outer `.app` bundle is also recorded as its owning application, including nested
helpers. On Linux and Windows the executable identity is reported; desktop-file,
package, and installer ownership are not inferred from executable basenames.

`graph scan` now includes process enumeration. Resource inspection stays targeted
so an ordinary inventory does not traverse every process's descriptors. The CLI
also refreshes graph records when emitting its JSON result. A collector error or
expired deadline prevents that process observation from being persisted.

| Platform | Identity | Detailed resources |
| --- | --- | --- |
| macOS | Native `kern.proc` and executable-path metadata | Structured, bounded system `lsof` output for files, mappings, sockets and descriptor-based IPC; system `ps` for memory and cumulative CPU time |
| Linux | `/proc/<pid>` within the caller's namespaces | File descriptors, file-backed mappings, TCP/UDP IPv4/IPv6 and Unix sockets, pipe/anonymous-inode evidence; memory, threads and CPU counters |
| Windows | Tool Help, process creation time, executable path and owner SID | Process Snapshotting for named handles/kernel objects and file-backed mappings; module enumeration fallback; IP Helper TCP/UDP IPv4/IPv6 tables; working set and CPU time |

Windows Process Snapshotting requires Windows 8.1 / Server 2012 R2 or later and
sufficient process-query rights. Handle collection and mapping collection have
separate permission requirements; one can succeed when the other fails. Paths
from snapshots may use the NT device namespace. TCP/UDP collection does not cover
Windows Unix-domain sockets. A module fallback is marked partial because it does
not enumerate all mapped files. Kernel-object handle evidence does not imply an
IPC protocol or reveal message contents. macOS descriptor inspection does not
enumerate every Mach port. Other operating systems currently return an explicit
unsupported error; additional collectors can use the same contracts.

Every collected category has `coverage` with a state: `complete`, `partial`,
`permission-denied`, `unsupported`, `exited`, or `unavailable`, plus a diagnostic
when useful and an observation timestamp. Complete means the documented category
was collected within the caller's visibility. Processes hidden by a sandbox,
namespace, or OS access policy cannot be counted as missing accessible records.
An empty successful collection is distinct from failed collection. Usage fields
are optional; zero and unavailable are different. CPU values are cumulative
seconds, not an instantaneous percentage; consumers can calculate a rate from
successive observations of the same process instance.

The defaults are 4096 processes, 1024 resources **per category**, and a ten-second
collection deadline. Record limits are capped at 65536. A limit produces partial
coverage; native APIs may enumerate a larger internal table before CTX filters
it. Proc-file and utility output reads are bounded. Context cancellation is
checked between native calls and during loops; an in-progress OS call cannot
always be interrupted immediately. No monitor, daemon, privilege escalation, or
application extension is installed.

Only metadata is collected. File contents, application memory contents, command
arguments, environment values, cookies, and IPC payloads are not returned or
persisted. macOS executable lookup reads the native process-arguments record but
uses only its executable-path prefix. An endpoint observation does not establish
HTTP, a browser debugging protocol, or permission to control the application.
Product-specific interpretation belongs to the corresponding adapter.

### Process library API and reconciliation

```go
options := systemgraph.ProcessOptions{
    MaxProcesses: 4096,
    MaxResources: 1024,
    Timeout: 10 * time.Second,
}

inventory, err := systemgraph.DiscoverProcesses(ctx, options)
inspection, err := systemgraph.InspectProcess(ctx, pid, options)

// Discovery does not require a store. Persist either result explicitly:
err = system.ObserveProcesses(ctx, inventory)
err = system.ObserveProcesses(ctx, inspection)

// Convenience operation for enumeration plus persistence:
inventory, err = system.ScanProcesses(ctx, options)
```

`DiscoverHost` / `ScanHost` retain their existing shell/filesystem/webview API;
`ScanProcesses` is separate for consumers that do not need process inventory.
The CLI `graph scan` calls both, then scans adapters. Those inventories are
separate graph transactions.

Process node IDs combine host/boot/visibility identity, PID, and native start
identity. Inspection checks that identity and executable again after resource
collection; exit, PID reuse, or an executable change rejects the observation.
Entries whose start identity cannot be established are returned with their
coverage and retained in the inventory marker’s `unidentified_processes` field,
but are not projected as stable process nodes.

The projection creates `process`, `executable`, `application`, and
`process-resource` vertices, and `runs`, `parent-of`, `executes`,
`belongs-to-application`, and `uses-resource` relationships. Resource vertices
are scoped to a process instance. Their `kind` attribute is `file`, `mapping`,
`socket`, or `ipc`; sockets retain protocol, addresses and state where available.

A complete enumeration removes absent process instances and their resources.
A limited or otherwise partial enumeration preserves unseen records. A targeted
inspection replaces only that PID's instance and the resource categories it
inspected, even when that category reports partial or denied coverage; old
resources are not silently presented as a successful new inspection. Later
identity-only scans retain resource evidence and coverage with their original
timestamps. Parent edges come from a shared enumeration; a targeted refresh may
retain an older parent edge if the parent PID still agrees, without changing the
edge's timestamp. All observations are point-in-time evidence, not atomic views
or guarantees that a resource remains available. Older observations are rejected
when they would overwrite newer inventory or process evidence.

Consumers such as Xallet can schedule these library calls, inspect graph changes,
and add their own interpretations. CTX's graph command itself remains short-lived.

Native references: [Linux proc](https://docs.kernel.org/filesystems/proc.html),
[Apple process APIs](https://github.com/apple-oss-distributions/xnu/blob/main/libsyscall/wrappers/libproc/libproc.h),
[Windows process snapshot flags](https://learn.microsoft.com/en-us/windows/win32/api/processsnapshot/ne-processsnapshot-pss_capture_flags),
and [Windows socket ownership](https://learn.microsoft.com/en-us/windows/win32/api/tcpmib/ns-tcpmib-mib_tcptable_owner_pid).

### Validation

The process tests use a disposable subprocess, not an installed application. The
fixture opens a uniquely named file and file mapping, TCP and UDP loopback
sockets, and a pipe. Its parent checks identities, resource ownership, listener
state, usage, metadata privacy, limits, resource closure, and process exit. A
macOS fixture also launches from a temporary `.app` path to check generic bundle
attribution. Linux additionally checks permission-denied results against a
non-dumpable child, under a non-root user without ptrace bypass privileges.

Portable tests cover projection, stale observations, simulated PID reuse,
partial/denied inventories, orphan cleanup, coverage timestamps, preservation of
other host inventory, and invalid CLI arguments. Platform tests cover native
resource parsing, output bounds, address formats, and Windows snapshot ABI
layout. Real PID reuse is not forced, and a passing build is not a native test.

Run ordinary contract/parser tests with:

```sh
(cd pkg/graph && go test ./...) && (cd src/ctx && go test ./internal/app)
```

Enable native fixtures explicitly (the shell must permit subprocess inspection
and binding temporary loopback sockets):

```sh
EXT_GRAPH_PROCESS_NATIVE_TESTS=1 go test -race -v ./pkg/graph/system \
  -run '^(TestNativeProcess|TestProcess)' -count=3 -timeout=5m
```

On PowerShell, set `$env:EXT_GRAPH_PROCESS_NATIVE_TESTS = '1'` before the same
`go test` command. The native GitHub Actions workflow now runs this step on
macOS, Linux, and Windows. It fails when expected fixture resources are missing;
partial coverage is not used to excuse a missing expected file or socket.
The Linux permission test explicitly skips root execution because that would
not establish the unprivileged denial behavior.

Validation recorded on 2026-10-04:

| Environment | Evidence |
| --- | --- |
| macOS 15.7.8 / arm64 | Native process lifecycle, generic app bundle attribution, graph tests with race detection, parser/limit regressions, and the existing repository suite |
| Linux / arm64, OrbStack Alpine container | Native fixture, resource lifecycle, real permission denial, parser and projection tests; run three times with an unprivileged UID, all capabilities dropped, no external networking, and a read-only root filesystem |
| Windows / amd64 | Test executable cross-compiled; native execution pending on the Windows CI runner |

These results cover the named environments. Release-level claims for additional
OS versions and architectures need passing runs on those targets. Windows and
macOS protected-process scenarios beyond ordinary fixture permissions, and
extended resource-churn/exit-during-inspection stress, still need separate native
coverage. CI configuration in the working tree is not evidence that a remote
CI run has passed.
