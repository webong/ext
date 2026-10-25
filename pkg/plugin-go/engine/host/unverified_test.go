package host_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/host"
	"github.com/webong/ext/pkg/plugin-go/engine/host/process"
	"github.com/webong/ext/pkg/plugin-go/engine/host/store"
	"os"
	"path/filepath"
	"testing"
)

// A package taken from Store.ListUnverified has not been verified. The launch is
// where that is enforced: when the content changed after install, host.Open
// refuses it, with the default launch and with a launch override, and the
// refusal does not depend on the listing having been fresh.
func TestOpenFailsClosedOnUnverifiedListing(t *testing.T) {
	installed := installExecutable(t)
	s, err := store.New(filepath.Dir(installed.Directory), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	list := func() store.Installed {
		t.Helper()
		listed, problems, err := s.ListUnverified()
		if err != nil || len(problems) != 0 || len(listed) != 1 {
			t.Fatalf("%v %v %v", listed, problems, err)
		}
		return listed[0]
	}
	override := func(path string) host.Options {
		return host.Options{Launch: &process.Command{Path: path, Env: []string{"HOST_TEST_CHILD=1"}}}
	}
	exe := mustExecutable(t, installed)

	// intact content opens from a listed package
	before := list()
	p, err := host.Open(context.Background(), before, override(exe))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	// change the artifact after install
	artifact := filepath.Join(installed.Directory, filepath.FromSlash(installed.Manifest.Artifacts[0].Path))
	f, err := os.OpenFile(artifact, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("tampered"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// the listing still returns it, because it reads no content; List does not
	altered := list()
	if _, err := s.List(); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("List: %v", err)
	}
	for name, options := range map[string]host.Options{"default launch": {}, "launch override": override(exe)} {
		for which, listed := range map[string]store.Installed{"listed after the change": altered, "listed before the change": before} {
			if proc, err := host.Open(context.Background(), listed, options); err == nil || proc != nil || !errors.Is(err, plugin.ErrMismatch) {
				if proc != nil {
					proc.Close()
				}
				t.Fatalf("%s, %s: opened (%v)", name, which, err)
			}
		}
	}

	// A listed package carries the manifest read at listing time, and is verified
	// against that manifest. Rewriting plugin.json to match the new content does
	// not change what an earlier listing will accept; a new listing sees it.
	sum := sha256.Sum256([]byte(readFile(t, artifact)))
	rewritten := altered.Manifest
	rewritten.Artifacts = append(rewritten.Artifacts[:0:0], rewritten.Artifacts...)
	rewritten.Artifacts[0].SHA256 = hex.EncodeToString(sum[:])
	if err := store.WriteManifest(installed.Directory, rewritten); err != nil {
		t.Fatal(err)
	}
	if err := altered.Verify(); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("an earlier listing accepted a rewritten manifest: %v", err)
	}
	if proc, err := host.Open(context.Background(), altered, override(exe)); err == nil {
		proc.Close()
		t.Fatal("an earlier listing opened against a rewritten manifest")
	}
	if fresh := list(); fresh.Manifest.Artifacts[0].SHA256 != rewritten.Artifacts[0].SHA256 {
		t.Fatal("a new listing did not read the new manifest")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
