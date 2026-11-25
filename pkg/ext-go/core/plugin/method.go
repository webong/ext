package plugin

import (
	"fmt"
	"strings"
)

// ResolveMethod maps a dotted method name onto one declared contract and
// operation, for hosts and guests that address operations as a single string
// "<contract name>.<operation>". Contract names may themselves contain dots.
//
// The longest declared contract name that prefixes the method wins, and the
// rest is the operation. When none does, the first dot-separated segment is
// tried: if exactly one declared contract name starts with that segment plus
// ".", that contract is used and everything after the segment is the
// operation. Otherwise the method is unresolved.
//
// The result names a contract only. Whether the operation exists, and its
// surface, are still checked by Lookup and by the session at call time.
func (d Descriptor) ResolveMethod(method string) (ContractRef, string, error) {
	var best ContractRef
	for _, c := range d.Contracts {
		if strings.HasPrefix(method, c.Name+".") && len(c.Name) > len(best.Name) {
			best = c.ContractRef
		}
	}
	if best.Name != "" {
		return best, strings.TrimPrefix(method, best.Name+"."), nil
	}
	if head, operation, ok := strings.Cut(method, "."); ok && operation != "" {
		var matches []ContractRef
		for _, c := range d.Contracts {
			if strings.HasPrefix(c.Name, head+".") {
				matches = append(matches, c.ContractRef)
			}
		}
		if len(matches) == 1 {
			return matches[0], operation, nil
		}
	}
	return ContractRef{}, "", fmt.Errorf("%w: no declared contract for method %q", ErrUnsupported, method)
}

// Method is the inverse of ResolveMethod: "<contract name>.<operation>".
func Method(ref ContractRef, operation string) string { return ref.Name + "." + operation }
