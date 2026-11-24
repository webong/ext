package library_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

var pluginBuild struct {
	once sync.Once
	dir  string
	err  error
}

// pluginEngine is a build of lib/plugin with its shared host library and test guests: the directory
// EXT_PLUGIN_BUILD names, or one built here with CMake.
func pluginEngine(t *testing.T) string {
	t.Helper()
	if !library.Supported() {
		t.Skip("this build cannot load a C library")
	}
	if dir := os.Getenv("EXT_PLUGIN_BUILD"); dir != "" {
		return dir
	}
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("set EXT_PLUGIN_BUILD or install cmake")
	}
	pluginBuild.once.Do(func() {
		source, err := filepath.Abs("../../../../lib/plugin")
		if err != nil {
			pluginBuild.err = err
			return
		}
		build, err := os.MkdirTemp("", "ext-plugin-build")
		if err != nil {
			pluginBuild.err = err
			return
		}
		for _, args := range [][]string{
			{"-S", source, "-B", build, "-DCMAKE_BUILD_TYPE=Release", "-DEXT_BUILD_SHARED=ON"},
			{"--build", build, "-j", "8", "--target", "ext_plugin_host_shared", "ext_shared_test_guest", "ext_reverse_test"},
		} {
			if out, err := exec.Command("cmake", args...).CombinedOutput(); err != nil {
				pluginBuild.err = errors.New(string(out))
				return
			}
		}
		pluginBuild.dir = build
	})
	if pluginBuild.err != nil {
		t.Fatalf("build lib/plugin: %v", pluginBuild.err)
	}
	return pluginBuild.dir
}

func pluginFile(t *testing.T, dir string, names ...string) string {
	t.Helper()
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return filepath.Join(dir, n)
		}
	}
	t.Fatalf("none of %v in %s", names, dir)
	return ""
}

func describedPlugin(t *testing.T) (*library.Described, string) {
	t.Helper()
	dir := pluginEngine(t)
	name := map[string][]string{"darwin": {"libext_plugin_host.dylib"}, "windows": {"ext_plugin_host.dll", "libext_plugin_host.dll"}}[runtime.GOOS]
	if name == nil {
		name = []string{"libext_plugin_host.so"}
	}
	lib, err := library.Open(pluginFile(t, dir, name...))
	if err != nil {
		t.Fatal(err)
	}
	ifc, err := library.BindInterface(lib, "ext_plugin", 1)
	if err != nil {
		t.Fatal(err)
	}
	d, err := library.Describe(ifc)
	if err != nil {
		t.Fatal(err)
	}
	return d, dir
}

// TestEveryDescribedOperationIsAnswered holds lib/plugin's description and its engine to each other:
// each operation the description lists, called with an empty object, gets an answer or a code other
// than unsupported, and an operation it does not list never reaches the library.
func TestEveryDescribedOperationIsAnswered(t *testing.T) {
	d, _ := describedPlugin(t)
	if d.Description.ABIVersion != 1 || len(d.Description.Codes) == 0 {
		t.Fatalf("description: %+v", d.Description)
	}
	for i, name := range d.Description.Codes {
		if got, err := library.Call[string](d.Library(), "code_name", map[string]int{"code": i}); err != nil || got != name {
			t.Errorf("code %d is %q in the description and %q (%v) in the library", i, name, got, err)
		}
	}
	root := t.TempDir()
	objects := map[string]any{
		"store":   map[string]any{"root": root},
		"layered": map[string]any{"roots": []string{root}},
	}
	for kind, about := range d.Description.Objects {
		var o *library.Object
		switch kind {
		case "library":
			o = d.Library()
		case "host":
			continue // a host's operations run a plugin: TestADescribedHost
		default:
			var err error
			if o, err = d.Open(kind, objects[kind], nil); err != nil {
				t.Fatalf("open %s: %v", kind, err)
			}
		}
		for op := range about.Operations {
			err := o.Call(op, map[string]any{}, nil)
			var code *library.CodeError
			if errors.As(err, &code) && code.Name == "unsupported" {
				t.Errorf("%s %s is described and unsupported", kind, op)
			}
		}
		if err := o.Call("no_such_operation", nil, nil); !errors.Is(err, library.ErrNotDescribed) {
			t.Errorf("%s: an operation the description does not list: %v", kind, err)
		}
		if err := o.Close(); err != nil {
			t.Errorf("close %s: %v", kind, err)
		}
	}
	if _, err := d.Open("nothing", nil, nil); !errors.Is(err, library.ErrNotDescribed) {
		t.Errorf("an object the description does not list: %v", err)
	}
}

const conformance = `{"apiVersion":"ext.plugin/v1","identity":{"id":"ctx/conformance","revision":"fixture-1"},` +
	`"contracts":[{"name":"ext.conformance","version":"v1","operations":[{"name":"echo"},{"name":"wait"},` +
	`{"name":"private-error"},{"name":"public-error"}]}]}`

