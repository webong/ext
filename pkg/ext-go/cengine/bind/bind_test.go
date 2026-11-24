package bind_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/bind"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func pluginDescription(t *testing.T) library.Description {
	t.Helper()
	data, err := os.ReadFile("../../../../lib/plugin/interface.json")
	if err != nil {
		t.Skip("lib/plugin is not beside this module")
	}
	var d library.Description
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGenerateRefusesWhatItCannotBind(t *testing.T) {
	d := pluginDescription(t)
	if _, err := bind.Generate(d, bind.Options{Package: "Not-A-Name"}); err == nil {
		t.Error("a package name that is no identifier")
	}
	if _, err := bind.Generate(library.Description{Library: "ext_x", ABIVersion: 1}, bind.Options{Package: "x"}); err == nil {
		t.Error("a description without a library object")
	}
}

// The program the test runs inside the generated package: lib/plugin's library, a store and a host
// through the generated types alone.
const generatedUse = `package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func TestTheGeneratedBinding(t *testing.T) {
	dir := os.Getenv("EXT_PLUGIN_BUILD")
	if dir == "" || !library.Supported() {
		t.Skip("set EXT_PLUGIN_BUILD to a build of lib/plugin")
	}
	name := map[string]string{"darwin": "libext_plugin_host.dylib", "windows": "libext_plugin_host.dll"}[runtime.GOOS]
	if name == "" {
		name = "libext_plugin_host.so"
	}
	lib, err := library.Open(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	x, err := Bind(lib)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := x.ProtocolNegotiate(json.RawMessage(` + "`" + `{"preferred":["a","b"],"offered":["b"]}` + "`" + `)); err != nil || got != "b" {
		t.Errorf("protocol.negotiate: %q, %v", got, err)
	}
	if got, err := x.CodeName(LibraryCodeNameInput{Code: 10}); err != nil || got != "not_found" {
		t.Errorf("code_name: %q, %v", got, err)
	}
	store, err := x.OpenStore(StoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if ids, err := store.IDs(nil); err != nil || len(ids) != 0 {
		t.Errorf("ids: %v, %v", ids, err)
	}
	if _, err := store.Load(StoreLoadInput{ID: "example/missing"}); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Errorf("load a missing package: %v", err)
	}
	guest := filepath.Join(dir, "ext_reverse_test")
	if runtime.GOOS == "windows" {
		guest += ".exe"
	}
	descriptor := json.RawMessage(` + "`" + `{"apiVersion":"ext.plugin/v1","identity":{"id":"reverse/guest","revision":"r1"},"contracts":[{"name":"reverse.test","version":"v1","operations":[{"name":"run","progress":true}]}],"reverse":[{"name":"host.svc","version":"v1","operations":[{"name":"sign","surface":"signing"},{"name":"secret"},{"name":"empty"},{"name":"fail"},{"name":"twice"},{"name":"none"},{"name":"errok"},{"name":"errcode"},{"name":"errmsg"},{"name":"errretry"},{"name":"errretryok"},{"name":"badjson"},{"name":"big"},{"name":"slow"},{"name":"slowerr"},{"name":"gate"},{"name":"hang"}]}]}` + "`" + `)
	reversed := 0
	host, err := x.OpenHost(HostConfig{
		Descriptor: descriptor, Verify: "allow", Authorize: "allow",
		Backend:  HostConfigBackend{Kind: "process", Executable: guest, Arguments: []string{"--guest", "interleave"}},
		Services: &HostConfigServices{TimeoutMS: 5000},
	}, HostHandlers{
		ReverseAuthorize: func(json.RawMessage) error { return nil },
		Reverse:          func(json.RawMessage) (any, error) { reversed++; return map[string]bool{"signed": true}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err := host.Start(HostStartInput{TimeoutMS: 5000}); err != nil {
		t.Fatal(err)
	}
	progress := 0
	out, err := host.CallWith(HostCallInput{
		Call:      json.RawMessage(` + "`" + `{"contract":{"name":"reverse.test","version":"v1"},"operation":"run","payload":{}}` + "`" + `),
		Progress:  &HostCallInputProgress{},
		TimeoutMS: 5000,
	}, HostCallHandlers{Progress: func(json.RawMessage) { progress++ }})
	if err != nil || !strings.Contains(string(out), ` + "`" + `"signed":true` + "`" + `) || progress != 2 || reversed != 1 {
		t.Errorf("run: %s, %v; progress %d, reverse %d", out, err, progress, reversed)
	}
}
`

