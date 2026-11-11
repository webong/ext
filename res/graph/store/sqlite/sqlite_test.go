package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/webong/ext/res/graph/store"
	"github.com/webong/ext/res/graph/store/sqlite"
	"github.com/webong/ext/res/graph/store/storetest"
)

const testNS = "test.example"

func backend() storetest.Backend {
	return storetest.Backend{Durable: true, Open: func(t testing.TB, dir string, o store.Options) store.Store {
		t.Helper()
		s, err := sqlite.Open(context.Background(), filepath.Join(dir, "graph.db"), o)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}}
}

func TestSQLiteStoreFixtures(t *testing.T) { storetest.Run(t, backend()) }

func TestSQLiteStoreBehaviour(t *testing.T) { storetest.RunBehaviour(t, backend()) }

func TestLayoutMatchesContract(t *testing.T) {
	dir, err := storetest.Dir()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := os.ReadFile(filepath.Join(dir, "tables.json"))
	if err != nil {
		t.Fatal(err)
	}
	copy, err := os.ReadFile("tables.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, copy) {
		t.Fatal("store/sqlite/tables.json differs from testdata/store/v1/tables.json; run go generate ./store/sqlite")
	}
}

// The database another SQLite reader sees has exactly the documented tables.
func TestDatabaseHasTheDocumentedTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	s, err := sqlite.Open(context.Background(), path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Register(ctx, store.Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, store.Transaction{Namespace: testNS, Vertices: []store.Vertex{{ID: "a", Kind: testNS + "/node", Attributes: map[string]any{"b": 1, "a": "x"}}}}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	info, err := os.Stat(path)
	// Windows has no POSIX mode bits; the file is created with 0600 everywhere else.
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("database mode %v, %v", info.Mode(), err)
	}
	var layout struct {
		Layout string `json:"layout"`
		Tables []struct {
			Name    string `json:"name"`
			Columns []struct {
				Name string `json:"name"`
			} `json:"columns"`
		} `json:"tables"`
		Indexes []struct {
			Name string `json:"name"`
		} `json:"indexes"`
	}
	storetest.Load(t, "tables.json", &layout)
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range layout.Tables {
		rows, err := db.Query(fmt.Sprintf(`SELECT name FROM pragma_table_info('%s') ORDER BY cid`, table.Name))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var name string
			_ = rows.Scan(&name)
			got = append(got, name)
		}
		rows.Close()
		if len(got) != len(table.Columns) {
			t.Fatalf("table %s has columns %v", table.Name, got)
		}
		for i, c := range table.Columns {
			if got[i] != c.Name {
				t.Fatalf("table %s column %d is %s, want %s", table.Name, i, got[i], c.Name)
			}
		}
	}
	for _, ix := range layout.Indexes {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, ix.Name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s: %d, %v", ix.Name, n, err)
		}
	}
	var stored, attrs string
	if err := db.QueryRow(`SELECT layout FROM store`).Scan(&stored); err != nil || stored != layout.Layout {
		t.Fatalf("store layout %q, %v", stored, err)
	}
	if err := db.QueryRow(`SELECT attributes FROM vertices WHERE id = 'a'`).Scan(&attrs); err != nil || attrs != `{"a":"x","b":1}` {
		t.Fatalf("attributes column %q, %v", attrs, err)
	}
}

func TestRefusesAnotherLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	s, err := sqlite.Open(context.Background(), path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE store SET layout = 'ext.graph.store/v0'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := sqlite.Open(context.Background(), path, store.Options{}); store.Code(err) != "unsupported" {
		t.Fatalf("open of another layout: %v", err)
	}
	other := filepath.Join(t.TempDir(), "other.db")
	db, _ = sql.Open("sqlite", other)
	if _, err := db.Exec(`CREATE TABLE unrelated (x)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := sqlite.Open(context.Background(), other, store.Options{}); store.Code(err) != "unsupported" {
		t.Fatalf("open of a foreign database: %v", err)
	}
}

func TestHandlesShareWritesAndWatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared", "graph.db")
	open := func() *sqlite.Store {
		s, err := sqlite.Open(context.Background(), path, store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Register(context.Background(), store.Schema{Namespace: testNS, Version: "1"}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	a, b := open(), open()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch, err := a.Watch(ctx, testNS, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			_, err := b.Apply(context.Background(), store.Transaction{Namespace: testNS, Vertices: []store.Vertex{{ID: fmt.Sprintf("shared-%d", i), Kind: testNS + "/node"}}})
			errs <- err
		}(i)
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	expectWatch(t, watch, 0, writers)
	vertices, err := a.QueryVertices(context.Background(), store.VertexQuery{Namespace: testNS, Limit: 32})
	if err != nil || len(vertices) != writers {
		t.Fatalf("vertices %d, %v", len(vertices), err)
	}
}

func expectWatch(t *testing.T, watch <-chan store.Change, from uint64, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for seen := from; seen < from+uint64(n); {
		select {
		case change, ok := <-watch:
			if !ok {
				t.Fatal("watch closed")
			}
			if change.Cursor != seen+1 {
				t.Fatalf("cursor %d after %d", change.Cursor, seen)
			}
			seen = change.Cursor
		case <-deadline:
			t.Fatalf("watch delivered %d of %d changes", seen-from, n)
		}
	}
}

func TestSubprocessWriter(t *testing.T) {
	if os.Getenv("EXT_GRAPH_WRITER_HELPER") != "1" {
		t.Skip("helper process")
	}
	s, err := sqlite.Open(context.Background(), os.Getenv("EXT_GRAPH_STORE"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Register(context.Background(), store.Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(context.Background(), store.Transaction{Namespace: testNS, Vertices: []store.Vertex{{ID: os.Getenv("EXT_GRAPH_VERTEX_ID"), Kind: testNS + "/node"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestProcessesShareWritesAndCursors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	parent, err := sqlite.Open(context.Background(), path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := parent.Register(context.Background(), store.Schema{Namespace: testNS, Version: "1"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watch, err := parent.Watch(ctx, testNS, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 4
	commands := make([]*exec.Cmd, writers)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestSubprocessWriter$")
		commands[i].Env = append(os.Environ(), "EXT_GRAPH_WRITER_HELPER=1", "EXT_GRAPH_STORE="+path, fmt.Sprintf("EXT_GRAPH_VERTEX_ID=child-%d", i))
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	expectWatch(t, watch, 0, writers)
	for i := 0; i < writers; i++ {
		if _, err := parent.GetVertex(context.Background(), testNS, fmt.Sprintf("child-%d", i)); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
	}
}
