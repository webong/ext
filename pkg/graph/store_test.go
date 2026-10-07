package graph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

const testNS = "test.example"

func testStore(t *testing.T) Store {
	t.Helper()
	s := NewMemory()
	if err := s.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDurableHistoryRetentionRequiresResnapshot(t *testing.T) {
	path := t.TempDir() + "/graph.json"
	store, err := OpenFileWithOptions(path, Options{MaxChanges: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err := store.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{vertex(fmt.Sprintf("node-%d", i), "node")}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenFileWithOptions(path, Options{MaxChanges: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Snapshot(context.Background(), testNS)
	if err != nil || snapshot.Cursor != 3 || snapshot.HistoryFloor != 1 || len(snapshot.Vertices) != 3 {
		t.Fatalf("snapshot after compaction: %+v, %v", snapshot, err)
	}
	if _, err := store.Changes(context.Background(), testNS, 0, 2); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired change cursor: %v", err)
	}
	if _, err := store.Watch(context.Background(), testNS, 0, 2); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired watch cursor: %v", err)
	}
	changes, err := store.Changes(context.Background(), testNS, 1, 2)
	if err != nil || len(changes) != 2 || changes[0].Cursor != 2 || changes[1].Cursor != 3 {
		t.Fatalf("retained history: %+v, %v", changes, err)
	}
}

func TestHistoryByteLimitRetainsLatestCommit(t *testing.T) {
	store := NewMemoryWithOptions(Options{MaxChanges: 10, MaxChangeBytes: 1})
	defer store.Close()
	if err := store.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{vertex(fmt.Sprintf("node-%d", i), "node")}}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := store.Snapshot(context.Background(), testNS)
	if err != nil || snapshot.HistoryFloor != 2 {
		t.Fatalf("byte-limited history floor: %+v, %v", snapshot, err)
	}
	changes, err := store.Changes(context.Background(), testNS, 2, 10)
	if err != nil || len(changes) != 1 || changes[0].Cursor != 3 {
		t.Fatalf("latest change unavailable: %+v, %v", changes, err)
	}
}
func vertex(id, kind string) Vertex { return Vertex{ID: id, Kind: testNS + "/" + kind} }
func edge(id, from, to, rel string) Edge {
	return Edge{ID: id, From: from, To: to, Type: testNS + "/" + rel}
}

func TestApplyIsAtomicAndChecksReferentialIntegrity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	commit, err := s.Apply(ctx, Transaction{Namespace: testNS, Vertices: []Vertex{vertex("a", "node"), vertex("b", "node")}, Edges: []Edge{edge("ab", "a", "b", "link")}})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Revision != 1 {
		t.Fatalf("revision=%d", commit.Revision)
	}
	if _, err = s.GetEdge(ctx, testNS, "ab"); err != nil {
		t.Fatalf("atomic edge absent: %v", err)
	}
	_, err = s.Apply(ctx, Transaction{Namespace: testNS, Vertices: []Vertex{vertex("c", "node")}, Edges: []Edge{edge("bad", "c", "missing", "link")}})
	if !errors.Is(err, ErrDanglingEdge) {
		t.Fatalf("expected dangling edge, got %v", err)
	}
	if _, err = s.GetVertex(ctx, testNS, "c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial vertex committed: %v", err)
	}
}

func TestIdempotencyAndOptimisticRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	expected := uint64(0)
	tx := Transaction{Namespace: testNS, ExpectedRevision: &expected, IdempotencyKey: "request-1", Vertices: []Vertex{vertex("a", "node")}}
	first, err := s.Apply(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Apply(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.Cursor != first.Cursor || replay.Revision != first.Revision {
		t.Fatalf("bad replay: %+v / %+v", first, replay)
	}
	different := tx
	different.Vertices = []Vertex{vertex("b", "node")}
	if _, err = s.Apply(ctx, different); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("expected idempotency error, got %v", err)
	}
	_, err = s.Apply(ctx, Transaction{Namespace: testNS, ExpectedRevision: &expected, Vertices: []Vertex{vertex("c", "node")}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}

func TestNamespaceIsolationAndConsumerValidation(t *testing.T) {
	s := NewMemory()
	ctx := context.Background()
	allowed := true
	validator := func(view View, tx Transaction) error {
		for _, e := range tx.Edges {
			if !allowed && e.Type == "one.example/forbidden" {
				return errors.New("consumer constraint")
			}
			if _, ok := view.Vertex(tx.Namespace, e.From); !ok {
				return ErrDanglingEdge
			}
		}
		return nil
	}
	for _, ns := range []string{"one.example", "two.example"} {
		if err := s.Register(Schema{Namespace: ns, Version: "1", Validate: validator}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Apply(ctx, Transaction{Namespace: "one.example", Vertices: []Vertex{{ID: "a", Kind: "one.example/node"}, {ID: "b", Kind: "one.example/node"}}, Edges: []Edge{{ID: "ab", From: "a", To: "b", Type: "one.example/allowed"}}})
	if err != nil {
		t.Fatal(err)
	}
	allowed = false
	_, err = s.Apply(ctx, Transaction{Namespace: "one.example", Edges: []Edge{{ID: "reject", From: "a", To: "b", Type: "one.example/forbidden"}}})
	if err == nil {
		t.Fatal("consumer validator accepted rejected relationship")
	}
	if _, err = s.GetVertex(ctx, "two.example", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("namespace leaked vertex: %v", err)
	}
}

func TestBoundedQueriesAndTraversal(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, err := s.Apply(ctx, Transaction{Namespace: testNS, Vertices: []Vertex{vertex("a", "node"), vertex("b", "node"), vertex("c", "node"), vertex("d", "node")}, Edges: []Edge{edge("ab", "a", "b", "link"), edge("bc", "b", "c", "link"), edge("cd", "c", "d", "link")}})
	if err != nil {
		t.Fatal(err)
	}
	verts, err := s.QueryVertices(ctx, VertexQuery{Namespace: testNS, Limit: 2})
	if err != nil || len(verts) != 2 {
		t.Fatalf("bounded vertex query: %d, %v", len(verts), err)
	}
	edges, err := s.QueryEdges(ctx, EdgeQuery{Namespace: testNS, From: "b", Limit: 1})
	if err != nil || len(edges) != 1 || edges[0].ID != "bc" {
		t.Fatalf("relationship query: %+v, %v", edges, err)
	}
	paths, err := s.Traverse(ctx, testNS, "a", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || len(paths[1].Vertices) != 3 {
		t.Fatalf("depth bound failed: %+v", paths)
	}
}

func TestChangesUsesBoundedGlobalCursors(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.Apply(ctx, Transaction{Namespace: testNS, Vertices: []Vertex{vertex(id, "node")}}); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := s.Changes(ctx, testNS, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Cursor != 2 {
		t.Fatalf("unexpected bounded changes: %+v", changes)
	}
	snapshot, err := s.Snapshot(ctx, testNS)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Cursor != 3 {
		t.Fatalf("global cursor not included in snapshot: %d", snapshot.Cursor)
	}
}

func TestWatchCursorOrderAndDurableReopen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := testStore(t)
	watch, err := s.Watch(ctx, testNS, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a", "b"} {
		if _, err := s.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{vertex(id, "node")}}); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	for want := uint64(1); want <= 2; want++ {
		select {
		case ch := <-watch:
			if ch.Cursor != want {
				t.Fatalf("cursor order: got %d want %d", ch.Cursor, want)
			}
		case <-time.After(time.Second):
			t.Fatal("watch timed out")
		}
	}
	temp := t.TempDir() + "/graph.json"
	durable, err := OpenFile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if err = durable.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = durable.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{vertex("persisted", "node")}}); err != nil {
		t.Fatal(err)
	}
	if err = durable.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenFile(temp)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.GetVertex(context.Background(), testNS, "persisted"); err != nil {
		t.Fatalf("durable record missing after reopen: %v", err)
	}
}

func TestDurableStoreRequiresConsumerSchemaRegistrationPerProcess(t *testing.T) {
	path := t.TempDir() + "/graph.json"
	first, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{vertex("a", "node")}}); err == nil {
		t.Fatal("durable schema metadata bypassed consumer validator registration")
	}
}

