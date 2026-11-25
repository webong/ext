package pkgcmd

import (
	"context"
	"fmt"

	"github.com/webong/ext/cmd/ext/internal/registry"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
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
		ok, err := trust.trustedIn(p, scope)
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
		commandOptions := options
		if len(p.Manifest.Requires) > 0 {
			id := p.Name()
			commandOptions.Verify = func(ctx context.Context) error {
				if options.Verify != nil {
					if err := options.Verify(ctx); err != nil {
						return err
					}
				}
				return (Manager{Store: s, Trust: trust}).CheckInvocation(id)
			}
		}
		for _, d := range declared {
			if err := reg.Register(snapshotCommand(p, d, commandOptions, s, trust)); err != nil {
				reg.Note(fmt.Sprintf("package %s: %v", p.Name(), err))
			}
		}
	}
}

// DiscoverBundled registers verified packages installed beside the ext binary.
// This directory is part of the same release and trust boundary as that binary.
// User installed packages remain in EXT_HOME and require explicit trust.
func DiscoverBundled(reg *registry.Registry, root string) {
	s, err := store.New(root, store.Limits{})
	if err != nil {
		reg.Note(fmt.Sprintf("bundled packages: %v", err))
		return
	}
	installed, checks, err := s.ListUnverified()
	if err != nil {
		reg.Note(fmt.Sprintf("bundled packages: %v", err))
		return
	}
	for _, check := range checks {
		if !check.OK() {
			reg.Note(fmt.Sprintf("bundled package %s failed verification: %v", check.ID, check.Err))
		}
	}
	for _, p := range installed {
		if err := p.Verify(); err != nil {
			continue
		}
		declared, err := Declare(p)
		if err != nil {
			reg.Note(err.Error())
			continue
		}
		for _, d := range declared {
			if err := reg.Register(Command(p, d, host.Options{})); err != nil {
				reg.Note(fmt.Sprintf("bundled package %s: %v", p.Name(), err))
			}
		}
	}
}
