// Package confine restricts one local process to a sandbox grant (docs/adr-sandboxing.md,
// section 4) and then becomes the guest. It is the mechanism behind `ext sandbox exec`: a runner
// (the plugin engine, workload's local driver, a product) starts that command with a grant, and
// the command calls Exec, which confines its own process with an OS driver, reports what it
// enforced, and replaces itself with the guest.
//
// The package owns mechanisms only. It never chooses a policy: the grant comes from the caller,
// computed with res/sandbox from what a package requests and what the consumer allows.
//
// A driver is one OS mechanism (Seatbelt on macOS, Landlock and seccomp on Linux, AppContainer
// and a job object on Windows). Each reports the classes it can enforce on this host
// (Capabilities); the library never picks a driver by name for the caller, and a launch that
// needs a class nobody enforces fails closed with sandbox.ErrUnsupported.
//
// Some classes cannot be enforced by a process that confines itself and then becomes the guest:
// a wall-time bound and a fresh or snapshot lifecycle need something that outlives the guest.
// The runner that starts the launch step enforces those, and says so in Launch.Runner; they are
// reported as enforced on its word.
package confine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/webong/ext/res/sandbox"
)

// Errors beyond res/sandbox's. Code maps them to the shared codes.
var (
	// ErrNotExecutable reports a grant that does not let the guest's own executable run.
	ErrNotExecutable = fmt.Errorf("%w: the grant does not allow executing the guest", sandbox.ErrDenied)
	// ErrNoDriver reports that no driver is available here, or that more than one is and the
	// launch did not name one, or that it named one that is not available.
	ErrNoDriver = fmt.Errorf("%w: no such confinement driver here", sandbox.ErrUnsupported)
	// ErrDriver reports a native confinement call that failed after the plan was accepted.
	ErrDriver = errors.New("confine: the operating system refused to confine the process")
)

// Code returns the stable code of err: res/sandbox's codes, "io" for ErrDriver or an exec
// failure, "" for nil.
func Code(err error) string {
	if err == nil {
		return ""
	}
	if c := sandbox.Code(err); c != "" {
		return c
	}
	return "io"
}

// Driver is one OS confinement mechanism.
type Driver interface {
	// Name is the driver's stable name, such as "seatbelt" or "landlock".
	Name() string
	// Capabilities reports what the driver enforces on this host. It probes and never
	// changes the process.
	Capabilities() sandbox.Capabilities
	// confine restricts the calling process to the grant; the guest is started by exec after it.
	confine(plan plan) error
}

// Drivers returns the drivers this build offers on this OS, in a fixed order. A driver whose
// mechanism is missing on this host is still listed; its Capabilities say why it enforces
// nothing.
func Drivers() []Driver { return platformDrivers() }

// Launch is one confined start.
type Launch struct {
	// Grant is what the guest may touch, already the overlap of a request and a policy
	// (sandbox.Grant).
	Grant sandbox.Policy
	// Accept names the classes the caller agrees to run without (sandbox.Enforce).
	Accept []string
	// Runner names the classes the caller enforces itself (wall time, lifecycle). They count
	// as enforced; the caller answers for them.
	Runner []string
	// Driver names the driver to use. Empty uses the only available driver, and fails when
	// there are several.
	Driver string
	// Path is the guest's executable, an absolute path that the grant allows executing.
	Path string
	// Args are the arguments after the executable name.
	Args []string
	// Env is the base environment the runner chose (for example PATH). The grant's entries are
	// added after it, and the values of its secrets from Secrets.
	Env []string
	// Secrets maps each secret name in the grant to the value the runner resolved from its
	// reference. A grant secret without a value is invalid.
	Secrets map[string]string
	// Dir is the working directory; empty keeps the launcher's.
	Dir string
}

// Status is the one line Exec writes to the status writer before it starts the guest: the
// report of an accepted launch, or the code of a refused one.
type Status struct {
	Report *sandbox.Report `json:"report,omitempty"`
	Code   string          `json:"code,omitempty"`
}

// plan is a checked launch, ready for a driver.
type plan struct {
	Launch
	report sandbox.Report
	env    []string
}

// Prepare checks a launch without changing anything: it picks the driver, checks the grant
// against what the driver and the runner enforce, and returns the report Exec would give.
func Prepare(l Launch) (sandbox.Report, error) {
	p, _, err := prepare(l)
	return p.report, err
}