const reverseDescriptor = `{"apiVersion":"ext.plugin/v1","identity":{"id":"reverse/guest","revision":"r1"},` +
	`"contracts":[{"name":"reverse.test","version":"v1","operations":[{"name":"run","progress":true}]}],` +
	`"reverse":[{"name":"host.svc","version":"v1","operations":[{"name":"sign","surface":"signing"},{"name":"secret"},` +
	`{"name":"empty"},{"name":"fail"},{"name":"twice"},{"name":"none"},{"name":"errok"},{"name":"errcode"},{"name":"errmsg"},` +
	`{"name":"errretry"},{"name":"errretryok"},{"name":"badjson"},{"name":"big"},{"name":"slow"},{"name":"slowerr"},` +
	`{"name":"gate"},{"name":"hang"}]}]}`

// TestADescribedHost drives lib/plugin's host from its description alone: policies, progress tagged to
// its call and reverse calls are events answered by handlers named for their kinds.
func TestADescribedHost(t *testing.T) {
	d, dir := describedPlugin(t)
	guest := pluginFile(t, dir, "libext_shared_test_guest.so", "libext_shared_test_guest.dylib", "ext_shared_test_guest.dll", "libext_shared_test_guest.dll")
	var verified, authorized atomic.Int32
	deny := atomic.Bool{}
	host, err := d.Open("host", map[string]any{
		"descriptor": json.RawMessage(conformance), "backend": map[string]any{"kind": "shared_library", "path": guest},
		"verify": "event", "authorize": "event",
	}, library.Handlers{
		"verify": func(json.RawMessage) (any, error) { verified.Add(1); return nil, nil },
		"authorize": func(json.RawMessage) (any, error) {
			authorized.Add(1)
			if deny.Load() {
				return nil, errors.New("no")
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Call("start", map[string]any{"timeout_ms": 5000}, nil); err != nil {
		t.Fatal(err)
	}
	echo := map[string]any{"call": map[string]any{"contract": map[string]string{"name": "ext.conformance", "version": "v1"}, "operation": "echo", "payload": 7}, "timeout_ms": 5000}
	response, err := library.Call[map[string]json.RawMessage](host, "call", echo)
	if err != nil || !strings.Contains(string(response["payload"]), `"echoed":true`) {
		t.Errorf("echo: %v, %v", response, err)
	}
	deny.Store(true)
	var code *library.CodeError
	if err := host.Call("call", echo, nil); !errors.As(err, &code) || code.Name != "denied" {
		t.Errorf("a call the authorize handler refuses: %v", err)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if verified.Load() != 1 || authorized.Load() != 2 {
		t.Errorf("verify %d, authorize %d", verified.Load(), authorized.Load())
	}

	// A process guest that reports progress and calls its host back while a call runs.
	var reverseAuthorized, reversed atomic.Int32
	host, err = d.Open("host", map[string]any{
		"descriptor": json.RawMessage(reverseDescriptor),
		"backend":    map[string]any{"kind": "process", "executable": pluginFile(t, dir, "ext_reverse_test", "ext_reverse_test.exe"), "arguments": []string{"--guest", "interleave"}},
		"verify":     "allow", "authorize": "allow", "services": map[string]any{"timeout_ms": 5000},
	}, library.Handlers{
		"reverse_authorize": func(json.RawMessage) (any, error) { reverseAuthorized.Add(1); return nil, nil },
		"reverse":           func(json.RawMessage) (any, error) { reversed.Add(1); return map[string]bool{"signed": true}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err := host.Call("start", map[string]any{"timeout_ms": 5000}, nil); err != nil {
		t.Fatal(err)
	}
	run := map[string]any{"call": map[string]any{"contract": map[string]string{"name": "reverse.test", "version": "v1"}, "operation": "run", "payload": map[string]any{}}, "progress": map[string]any{}, "timeout_ms": 5000}
	var progress []json.RawMessage
	var out map[string]json.RawMessage
	err = host.CallWith("call", run, &out, library.Handlers{"progress": func(data json.RawMessage) (any, error) {
		progress = append(progress, data)
		return nil, nil
	}})
	if err != nil || !strings.Contains(string(out["payload"]), `"signed":true`) {
		t.Errorf("run: %s, %v", out["payload"], err)
	}
	if len(progress) != 2 || reverseAuthorized.Load() != 1 || reversed.Load() != 1 {
		t.Errorf("progress %d (before the call returned), reverse authorize %d, reverse %d", len(progress), reverseAuthorized.Load(), reversed.Load())
	}
	if err := host.CallWith("call", run, nil, library.Handlers{"verify": nil}); !errors.Is(err, library.ErrNotDescribed) {
		t.Errorf("a handler for an event the call does not raise: %v", err)
	}
	if _, err := d.Open("host", map[string]any{}, library.Handlers{"no_such_event": nil}); !errors.Is(err, library.ErrNotDescribed) {
		t.Errorf("a handler for an event the object does not raise: %v", err)
	}
}
