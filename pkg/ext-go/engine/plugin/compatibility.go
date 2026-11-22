package plugin

import (
	"fmt"
	"slices"
)

// NegotiateProtocol selects the first locally preferred exact protocol offered
// by a peer. Offers must come from reviewed metadata or an authenticated native
// bootstrap. This does not authenticate a peer or change the v1 hello format.
// Only pass versions for which the caller has an installed implementation.
func NegotiateProtocol(preferred, offered []string) (string, error) {
	for _, versions := range [][]string{preferred, offered} {
		if len(versions) == 0 || len(versions) > 32 {
			return "", ErrInvalid
		}
		seen := map[string]bool{}
		for _, version := range versions {
			if !identifier.MatchString(version) || seen[version] {
				return "", ErrInvalid
			}
			seen[version] = true
		}
	}
	for _, version := range preferred {
		if slices.Contains(offered, version) {
			return version, nil
		}
	}
	return "", fmt.Errorf("%w: no common protocol", ErrUnsupported)
}

// The sandbox boundary levels of docs/adr-sandboxing.md: where isolation is enforced
// for a guest. They are the same strings as res/sandbox's levels; the engine does not
// import that module, so it keeps its own copy.
const (
	// BoundaryInProcess shares the host's address space and authority.
	BoundaryInProcess = "in-process"
	// BoundaryProcess is a separate process with the user's full authority.
	BoundaryProcess = "process"
	// BoundaryConfined is a separate process restricted by OS policy.
	BoundaryConfined = "confined"
	// BoundaryRuntime has no syscall ABI, only the imports the host supplies.
	BoundaryRuntime = "runtime"
	// BoundaryContainer is a process in namespaces managed by a container runtime.
	BoundaryContainer = "container"
	// BoundaryKernel has its syscalls served by a separate kernel.
	BoundaryKernel = "kernel"
)

// Boundaries lists every boundary level.
var Boundaries = []string{BoundaryInProcess, BoundaryProcess, BoundaryConfined, BoundaryRuntime, BoundaryContainer, BoundaryKernel}

// ValidBoundary reports whether b is one of the boundary levels.
func ValidBoundary(b string) bool { return slices.Contains(Boundaries, b) }

// BackendProfile describes transport mechanics, separately from the guest's
// domain contracts. Profiles are supplied by the selected backend factory.
// Boundary states where a guest on this backend is isolated, honestly: a
// backend that runs its guest in the host's process says in-process however it
// is wrapped. It is empty when the backend does not say. A profile never grants
// permissions; a boundary is a fact about the backend, not a policy.
type BackendProfile struct {
	Name            string   `json:"name"`
	Protocols       []string `json:"protocols"`
	Concurrent      bool     `json:"concurrent"`
	Cancellation    string   `json:"cancellation"`
	ProcessOwner    string   `json:"processOwner"`
	NativeStreaming bool     `json:"nativeStreaming"`
	NativeCallbacks bool     `json:"nativeCallbacks"`
	Boundary        string   `json:"boundary,omitempty"`
}

// CheckRequirements is a preflight for exact domain operations, including
// optional SDK capabilities. Call before launch; Open still verifies and checks
// the complete descriptor. A preflight is never an admission decision.
func CheckRequirements(d Descriptor, requirements []Requirement) error {
	if err := d.Validate(); err != nil {
		return err
	}
	for _, want := range requirements {
		if _, err := Select([]Descriptor{d}, want); err != nil {
			return fmt.Errorf("require %s@%s/%s: %w", want.Contract.Name, want.Contract.Version, want.Operation, err)
		}
	}
	return nil
}
