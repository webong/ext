package systemgraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ctx/pkg/graph"
)

func TestSchemaRejectsUnknownCTXKindsAndInvalidRelationshipMeaning(t *testing.T) {
	g, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	_, err = g.Store.Apply(context.Background(), graph.Transaction{Namespace: Namespace, Vertices: []graph.Vertex{{ID: "bad", Kind: Namespace + "/permission"}}})
	if err == nil {
		t.Fatal("unknown CTX system kind accepted")
	}
	_, err = g.Store.Apply(context.Background(), graph.Transaction{Namespace: Namespace, Vertices: []graph.Vertex{{ID: "machine", Kind: Namespace + "/machine"}, {ID: "project", Kind: Namespace + "/project"}}, Edges: []graph.Edge{{ID: "wrong", From: "machine", To: "project", Type: Namespace + "/hosts"}}})
	if err == nil || errors.Is(err, graph.ErrDanglingEdge) {
		t.Fatalf("invalid CTX semantic relationship not rejected by schema: %v", err)
	}
}

func TestObserveShellAndWebContextWithoutEnvironmentSecrets(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ctx"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "sensitive-path-value")
	t.Setenv("CTX_PROFILE", "profile-demo")
	g, err := Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err = g.ObserveShell(context.Background(), project, "profile-demo", map[string]string{"browser": "chrome:Default"}); err != nil {
		t.Fatal(err)
	}
	initial, err := g.Store.Snapshot(context.Background(), Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.ObserveShell(context.Background(), project, "profile-demo", map[string]string{"browser": "chrome:Default"}); err != nil {
		t.Fatal(err)
	}
	repeated, err := g.Store.Snapshot(context.Background(), Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Cursor != initial.Cursor {
		t.Fatalf("unchanged shell observation wrote a new graph revision: %d -> %d", initial.Cursor, repeated.Cursor)
	}
	if err = g.ObserveWeb(context.Background(), "browser", "profile-demo", "https", "example.com"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := g.Store.Snapshot(context.Background(), Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Vertices) < 5 {
		t.Fatalf("expected machine, shell, directory, project, and browser facts; got %d", len(snapshot.Vertices))
	}
	for _, vertex := range snapshot.Vertices {
		for _, value := range vertex.Attributes {
			if value == "sensitive-path-value" {
				t.Fatal("environment value leaked into graph")
			}
		}
	}
	if _, err = g.Store.GetEdge(context.Background(), Namespace, "shell-directory/"+itoa(os.Getppid())); err != nil {
		t.Fatalf("shell context relationship missing: %v", err)
	}
}

func TestSafeOriginDropsURLPathQueryAndCredentials(t *testing.T) {
	scheme, host, ok := SafeOrigin("https://user:password@example.com/private/path?token=secret#fragment")
	if !ok || scheme != "https" || host != "example.com" {
		t.Fatalf("unsafe or invalid origin result: %q %q %v", scheme, host, ok)
	}
}
