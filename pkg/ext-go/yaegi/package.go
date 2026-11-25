package yaegi

import (
	"context"
	"fmt"
	goruntime "runtime"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// PackageOptions prepares verified source loading for a caller-selected host.
// entrypoint names it; empty selects the first entrypoint whose runtime is
// Runtime. The entrypoint's artifact must be a tree.
//
// Like engine/plugin/host.Open, it re-verifies the installed package before anything
// is evaluated: session.Verify runs after that check, and the source is then
// read once and held to the artifact's digest. session.Connect is replaced.
// The caller still owns package trust and operation authorization
// (session.Authorize).
func PackageOptions(installed store.Installed, entrypoint string, options Options, session plugin.Options) (plugin.Options, error) {
	artifact, err := selectSource(installed.Manifest, entrypoint)
	if err != nil {
		return plugin.Options{}, err
	}
	verify := session.Verify
	session.Verify = func(ctx context.Context, d plugin.Descriptor) error {
		if err := installed.Verify(); err != nil {
			return err
		}
		if verify != nil {
			return verify(ctx, d)
		}
		return nil
	}
	session.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		dir, err := installed.ArtifactPath(artifact.Name)
		if err != nil {
			return nil, err
		}
		return Open(ctx, dir, artifact.SHA256, options)
	}
	return session, nil
}

func selectSource(m packagekit.Manifest, name string) (packagekit.Artifact, error) {
	if name == "" {
		for _, e := range m.Entrypoints {
			if e.Runtime == Runtime {
				name = e.Name
				break
			}
		}
		if name == "" {
			return packagekit.Artifact{}, fmt.Errorf("%w: no %s entrypoint", plugin.ErrNotFound, Runtime)
		}
	}
	selected, err := m.SelectEntrypoint(name, packagekit.Environment{
		OS: goruntime.GOOS, Arch: goruntime.GOARCH,
		Runtimes: map[string]plugin.BackendProfile{Runtime: Profile()},
	})
	if err != nil {
		return packagekit.Artifact{}, err
	}
	if selected.Artifact.Kind != packagekit.ArtifactTree {
		return packagekit.Artifact{}, fmt.Errorf("%w: a %s entrypoint names a tree artifact", plugin.ErrInvalid, Runtime)
	}
	return selected.Artifact, nil
}
