package crosslang

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"github.com/webong/ext/pkg/ext-go/engine/ledger/lib"
)

// cLedger is lib/ledger's library interface: EXT_LEDGER_LIBRARY, or a shared library CMake builds.
func cLedger(t *testing.T) lib.Interface {
	t.Helper()
	if !library.Supported() {
		t.Skip("this build cannot load a C library")
	}
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		if _, err := exec.LookPath("cmake"); err != nil {
			t.Skip("set EXT_LEDGER_LIBRARY or install cmake")
		}
		source, _ := filepath.Abs("../../lib/ledger")
		build := t.TempDir()
		for _, args := range [][]string{
			{"-S", source, "-B", build, "-DCMAKE_BUILD_TYPE=Release", "-DEXT_LEDGER_SHARED=ON"},
			{"--build", build, "-j", "8", "--target", "ext_ledger_shared"},
		} {
			if out, err := exec.Command("cmake", args...).CombinedOutput(); err != nil {
				t.Fatalf("cmake %v: %v\n%s", args, err, out)
			}
		}
		name := map[string]string{"darwin": "libext_ledger.dylib", "windows": "ext_ledger.dll"}[runtime.GOOS]
		if name == "" {
			name = "libext_ledger.so"
		}
		path = filepath.Join(build, name)
	}
	loaded, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	i, err := library.BindInterface(loaded, "ext_ledger", lib.ABIVersion)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// normal makes two answers comparable: canonical JSON with every time ("..._at") blanked.
func normal(t *testing.T, data []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("answer %q: %v", data, err)
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if strings.HasSuffix(k, "_at") {
					x[k] = "T"
				} else {
					x[k] = walk(val)
				}
			}
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		}
		return v
	}
	out, _ := json.Marshal(walk(v)) // encoding/json sorts map keys
	return string(out)
}

type step struct {
	handle string // "" is the library (0); otherwise a name an earlier open gave
	open   string // the config, when the step opens
	name   string // the name an open gives its handle
	op     string
	input  string
	close  bool
}

// TestTheLedgerInterfaceAgrees runs one script through the pure-Go ledger and the C one and holds
// every answer and every error code to be the same.
func TestTheLedgerInterfaceAgrees(t *testing.T) {
	script := []step{
		{open: `{"object":"store","backend":"memory"}`, name: "s"},
		{handle: "s", op: "register", input: `{"schema":{"namespace":"demo","version":"1","kinds":["demo/node"],"relationships":[{"type":"demo/link","from":"demo/node","to":"demo/node"}]}}`},
		{open: `{"object":"watch","store":S,"namespace":"demo","buffer":8}`, name: "w"},
		{handle: "w", op: "next", input: `{}`},
		{handle: "s", op: "apply", input: `{"transaction":{"namespace":"demo","vertices":[{"id":"a","kind":"demo/node","labels":{"z":"1","a":"2"}},{"id":"b","kind":"demo/node"}]}}`},
		{handle: "s", op: "apply", input: `{"transaction":{"namespace":"demo","edges":[{"id":"ab","type":"demo/link","from":"a","to":"b"}]}}`},
		{handle: "s", op: "apply", input: `{"transaction":{"namespace":"demo","vertices":[{"id":"c","kind":"other/kind"}]}}`},
		{handle: "s", op: "get_vertex", input: `{"namespace":"demo","id":"a"}`},
		{handle: "s", op: "get_edge", input: `{"namespace":"demo","id":"ab"}`},
		{handle: "s", op: "get_vertex", input: `{"namespace":"demo","id":"missing"}`},
		{handle: "s", op: "query_vertices", input: `{"query":{"namespace":"demo","limit":10}}`},
		{handle: "s", op: "query_vertices", input: `{"query":{"namespace":"demo","limit":0}}`},
		{handle: "s", op: "query_edges", input: `{"query":{"namespace":"demo","from":"a","limit":10}}`},
		{handle: "s", op: "traverse", input: `{"namespace":"demo","start":"a","max_depth":2,"max_vertices":10}`},
		{handle: "s", op: "snapshot", input: `{"namespace":"demo"}`},
		{handle: "s", op: "changes", input: `{"namespace":"demo","after":0,"limit":10}`},
		{handle: "s", op: "changes", input: `{"namespace":"demo","after":0,"limit":0}`},
		{handle: "w", op: "next", input: `{"timeout_ms":1000}`},
		{handle: "w", op: "next", input: `{"timeout_ms":1000}`},
		{handle: "s", op: "nothing", input: `{}`},
		{handle: "s", op: "apply", input: `{}`},
		{op: "canonical_transaction", input: `{"transaction":{"vertices":[{"kind":"demo/node","id":"x"}],"namespace":"demo"}}`},
		{op: "code_name", input: `{"code":4}`},
		{handle: "s", close: true},
		{handle: "w", close: true},
		{handle: "s", close: true},
		{handle: "s", op: "snapshot", input: `{"namespace":"demo"}`},
	}
	run := func(i lib.Interface) []string {
		handles := map[string]uint64{}
		var answers []string
		for _, st := range script {
			switch {
			case st.open != "":
				config := strings.ReplaceAll(st.open, "S", jsonNumber(handles["s"]))
				h, err := i.Open([]byte(config))
				handles[st.name] = h
				answers = append(answers, "open "+st.name+": "+lib.Code(err))
			case st.close:
				answers = append(answers, "close "+st.handle+": "+lib.Code(i.Close(handles[st.handle])))
			default:
				out, err := i.Call(handles[st.handle], st.op, []byte(st.input))
				if err != nil {
					answers = append(answers, st.op+": error "+lib.Code(err))
				} else {
					answers = append(answers, st.op+": "+normal(t, out))
				}
			}
		}
		return answers
	}
	goAnswers, cAnswers := run(lib.New()), run(cLedger(t))
	for k := range script {
		if goAnswers[k] != cAnswers[k] {
			t.Errorf("step %d differs\n  go: %s\n  c:  %s", k, goAnswers[k], cAnswers[k])
		}
	}
	if t.Failed() {
		return
	}
	// The script must reach real answers and real refusals, or agreeing proves nothing.
	all := strings.Join(goAnswers, "\n")
	for _, want := range []string{`"from":"a","id":"ab"`, "get_vertex: error not_found", "query_vertices: error invalid", "nothing: error unsupported",
		`next: {"committed_at":"T","cursor":2`, "close s: busy", "close s: \n", "snapshot: error not_found", "open w: \n"} {
		if !strings.Contains(all+"\n", want) {
			t.Errorf("the script never answered %q:\n%s", want, all)
		}
	}
	t.Logf("%d steps agree", len(script))
}

func jsonNumber(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
