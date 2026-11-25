package crosslang_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"github.com/webong/ext/tests/crosslang/internal/pluginbind"
)

func TestReviewedPackageSnapshot(t *testing.T) {
	lib := pureGo(t)
	source, root := t.TempDir(), t.TempDir()
	binary := filepath.Join(source, "guest")
	if err := os.WriteFile(binary, []byte("reviewed executable"), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := packagekit.ExecutableManifest("example/snapshot", "1.0.0", []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}, binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(source, m); err != nil {
		t.Fatal(err)
	}
	digest, err := lib.DirectoryDigest(pluginbind.LibraryDirectoryDigestInput{Root: source})
	if err != nil {
		t.Fatal(err)
	}
	target, err := lib.OpenStore(pluginbind.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	input := pluginbind.StoreSnapshotInput{ID: m.Name(), Source: source, ReviewedDigest: digest}
	expectCode := func(want string, err error) {
		t.Helper()
		var code *library.CodeError
		if !errors.As(err, &code) || code.Name != want {
			t.Fatalf("want %s, got %v", want, err)
		}
	}
	bad := input
	bad.ReviewedDigest = strings.Repeat("0", 64)
	_, err = target.Snapshot(bad)
	expectCode("denied", err)
	ids, err := target.IDs(nil)
	if err != nil || len(ids) != 0 {
		t.Fatalf("rejected copy published: %v %v", ids, err)
	}
	if _, err := target.Snapshot(input); err != nil {
		t.Fatal(err)
	}
	_, err = target.Snapshot(input)
	expectCode("exists", err)
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	got, err := lib.StoreDigest(pluginbind.LibraryStoreDigestInput{Root: root, ID: m.Name()})
	if err != nil || got != digest {
		t.Fatalf("snapshot changed after source removal: %s %v", got, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "example%2fsnapshot", "guest")); err != nil || string(data) != "reviewed executable" {
		t.Fatalf("copied artifact: %s %v", data, err)
	}
	for _, name := range []string{".package-trust.json", ".receipts"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("snapshot created %s: %v", name, err)
		}
	}
}
