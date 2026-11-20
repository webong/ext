//go:build darwin

package confine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/webong/ext/res/sandbox"
)

func platformDrivers() []Driver { return []Driver{seatbelt{}} }

// seatbelt confines a process with a Seatbelt profile generated from the grant and applied with
// sandbox_init before exec; the guest inherits it. sandbox_init is unsupported for third parties
// and may change with a macOS release (docs/adr-sandboxing.md, decision 3), so the profiles are
// tested on each release in CI.
type seatbelt struct{}

func (seatbelt) Name() string { return "seatbelt" }

// seatbeltBase is what any process needs to start on macOS: the dynamic linker and system
// libraries (read and map, never write), and the harmless devices. It is added to every
// profile and is not part of the grant.
var seatbeltBase = []string{
	"/usr/lib",
	"/System/Library",
	"/System/Cryptexes",
	"/System/Volumes/Preboot/Cryptexes",
	"/private/var/db/dyld",
	"/Library/Apple/usr/lib",
	"/dev/null",
	"/dev/zero",
	"/dev/random",
	"/dev/urandom",
}

func (seatbelt) Capabilities() sandbox.Capabilities {
	enforced := sandbox.Support{Enforced: true}
	missing := sandbox.Support{Reason: sandbox.ReasonUnsupported}
	classes := map[string]sandbox.Support{
		"filesystem":                    enforced,
		"network.none":                  enforced,
		"network.open":                  enforced,
		"process":                       enforced,
		"devices":                       enforced,
		"environment":                   enforced,
		"resources.cpuTimeMs":           enforced,
		"resources.openFiles":           enforced,
		"network.ports":                 missing, // Seatbelt filters remote ports only for loopback and named hosts
		"network.proxy":                 missing, // the egress proxy is phase 3
		"resources.memoryBytes":         missing, // macOS does not enforce RLIMIT_AS
		"resources.milliCpu":            missing,
		"resources.pids":                missing, // RLIMIT_NPROC counts every process of the user
		"resources.wallTimeMs":          missing, // only a runner that outlives the guest can bound wall time
		"resources.writeBytes":          missing, // RLIMIT_FSIZE bounds each file, not the total
		"lifecycle.fresh":               missing, // lifecycles are the runner's
		"lifecycle.workspace":           missing,
		"lifecycle.snapshot.filesystem": missing,
		"lifecycle.snapshot.memory":     missing,
	}
	if !seatbeltAvailable {
		for class := range classes {
			classes[class] = missing
		}
	}
	return sandbox.Capabilities{Boundary: sandbox.Confined, Driver: "seatbelt", Classes: classes}
}

func (seatbelt) confine(p plan) error {
	if err := applyRlimits(p.Grant.Resources); err != nil {
		return err
	}
	return applySeatbelt(seatbeltProfile(p))
}

// seatbeltProfile is the SBPL profile of a plan: everything denied, then the base, then the
// grant. Metadata reads (stat, not directory listings or contents) are allowed everywhere,
// since resolving any path stats its parents.
func seatbeltProfile(p plan) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString("(allow file-read-metadata)\n")
	// The dynamic linker opens the root directory itself (not anything below it) at start.
	b.WriteString("(allow file-read* (literal \"/\"))\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow process-info* (target self))\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow file-write-data (literal \"/dev/null\"))\n")
	for _, path := range seatbeltBase {
		fmt.Fprintf(&b, "(allow file-read* file-map-executable (subpath %s))\n", sbplString(path))
	}
	for _, entry := range p.Grant.Filesystem {
		path := sbplString(entry.Path)
		if slices.Contains(entry.Access, sandbox.Read) {
			fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", path)
		}
		if slices.Contains(entry.Access, sandbox.Write) {
			fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", path)
		}
		if slices.Contains(entry.Access, sandbox.Execute) {
			fmt.Fprintf(&b, "(allow process-exec file-map-executable file-read-data (subpath %s))\n", path)
		}
	}
	if p.Grant.Process.Children {
		b.WriteString("(allow process-fork)\n")
	}
	if p.Grant.Network.Mode == sandbox.NetworkOpen {
		b.WriteString("(allow network*)\n(allow system-socket)\n(allow mach-lookup)\n")
	}
	return b.String()
}

// sbplString quotes s as an SBPL string literal.
func sbplString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
