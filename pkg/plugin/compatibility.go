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

// BackendProfile describes transport mechanics, separately from the guest's
// domain contracts. Profiles are supplied by the selected backend factory.
// They never grant permissions or claim that subprocesses are sandboxed.
type BackendProfile struct {
	Name            string   `json:"name"`
	Protocols       []string `json:"protocols"`
	Concurrent      bool     `json:"concurrent"`
	Cancellation    string   `json:"cancellation"`
	ProcessOwner    string   `json:"processOwner"`
	NativeStreaming bool     `json:"nativeStreaming"`
	NativeCallbacks bool     `json:"nativeCallbacks"`
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
