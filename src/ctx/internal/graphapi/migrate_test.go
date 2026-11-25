package graphapi

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"database/sql"
	model "github.com/webong/ext/pkg/ext-go/cengine/ledger"
)

func requireNative(t *testing.T) {
	t.Helper()
	if os.Getenv("EXT_LEDGER_LIBRARY") == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
}
func TestLegacyGraphMigration(t *testing.T) {
	requireNative(t)
	home := t.TempDir()
	path := filepath.Join(home, "graph.db")
	ctx := context.Background()
	fixture, err := os.ReadFile("testdata/legacy-graph.sql")
	if err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(ctx, string(fixture)); err != nil {
		old.Close()
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureStore(ctx, lib, home); err != nil {
		t.Fatal(err)
	}
	graph, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Store.GetVertex(ctx, Namespace, "saved-machine"); err != nil {
		t.Fatal(err)
	}
	graph.Close()
	s, err := lib.OpenStore(ctx, "file", filepath.Join(home, Directory), model.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetVertex(ctx, "external", "saved")
	if err != nil || got.Attributes["value"] != "preserved" {
		t.Fatalf("migration: %+v %v", got, err)
	}
	if _, err := s.GetEdge(ctx, "external", "edge"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy database changed")
	}
	if _, err := s.Apply(ctx, model.Transaction{Namespace: "external", DeleteEdges: []model.EdgeRef{{ID: "edge"}}, DeleteVertices: []model.VertexRef{{ID: "saved"}}}); err != nil {
		t.Fatal(err)
	}
	if err := ensureStore(ctx, lib, home); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetVertex(ctx, "external", "saved"); err == nil {
		t.Fatal("legacy data reimported after cutover")
	}
}
func TestMigrationFailurePreservesLegacy(t *testing.T) {
	requireNative(t)
	home := t.TempDir()
	path := filepath.Join(home, "graph.db")
	data := []byte("not a ledger database")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(home); err == nil {
		t.Fatal("invalid legacy database accepted")
	}
	if _, err := os.Stat(filepath.Join(home, Directory)); !os.IsNotExist(err) {
		t.Fatal("failed migration published a destination")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, data) {
		t.Fatal("failed migration changed legacy data")
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".ledger-migration*"))
	if len(matches) != 0 {
		t.Fatalf("migration leftovers: %v", matches)
	}
}
