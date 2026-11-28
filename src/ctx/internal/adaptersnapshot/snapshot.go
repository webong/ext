// Package adaptersnapshot owns CTX's per-invocation adapter copies.
package adaptersnapshot

import (
	"context"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cgoengine/plugin/host/commandscope"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"os"
	"path/filepath"
	"reflect"
	"runtime"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"github.com/webong/ext/pkg/ext-go/core/dirlock"
	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
)

// Prepare reviews and snapshots the complete dependency closure under the writer.
// The returned broker owns those copies until every nested dispatch has joined.
func Prepare(ctx context.Context, owner *adapter.Store, candidate *adapter.Adapter) (*adapter.Adapter, *commandscope.Scope, error) {
	fail := func(err error) (*adapter.Adapter, *commandscope.Scope, error) { return nil, nil, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if owner == nil || candidate == nil {
		return fail(fmt.Errorf("adapter snapshot requires store and selection"))
	}
	release, err := dirlock.Acquire(filepath.Join(filepath.Dir(owner.Home), ".package-write.lock"))
	if err != nil {
		return fail(err)
	}
	defer release()
	current, err := owner.Load(candidate.Manifest.Name)
	if err != nil {
		return fail(err)
	}
	if !reflect.DeepEqual(current.Package, candidate.Package) {
		return fail(fmt.Errorf("adapter changed after selection"))
	}
	path := os.Getenv("EXT_PLUGIN_LIBRARY")
	var native *library.Library
	if path != "" {
		native, err = library.Open(path)
	} else if _, ok := library.Provided("ext_plugin"); ok {
		native, err = library.OpenProvided("ext_plugin", "")
	} else {
		return fail(fmt.Errorf("adapter snapshots require the ext host's EXT_PLUGIN_LIBRARY or a provided ext_plugin library"))
	}
	if err != nil {
		return fail(err)
	}
	api, err := library.BindInterface(native, "ext_plugin", 1)
	if err != nil {
		return fail(err)
	}
	lib, err := library.Describe(api)
	if err != nil {
		return fail(err)
	}

	packages, err := store.NewWithLayout(owner.Home, store.Limits{}, storelayout.Ext())
	if err != nil {
		return fail(err)
	}
	installed, problems, err := packages.ListUnverified()
	if err != nil {
		return fail(err)
	}
	// An explicitly configured legacy adapter home keeps its trust file beside
	// packages. Only that exact regular file is excluded from the package inventory.
	for _, problem := range problems {
		info, statErr := os.Lstat(problem.Directory)
		if filepath.Clean(problem.Directory) != filepath.Clean(owner.TrustFile) || statErr != nil || !info.Mode().IsRegular() {
			return fail(problem.Err)
		}
	}
	for i, p := range installed {
		verified, err := packages.Load(p.Manifest.Name())
		if err != nil {
			return fail(err)
		}
		installed[i] = verified
	}
	manifests := make([]packagekit.Manifest, len(installed))
	for i, p := range installed {
		manifests[i] = p.Manifest
	}
	projected, err := adapter.ProjectDependencies(manifests, runtime.GOOS)
	if err != nil {
		return fail(err)
	}
	rows := make([]commandscope.Row, len(installed))
	for i, p := range installed {
		rows[i] = commandscope.Row{Manifest: projected[i], Directory: p.Directory}
	}
	scope, err := commandscope.Open(ctx, lib, commandscope.Options{Root: current.Package.Descriptor.Identity, Rows: rows, Layout: packages.Layout(), Review: func(m packagekit.Manifest) (string, error) {
		a, err := owner.Load(m.Name())
		if err != nil {
			return "", err
		}
		if err := owner.AssertTrusted(a); err != nil {
			return "", err
		}
		return a.Checksum()
	}, Dispatch: dispatch})
	if err != nil {
		return fail(err)
	}
	pinned, err := scope.Load(current.Package.Descriptor.Identity)
	if err != nil {
		_ = scope.Close()
		return fail(err)
	}
	copied, err := adapter.LoadDirectory(pinned.Directory)
	if err != nil {
		_ = scope.Close()
		return fail(err)
	}
	return copied, scope, nil
}
