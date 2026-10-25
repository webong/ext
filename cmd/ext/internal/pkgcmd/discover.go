package pkgcmd

import (
	"fmt"

	"github.com/webong/ext/cmd/ext/internal/registry"
	"github.com/webong/ext/pkg/ext-go/engine/host"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

// Discover registers the commands of every installed, verified, trusted package as
// package-sourced entries. Anything it skips is reported as a note on the registry,
// never silently: an untrusted package, a package whose declaration is invalid, or a
// name that cannot be registered.
func Discover(reg *registry.Registry, s *store.Store, trust *Trust, options host.Options) {
	installed, checks, err := s.ListUnverified()
	if err != nil {
		reg.Note(fmt.Sprintf("packages: %v", err))
		return
	}
	for _, c := range checks {
		if !c.OK() {
			reg.Note(fmt.Sprintf("package %s failed verification: %v", c.ID, c.Err))
		}
	}
	for _, p := range installed {
		declared, err := Declare(p)
		if err != nil {
			reg.Note(err.Error())
			continue
		}
		if len(declared) == 0 {
			continue
		}
		ok, err := trust.Trusted(p)
		if err != nil {
			reg.Note(fmt.Sprintf("package %s: %v", p.Name(), err))
			continue
		}
		if !ok {
			reg.Note(fmt.Sprintf("package %s declares commands but is not trusted", p.Name()))
			continue
		}
		if p.Verify() != nil {
			continue // reported above from the store's own checks
		}
		for _, d := range declared {
			if err := reg.Register(Command(p, d, options)); err != nil {
				reg.Note(fmt.Sprintf("package %s: %v", p.Name(), err))
			}
		}
	}
}