func prepare(l Launch) (plan, Driver, error) {
	if err := l.Grant.Validate(); err != nil {
		return plan{}, nil, err
	}
	if !filepath.IsAbs(l.Path) || strings.ContainsRune(l.Path, 0) {
		return plan{}, nil, sandbox.ErrInvalid
	}
	for _, class := range l.Runner {
		if !slices.Contains(sandbox.Classes, class) {
			return plan{}, nil, sandbox.ErrInvalid
		}
	}
	driver, err := pick(l.Driver)
	if err != nil {
		return plan{}, nil, err
	}
	if !executable(l.Grant, filepath.ToSlash(l.Path)) {
		return plan{}, nil, ErrNotExecutable
	}
	env, err := environment(l)
	if err != nil {
		return plan{}, nil, err
	}
	caps := driver.Capabilities()
	if caps.Classes == nil {
		caps.Classes = map[string]sandbox.Support{}
	}
	merged := sandbox.Capabilities{Boundary: caps.Boundary, Driver: caps.Driver, Classes: map[string]sandbox.Support{}}
	for class, support := range caps.Classes {
		merged.Classes[class] = support
	}
	for _, class := range l.Runner {
		merged.Classes[class] = sandbox.Support{Enforced: true}
	}
	report := sandbox.Report{Boundary: merged.Boundary, Driver: merged.Driver, Grant: l.Grant}
	outcome, err := sandbox.Enforce(l.Grant, merged, l.Accept)
	report.Enforced, report.Unenforced = outcome.Enforced, outcome.Unenforced
	if err != nil {
		return plan{report: report}, nil, err
	}
	// No driver maps device names to native devices yet: every driver denies all devices.
	if len(l.Grant.Devices) > 0 {
		return plan{report: report}, nil, fmt.Errorf("%w: no driver grants a named device", sandbox.ErrUnsupported)
	}
	return plan{Launch: l, report: report, env: env}, driver, nil
}

func pick(name string) (Driver, error) {
	var available []Driver
	for _, d := range platformDrivers() {
		if c := d.Capabilities(); c.Boundary == sandbox.Confined && enforcesSomething(c) {
			available = append(available, d)
		}
	}
	if name != "" {
		for _, d := range available {
			if d.Name() == name {
				return d, nil
			}
		}
		return nil, ErrNoDriver
	}
	if len(available) != 1 {
		return nil, ErrNoDriver
	}
	return available[0], nil
}

func enforcesSomething(c sandbox.Capabilities) bool {
	for _, s := range c.Classes {
		if s.Enforced {
			return true
		}
	}
	return false
}

// executable reports whether a granted path at or above the guest allows execute.
func executable(grant sandbox.Policy, path string) bool {
	for _, p := range grant.Filesystem {
		if !slices.Contains(p.Access, sandbox.Execute) {
			continue
		}
		if path == p.Path || strings.HasPrefix(path, strings.TrimSuffix(p.Path, "/")+"/") {
			return true
		}
	}
	return false
}

// environment is the guest's whole environment: the runner's base, then the grant's entries,
// then its secrets with their resolved values. A name may appear once.
func environment(l Launch) ([]string, error) {
	seen := map[string]bool{}
	var env []string
	add := func(entry string) error {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" || strings.ContainsRune(entry, 0) || seen[name] {
			return sandbox.ErrInvalid
		}
		seen[name] = true
		env = append(env, entry)
		return nil
	}
	for _, e := range l.Env {
		if err := add(e); err != nil {
			return nil, err
		}
	}
	for _, e := range l.Grant.Environment.Entries {
		if err := add(e); err != nil {
			return nil, err
		}
	}
	for _, s := range l.Grant.Environment.Secrets {
		value, ok := l.Secrets[s.Name]
		if !ok {
			return nil, sandbox.ErrInvalid
		}
		if err := add(s.Name + "=" + value); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// Exec confines the calling process to the launch's grant and replaces it with the guest. It
// writes one Status line to status (when not nil) first: the report once the process is
// confined, or the code of the refusal. It returns only on failure; on POSIX a successful Exec
// never returns. On a platform without exec (Windows) it starts the guest confined, waits for it,
// and returns its exit code with a nil error.
//
// Call it from a process that does nothing else: confining is not undone.
func Exec(l Launch, status io.Writer) (int, error) {
	p, driver, err := prepare(l)
	if err != nil {
		writeStatus(status, Status{Code: Code(err)})
		return 0, err
	}
	if err := driver.confine(p); err != nil {
		writeStatus(status, Status{Code: Code(err)})
		return 0, err
	}
	report := p.report
	writeStatus(status, Status{Report: &report})
	return start(p)
}

func writeStatus(w io.Writer, s Status) {
	if w == nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}
