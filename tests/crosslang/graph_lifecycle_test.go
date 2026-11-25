package crosslang_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest"
)

func graphLibrary(t *testing.T, env, prefix string) *library.Described {
	t.Helper()
	path := os.Getenv(env)
	if path == "" {
		t.Skip("requires " + env)
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	api, err := library.BindInterface(native, prefix, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, err := library.Describe(api)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func graphCall(t *testing.T, obj *library.Object, op string, input any, output any) {
	t.Helper()
	if err := obj.Call(op, input, output); err != nil {
		t.Fatalf("%s: %v", op, err)
	}
}
func graphCode(t *testing.T, want string, err error) {
	t.Helper()
	var code *library.CodeError
	if !errors.As(err, &code) || code.Name != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func TestExtGraphReconciliation(t *testing.T) {
	ledger := graphLibrary(t, "EXT_LEDGER_LIBRARY", "ext_ledger")
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			path := t.TempDir()
			db, err := ledger.Open("store", map[string]any{"backend": backend, "path": path}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, ns := range []string{"owned", "other"} {
				graphCall(t, db, "register", map[string]any{"schema": map[string]any{"namespace": ns, "version": "1"}}, nil)
				graphCall(t, db, "apply", map[string]any{"transaction": map[string]any{"namespace": ns, "vertices": []any{map[string]any{"id": "old", "kind": ns + "/node"}, map[string]any{"id": "keep", "kind": ns + "/node"}}, "edges": []any{map[string]any{"id": "old-route", "type": ns + "/route", "from": "old", "to": "keep"}}}}, nil)
			}
			snapshot := func() map[string]any {
				var s map[string]any
				graphCall(t, db, "snapshot", map[string]any{"namespace": "owned"}, &s)
				return s
			}
			before := snapshot()
			tx := map[string]any{"namespace": "owned", "expected_revision": before["revision"], "vertices": []any{map[string]any{"id": "new", "kind": "owned/node"}}, "edges": []any{}}
			graphCall(t, db, "reconcile", map[string]any{"transaction": tx}, nil)
			after := snapshot()
			if len(after["vertices"].([]any)) != 1 || len(after["edges"].([]any)) != 0 {
				t.Fatalf("stale records remain: %v", after)
			}
			graphCode(t, "not_found", db.Call("get_vertex", map[string]any{"namespace": "owned", "id": "old"}, nil))
			graphCall(t, db, "get_edge", map[string]any{"namespace": "other", "id": "old-route"}, nil)
			graphCode(t, "changed", db.Call("reconcile", map[string]any{"transaction": tx}, nil))
			if !reflect.DeepEqual(after, snapshot()) {
				t.Fatal("stale observation mutated namespace")
			}
			if backend == "file" {
				writer, err := ledger.Open("store", map[string]any{"backend": "file", "path": path}, nil)
				if err != nil {
					t.Fatal(err)
				}
				graphCall(t, writer, "apply", map[string]any{"transaction": map[string]any{"namespace": "owned", "vertices": []any{map[string]any{"id": "concurrent", "kind": "owned/node"}}}}, nil)
				writer.Close()
				tx["expected_revision"] = after["revision"]
				graphCode(t, "changed", db.Call("reconcile", map[string]any{"transaction": tx}, nil))
				graphCall(t, db, "get_vertex", map[string]any{"namespace": "owned", "id": "concurrent"}, nil)
				after = snapshot()
			}
			tx["expected_revision"] = after["revision"]
			tx["edges"] = []any{map[string]any{"id": "bad", "type": "owned/route", "from": "new", "to": "missing"}}
			if err := db.Call("reconcile", map[string]any{"transaction": tx}, nil); err == nil {
				t.Fatal("dangling desired edge accepted")
			}
			if !reflect.DeepEqual(after, snapshot()) {
				t.Fatal("invalid observation mutated namespace")
			}
			delete(tx, "edges")
			graphCode(t, "invalid", db.Call("reconcile", map[string]any{"transaction": tx}, nil))
			tx["edges"] = []any{}
			delete(tx, "expected_revision")
			graphCode(t, "invalid", db.Call("reconcile", map[string]any{"transaction": tx}, nil))
			tx["expected_revision"] = after["revision"]
			tx["vertices"] = []any{}
			graphCall(t, db, "reconcile", map[string]any{"transaction": tx}, nil)
			empty := snapshot()
			if len(empty["vertices"].([]any)) != 0 || len(empty["edges"].([]any)) != 0 {
				t.Fatal("explicit empty did not clear")
			}
			if backend == "file" {
				db.Close()
				reopened, err := ledger.Open("store", map[string]any{"backend": backend, "path": path}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				graphCode(t, "not_found", reopened.Call("get_vertex", map[string]any{"namespace": "owned", "id": "new"}, nil))
				graphCall(t, reopened, "get_edge", map[string]any{"namespace": "other", "id": "old-route"}, nil)
			}
		})
	}
}

func TestExtGraphLiveProcess(t *testing.T) {
	plugin, ledger := graphLibrary(t, "EXT_PLUGIN_LIBRARY", "ext_plugin"), graphLibrary(t, "EXT_LEDGER_LIBRARY", "ext_ledger")
	source, scratch := t.TempDir(), t.TempDir()
	binary := filepath.Join(source, "guest")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, "./testdata/graph-guest")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, out)
	}
	descriptor := plugintest.Descriptor()
	artifact, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	var rows, reviews []any
	for _, id := range []string{descriptor.Identity.ID, "example/provider"} {
		directory := t.TempDir()
		path := filepath.Join(directory, filepath.Base(binary))
		if err := os.WriteFile(path, artifact, 0700); err != nil {
			t.Fatal(err)
		}
		m, err := packagekit.ExecutableManifest(id, "", descriptor.Contracts, path)
		if err != nil {
			t.Fatal(err)
		}
		m.Descriptor = descriptor
		m.Descriptor.Identity.ID = id
		if id == descriptor.Identity.ID {
			m.Descriptor.Reverse = descriptor.Contracts
			m.Requires = []packagekit.Dependency{{Contract: descriptor.Contracts[0].ContractRef, Operation: "echo", Provider: "example/provider"}}
		}
		if err := store.WriteManifest(directory, m); err != nil {
			t.Fatal(err)
		}
		var digest string
		graphCall(t, plugin.Library(), "directory_digest", map[string]any{"root": directory}, &digest)
		rows = append(rows, map[string]any{"manifest": m, "directory": directory})
		reviews = append(reviews, map[string]any{"identity": m.Descriptor.Identity, "digest": digest})
	}
	open := func() *library.Object {
		g, err := plugin.Open("execution_graph", map[string]any{"packages": rows, "root": map[string]any{"identity": descriptor.Identity, "contract": descriptor.Contracts[0].ContractRef}, "os": runtime.GOOS, "arch": runtime.GOARCH, "temp_root": scratch}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { g.Close() })
		graphCode(t, "closed", g.Call("observe", map[string]any{}, nil))
		graphCall(t, g, "start", map[string]any{"reviews": reviews}, nil)
		return g
	}
	g := open()
	var sessions []map[string]any
	graphCall(t, g, "observe", map[string]any{}, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions: %v", sessions)
	}
	// The recorder and the public identity operation must agree exactly.
	db, err := ledger.Open("store", map[string]any{"backend": "memory"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rec, err := ledger.Open("recorder", map[string]any{"store": db.Handle(), "machine": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	var refs []any
	for _, session := range sessions {
		var observed map[string]any
		graphCall(t, ledger.Library(), "collect", map[string]any{"what": "process", "pid": session["pid"]}, &observed)
		processes, ok := observed["processes"].([]any)
		if !ok || len(processes) != 1 {
			t.Fatalf("no live child: %v", observed)
		}
		process := processes[0].(map[string]any)
		var ref map[string]any
		graphCall(t, ledger.Library(), "process_identity", map[string]any{"host_id": observed["host_id"], "pid": process["pid"], "start_id": process["start_id"]}, &ref)
		graphCall(t, rec, "record", map[string]any{"operation": "observe_processes", "input": observed}, nil)
		graphCall(t, db, "get_vertex", map[string]any{"namespace": "ext.system", "id": ref["id"]}, nil)
		ref["session"] = session["session"]
		refs = append(refs, ref)
	}
	ref := refs[0].(map[string]any)
	var projection struct {
		Schema      json.RawMessage
		Transaction map[string]any
	}
	input := map[string]any{"namespace": "run.graph", "processes": refs}
	graphCall(t, g, "snapshot", input, &projection)
	graphCall(t, db, "register", map[string]any{"schema": projection.Schema}, nil)
	projection.Transaction["expected_revision"] = 0
	graphCall(t, db, "reconcile", map[string]any{"transaction": projection.Transaction}, nil)
	var routes []map[string]any
	graphCall(t, db, "query_edges", map[string]any{"query": map[string]any{"namespace": "run.graph", "type": "run.graph/routes-to", "limit": 10}}, &routes)
	if len(routes) != 1 || routes[0]["from"] != sessions[0]["session"] || routes[0]["to"] != sessions[1]["session"] {
		t.Fatalf("live routes: %v", routes)
	}

	var stored map[string]any
	graphCall(t, db, "get_vertex", map[string]any{"namespace": "run.graph", "id": ref["id"]}, &stored)
	if stored["attributes"].(map[string]any)["reference"].(map[string]any)["start_id"] != ref["start_id"] {
		t.Fatal("lost birth token")
	}
	pid := ref["pid"]
	ref["pid"] = pid.(float64) + 1
	graphCode(t, "mismatch", g.Call("snapshot", input, nil))
	ref["pid"] = pid
	g2 := open()
	var next []map[string]any
	graphCall(t, g2, "observe", map[string]any{}, &next)
	if next[0]["session"] == sessions[0]["session"] {
		t.Fatal("session reused across executions")
	}
	graphCode(t, "mismatch", g2.Call("snapshot", input, nil))
	g2.Close()
	g.Close()
	graphCode(t, "not_found", g.Call("snapshot", input, nil))
	// Explicit owner reconciliation after termination removes session facts while
	// preserving the recorder's historical process identity in ext.system.
	var snap map[string]any
	graphCall(t, db, "snapshot", map[string]any{"namespace": "run.graph"}, &snap)
	graphCall(t, db, "reconcile", map[string]any{"transaction": map[string]any{"namespace": "run.graph", "expected_revision": snap["revision"], "vertices": []any{}, "edges": []any{}}}, nil)
	graphCode(t, "not_found", db.Call("get_vertex", map[string]any{"namespace": "run.graph", "id": sessions[0]["session"]}, nil))
	graphCall(t, db, "get_vertex", map[string]any{"namespace": "ext.system", "id": ref["id"]}, nil)
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("snapshot cleanup: %v %v", entries, err)
	}
}

func TestExtGraphProcessIdentity(t *testing.T) {
	ledger := graphLibrary(t, "EXT_LEDGER_LIBRARY", "ext_ledger")
	data, err := os.ReadFile("../../lib/ledger/testdata/recorder/v1/process-identity-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Status  string
			Input, Result map[string]any
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("empty process identity fixtures")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var result map[string]any
			err := ledger.Library().Call("process_identity", c.Input, &result)
			if c.Status != "ok" {
				graphCode(t, c.Status, err)
				return
			}
			if err != nil || !reflect.DeepEqual(result, c.Result) {
				t.Fatalf("identity mismatch: %v %v", result, err)
			}
		})
	}
}
