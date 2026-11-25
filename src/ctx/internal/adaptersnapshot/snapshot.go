// Package adaptersnapshot owns CTX's per-invocation adapter copies.
package adaptersnapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/res/system/service/host/dirlock"
)

// Prepare owns only this invocation's package. Nested dependency calls
// continue through the product dispatcher and perform their own admission.
func Prepare(ctx context.Context, owner *adapter.Store, candidate *adapter.Adapter) (*adapter.Adapter, func(), error) {
	fail := func(err error) (*adapter.Adapter, func(), error) { return nil, nil, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if owner == nil || candidate == nil {
		return fail(fmt.Errorf("adapter snapshot requires a store and selection"))
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
		return fail(fmt.Errorf("adapter %s changed after selection", candidate.Manifest.Name))
	}
	if err := owner.AssertTrusted(current); err != nil {
		return fail(err)
	}
	digest, err := current.Checksum()
	if err != nil {
		return fail(err)
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
	root, err := os.MkdirTemp("", "ext-adapter-snapshot-")
	if err != nil {
		return fail(err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	target, err := lib.Open("store", map[string]any{"root": root}, nil)
	if err != nil {
		cleanup()
		return fail(err)
	}
	defer target.Close()
	if err := target.Call("snapshot", map[string]any{"source": current.Directory, "id": current.Package.Name(), "reviewed_digest": digest}, nil); err != nil {
		cleanup()
		return fail(err)
	}
	packages, err := store.New(root, store.Limits{})
	if err != nil {
		cleanup()
		return fail(err)
	}
	installed, err := packages.Load(current.Package.Name())
	if err != nil {
		cleanup()
		return fail(err)
	}
	copied, err := adapter.LoadDirectory(installed.Directory)
	if err != nil {
		cleanup()
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return fail(err)
	}
	return copied, cleanup, nil
}
