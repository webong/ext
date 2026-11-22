// Package sandbox is the portable sandbox contract that ext and github.com/webong/workload
// share (docs/adr-sandboxing.md): the boundary levels, the policy and lifecycle vocabulary,
// the rules that turn a request and a policy into a grant, the check of a grant against what a
// runner can enforce, and the report every launch returns.
//
// It is data and rules only. It confines nothing, starts nothing and imports nothing but the
// standard library, so a runner that must stay on the standard library can depend on it. The
// mechanisms that enforce a grant live elsewhere: src/cto/internal/confine for one local
// process, workload's drivers for containers and VMs.
//
// The behaviour is fixed by the fixtures under testdata/v1, which every implementation runs.
// Where this package and a fixture disagree, the fixture is right.
package sandbox

import "errors"

// The errors of the contract. Code maps each to its stable code. Error text never carries a
// value it was given.
var (
	// ErrInvalid reports a malformed boundary, policy, capability set, class or report.
	ErrInvalid = errors.New("sandbox: invalid")
	// ErrDenied reports a boundary outside the set the consumer accepts.
	ErrDenied = errors.New("sandbox: boundary not accepted")
	// ErrUnsupported reports a grant that needs a class the runner cannot enforce and that the
	// caller did not accept unenforced.
	ErrUnsupported = errors.New("sandbox: class not enforced")
)

// Code returns the stable code of err: "invalid", "denied" or "unsupported", or "" for nil or
// an error that is not one of this package's.
func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrDenied):
		return "denied"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	}
	return ""
}

// Boundary is where isolation is enforced. The levels are not a ranking: each moves the attack
// surface somewhere else. A consumer states the set it accepts.
type Boundary string

// The boundary levels.
const (
	// InProcess shares the host's address space and authority.
	InProcess Boundary = "in-process"
	// Process is a separate process with the user's full authority.
	Process Boundary = "process"
	// Confined is a separate process on the host kernel, restricted by OS policy.
	Confined Boundary = "confined"
	// Runtime has no syscall ABI; only the imports the host supplies.
	Runtime Boundary = "runtime"
	// Container is a process in namespaces on the host kernel, managed by a container runtime.
	Container Boundary = "container"
	// Kernel has its syscalls served by a separate kernel: a userspace kernel or a guest kernel
	// behind hardware virtualization.
	Kernel Boundary = "kernel"
)

// Boundaries lists every level, in the order of docs/adr-sandboxing.md.
var Boundaries = []Boundary{InProcess, Process, Confined, Runtime, Container, Kernel}

// Valid reports whether b is one of the levels.
func (b Boundary) Valid() bool {
	for _, known := range Boundaries {
		if b == known {
			return true
		}
	}
	return false
}

// Accept checks b against the set a consumer accepts. An empty set accepts every level. It
// returns ErrInvalid when b or a member of the set is not a level, and ErrDenied when b is not
// in the set.
func Accept(accepted []Boundary, b Boundary) error {
	if !b.Valid() {
		return ErrInvalid
	}
	for _, a := range accepted {
		if !a.Valid() {
			return ErrInvalid
		}
	}
	if len(accepted) == 0 {
		return nil
	}
	for _, a := range accepted {
		if a == b {
			return nil
		}
	}
	return ErrDenied
}