func TestRejectSecretLikeGraphValues(t *testing.T) {
	s := testStore(t)
	_, err := s.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{{ID: "bad", Kind: testNS + "/node", Attributes: map[string]any{"password": "dont-store"}}}})
	if !errors.Is(err, ErrSecretValue) {
		t.Fatalf("expected secret value rejection, got %v", err)
	}
	_, err = s.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{{ID: "bad-label", Kind: testNS + "/node", Labels: map[string]string{"api_key": "secret-value:abc"}}}})
	if !errors.Is(err, ErrSecretValue) {
		t.Fatalf("expected secret label rejection, got %v", err)
	}
}

func TestFileStoresCoordinateAcrossIndependentHandles(t *testing.T) {
	path := t.TempDir() + "/shared/graph.json"
	a, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	b, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch, err := a.Watch(watchCtx, testNS, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			_, e := b.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{vertex(fmt.Sprintf("shared-%d", i), "node")}})
			errCh <- e
		}(i)
	}
	for i := 0; i < writers; i++ {
		if e := <-errCh; e != nil {
			t.Fatal(e)
		}
	}
	vertices, err := a.QueryVertices(context.Background(), VertexQuery{Namespace: testNS, Limit: 32})
	if err != nil {
		t.Fatal(err)
	}
	if len(vertices) != writers {
		t.Fatalf("lost concurrent writes: got %d, want %d", len(vertices), writers)
	}
	seen := uint64(0)
	deadline := time.After(2 * time.Second)
	for seen < uint64(writers) {
		select {
		case change, ok := <-watch:
			if !ok {
				t.Fatal("cross-process watch closed")
			}
			if change.Cursor != seen+1 {
				t.Fatalf("out-of-order cross-process cursor %d after %d", change.Cursor, seen)
			}
			seen = change.Cursor
		case <-deadline:
			t.Fatalf("cross-process watch delivered %d of %d changes", seen, writers)
		}
	}
}

func TestFileGraphSubprocessWriter(t *testing.T) {
	if os.Getenv("EXT_GRAPH_WRITER_HELPER") != "1" {
		return
	}
	store, err := OpenFile(os.Getenv("EXT_GRAPH_STORE"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Apply(context.Background(), Transaction{Namespace: testNS, Vertices: []Vertex{vertex(os.Getenv("EXT_GRAPH_VERTEX_ID"), "node")}}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreSharesWritesAndChangeCursorsAcrossProcesses(t *testing.T) {
	path := t.TempDir() + "/shared/graph.json"
	parent, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err = parent.Register(Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := parent.Snapshot(context.Background(), testNS)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch, err := parent.Watch(ctx, testNS, snapshot.Cursor, 8)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 4
	commands := make([]*exec.Cmd, writers)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestFileGraphSubprocessWriter$")
		commands[i].Env = append(os.Environ(), "EXT_GRAPH_WRITER_HELPER=1", "EXT_GRAPH_STORE="+path, fmt.Sprintf("EXT_GRAPH_VERTEX_ID=written-by-child-%d", i))
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("subprocess graph writer %d failed: %v", i, err)
		}
	}
	for i := 0; i < writers; i++ {
		select {
		case change, ok := <-watch:
			if !ok {
				t.Fatal("cross-process watcher closed")
			}
			if change.Cursor != snapshot.Cursor+uint64(i+1) {
				t.Fatalf("unexpected cross-process cursor %d", change.Cursor)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cross-process watcher did not observe all commits")
		}
	}
	vertices, err := parent.QueryVertices(context.Background(), VertexQuery{Namespace: testNS, Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(vertices) != writers {
		t.Fatalf("concurrent subprocess commits lost: got %d want %d", len(vertices), writers)
	}
	for i := 0; i < writers; i++ {
		if _, err = parent.GetVertex(context.Background(), testNS, fmt.Sprintf("written-by-child-%d", i)); err != nil {
			t.Fatalf("parent did not refresh child commit %d: %v", i, err)
		}
	}
}
