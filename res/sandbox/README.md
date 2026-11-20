# res/sandbox

The sandbox contract that ext and [workload](https://github.com/webong/workload) share, from
[the sandboxing ADR](../../docs/adr-sandboxing.md). A sandbox is three decisions: the
**boundary** (where isolation is enforced), the **policy** (what code can touch) and the
**lifecycle** (what survives between runs).

This module is data and rules only. It confines nothing and starts nothing, and it uses only the
standard library and nothing else in ext, so a runner that must stay on the standard library can
depend on it. Mechanisms live elsewhere:
- `res/system/service/confine` confines one local process, through `ext sandbox exec`;
- workload's drivers run containers and VMs.

```go
import "github.com/webong/ext/res/sandbox"
```

| Piece | What it is |
| --- | --- |
| `Boundary` | `in-process`, `process`, `confined`, `runtime`, `container` or `kernel`. Not a ranking: a consumer states the set it accepts (`Accept`) |
| `Policy` | Filesystem paths and their access, network (`none`, `ports`, `proxy` with a host allowlist, or `open`), child processes, devices, resource bounds, environment, lifecycle (`fresh`, `workspace` or `snapshot`). The same shape is a request, a policy and a grant |
| `Grant` | The overlap of what a package requests and what the consumer's policy allows, plus what was refused. A request never grants anything |
| `Capabilities` | What one runner can enforce, class by class, with a fixed reason for anything missing: `unsupported`, `permission` or `tool_missing` |
| `Enforce` | Fails closed: a class the grant needs that the runner cannot enforce stops the launch with `unsupported`, unless the caller accepted it unenforced |
| `Report` | What every launch returns: boundary, driver, grant, refusals, and the classes enforced and unenforced. `Sandboxed` tells whether it meets the evaluator isolation `sandboxed` |
| `Plan` | All of the above in one call, before a launch |

Errors carry a stable code (`Code`): `invalid`, `denied` or `unsupported`.

## Fixtures

`testdata/v1/cases.json` is the contract; `testdata/codes.json` lists its codes. Every
implementation runs every section, and where an implementation and a fixture disagree, the
fixture decides. The Go runner is `fixtures_test.go`; `EXT_SANDBOX_TESTDATA` points it at a copy
elsewhere. A change to an existing case makes a `v2` directory, so a binding that has not caught up
keeps running `v1`.