// TestTheGeneratedBindingOfLibPlugin generates lib/plugin's binding into a package of this module,
// compiles it and runs a program through it, against a build of lib/plugin when EXT_PLUGIN_BUILD
// names one.
func TestTheGeneratedBindingOfLibPlugin(t *testing.T) {
	d := pluginDescription(t)
	src, err := bind.Generate(d, bind.Options{Package: "plugin", Source: "lib/plugin/interface.json"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"func Bind(lib *library.Library) (*Library, error)", "func (x *Library) OpenHost(config HostConfig, handlers HostHandlers) (*Host, error)",
		"func (x *Host) CallWith(in HostCallInput, handlers HostCallHandlers) (json.RawMessage, error)", "func (x *Store) Remove(in StoreRemoveInput) error"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("the binding lacks %s", want)
		}
	}
	// A directory inside this module, so the binding compiles with the module's requirements.
	dir, err := os.MkdirTemp(".", "generated")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, body := range map[string][]byte{"plugin.go": src, "use_test.go": []byte(generatedUse)} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-v", "-count=1", "./"+filepath.Base(dir))
	cmd.Env = append(os.Environ(), "EXT_PLUGIN_BUILD="+os.Getenv("EXT_PLUGIN_BUILD"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the generated binding: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

const generatedLedgerUse = `package ledger

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func TestTheGeneratedBinding(t *testing.T) {
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" || !library.Supported() {
		t.Skip("set EXT_LEDGER_LIBRARY to a built lib/ledger shared library")
	}
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	x, err := Bind(lib)
	if err != nil {
		t.Fatal(err)
	}
	store, err := x.OpenStore(StoreConfig{Backend: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Register(StoreRegisterInput{Schema: json.RawMessage(` + "`" + `{"namespace":"demo","version":"1"}` + "`" + `)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(StoreApplyInput{Transaction: json.RawMessage(` + "`" + `{"namespace":"demo","vertices":[{"id":"a","kind":"demo/node"}]}` + "`" + `)}); err != nil {
		t.Fatal(err)
	}
	if v, err := store.GetVertex(StoreGetVertexInput{Namespace: "demo", ID: "a"}); err != nil || !strings.Contains(string(v), "demo/node") {
		t.Errorf("get_vertex: %s, %v", v, err)
	}
	watch, err := x.OpenWatch(WatchConfig{Store: int64(store.Handle()), Namespace: "demo", Buffer: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Next(WatchNextInput{TimeoutMS: 1000}); err != nil {
		t.Errorf("next: %v", err)
	}
	if err := watch.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := x.CanonicalTransaction(LibraryCanonicalTransactionInput{Transaction: json.RawMessage(` + "`" + `{"namespace":"demo"}` + "`" + `)}); err != nil || len(c.Fingerprint) != 64 {
		t.Errorf("canonical_transaction: %+v, %v", c, err)
	}
}
`

// TestTheGeneratedBindingOfLibLedger is TestTheGeneratedBindingOfLibPlugin for lib/ledger, against
// the library EXT_LEDGER_LIBRARY names.
func TestTheGeneratedBindingOfLibLedger(t *testing.T) {
	data, err := os.ReadFile("../../../../lib/ledger/interface.json")
	if err != nil {
		t.Skip("lib/ledger is not beside this module")
	}
	var d library.Description
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	src, err := bind.Generate(d, bind.Options{Package: "ledger", Source: "lib/ledger/interface.json"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(".", "generated")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, body := range map[string][]byte{"ledger.go": src, "use_test.go": []byte(generatedLedgerUse)} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command("go", "test", "-v", "-count=1", "./"+filepath.Base(dir)).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated binding: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
