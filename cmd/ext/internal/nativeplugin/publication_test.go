package nativeplugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/webong/ext/pkg/ext-go/engine/ledger"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestGraphPublicationOwnership(t *testing.T) {
	if os.Getenv("EXT_PLUGIN_LIBRARY") == "" || os.Getenv("EXT_LEDGER_LIBRARY") == "" {
		t.Skip("requires native plugin and ledger libraries")
	}
	lib, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	source, installed, path := t.TempDir(), t.TempDir(), t.TempDir()
	binary := filepath.Join(source, "guest")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, "../../../../tests/crosslang/testdata/graph-guest")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	ref := plugin.ContractRef{Name: "example.graph", Version: "v1"}
	m, err := packagekit.ExecutableManifest("fixture", "1.0.0", []plugin.Contract{{ContractRef: ref, Operations: []plugin.Operation{{Name: "read"}}}}, binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(source, m); err != nil {
		t.Fatal(err)
	}
	s, err := lib.Open("store", map[string]any{"root": installed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Call("install", map[string]any{"source": source}, nil); err != nil {
		t.Fatal(err)
	}
	start := func() *Graph {
		g, err := OpenGraph(context.Background(), lib, s, GraphOptions{RootID: "fixture", Contract: ref, OS: runtime.GOOS, Arch: runtime.GOARCH, LedgerPath: path, Review: func(m packagekit.Manifest) (string, error) {
			var digest string
			err := lib.Library().Call("store_digest", map[string]any{"root": installed, "id": m.Name()}, &digest)
			return digest, err
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = g.Close() })
		return g
	}
	first, second := start(), start()
	if first.publication.namespace == second.publication.namespace {
		t.Fatal("owners reused namespace")
	}
	snap, err := first.publication.store.Snapshot(context.Background(), first.publication.namespace)
	if err != nil || len(snap.Vertices) < 4 {
		t.Fatalf("no execution facts: %+v %v", snap, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := ledgerLibrary()
	if err != nil {
		t.Fatal(err)
	}
	db, err := l.OpenStore(context.Background(), "file", path, ledger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snap, err = db.Snapshot(context.Background(), first.publication.namespace)
	if err != nil || len(snap.Vertices) != 1 || len(snap.Edges) != 0 || snap.Vertices[0].Attributes["state"] != "closed" {
		t.Fatalf("shutdown: %+v %v", snap, err)
	}
	snap, err = db.Snapshot(context.Background(), second.publication.namespace)
	if err != nil || len(snap.Vertices) < 4 {
		t.Fatalf("other owner erased: %+v %v", snap, err)
	}
	// A stale owner must not overwrite a revision another writer changed.
	_, err = db.Apply(context.Background(), ledger.Transaction{Namespace: second.publication.namespace, Vertices: []ledger.Vertex{{ID: "external", Kind: second.publication.namespace + "/owner"}}})
	if err != nil {
		t.Fatal(err)
	}
	if code := ledger.Code(second.Close()); code != "changed" {
		t.Fatalf("stale cleanup: %s", code)
	}
	third := start()
	if third.publication.namespace == second.publication.namespace {
		t.Fatal("restart reused ownership")
	}
	if _, err := db.GetVertex(context.Background(), second.publication.namespace, "external"); err != nil {
		t.Fatal("restart erased abandoned observations", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}
