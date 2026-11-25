package pkgcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/cmd/ext/internal/packagelock"
	"github.com/webong/ext/cmd/ext/internal/registry"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
)

// snapshotCommand isolates the executable package for one command invocation.
// Dependency admission remains explicit; this does not redirect nested package
// resolution to a snapshot graph or copy approval into an environment variable.
func snapshotCommand(installed store.Installed, declared Declared, options host.Options, s *store.Store, trust *Trust) registry.Command {
	command := Command(installed, declared, options)
	command.Run = func(args []string, stdout, stderr io.Writer) int {
		copied, cleanup, err := prepareCommandSnapshot(installed, options, s, trust)
		if err != nil {
			fmt.Fprintf(stderr, "ext: %s: %v\n", declared.Name, err)
			return 1
		}
		defer cleanup()
		admitted := options
		admitted.Verify = nil // already evaluated under the writer, before copying
		return Command(copied, declared, admitted).Run(args, stdout, stderr)
	}
	return command
}

func prepareCommandSnapshot(installed store.Installed, options host.Options, s *store.Store, trust *Trust) (store.Installed, func(), error) {
	fail := func(err error) (store.Installed, func(), error) { return store.Installed{}, nil, err }
	// Explicit launch overrides can point outside this package; silently keeping
	// one would defeat execution isolation.
	if options.Launch != nil {
		return fail(fmt.Errorf("package command snapshots require the package entrypoint"))
	}
	release, err := packagelock.Acquire(filepath.Dir(s.Root()))
	if err != nil {
		return fail(err)
	}
	defer release()
	current, err := s.Load(installed.Name())
	if err != nil {
		return fail(err)
	}
	if !reflect.DeepEqual(current.Manifest, installed.Manifest) {
		return fail(fmt.Errorf("package %s changed after command discovery", installed.Name()))
	}
	installed = current
	if err := installed.Verify(); err != nil {
		return fail(err)
	}
	approved, err := trust.trustedIn(installed, scope)
	if err != nil {
		return fail(err)
	}
	if !approved {
		return fail(fmt.Errorf("package %s is not approved", installed.Name()))
	}
	if options.Verify != nil {
		if err := options.Verify(context.Background()); err != nil {
			return fail(err)
		}
	}
	digest, err := installed.Digest()
	if err != nil {
		return fail(err)
	}
	lib, err := nativeplugin.Open()
	if err != nil {
		return fail(err)
	}
	root, err := os.MkdirTemp("", "ext-command-snapshot-")
	if err != nil {
		return fail(err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	destination, err := lib.Open("store", map[string]any{"root": root}, nil)
	if err != nil {
		cleanup()
		return fail(err)
	}
	defer destination.Close()
	if err := destination.Call("snapshot", map[string]any{"source": installed.Directory, "id": installed.Name(), "reviewed_digest": digest}, nil); err != nil {
		cleanup()
		return fail(err)
	}
	snapshot, err := store.New(root, store.Limits{})
	if err != nil {
		cleanup()
		return fail(err)
	}
	copied, err := snapshot.Load(installed.Name())
	if err != nil {
		cleanup()
		return fail(err)
	}
	return copied, cleanup, nil
}
