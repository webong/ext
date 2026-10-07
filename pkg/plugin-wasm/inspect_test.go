package wasm

import (
	"context"
	"reflect"
	"testing"
)

func leb(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func name(s string) []byte { return append(leb(len(s)), s...) }

// moduleWithImports builds a module that imports each (module, field) function
// with the type () -> ().
func moduleWithImports(imports [][2]string) []byte {
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	module = append(module, 1, 4, 1, 0x60, 0, 0) // type section: one type, () -> ()
	if len(imports) == 0 {
		return module
	}
	body := leb(len(imports))
	for _, entry := range imports {
		body = append(body, name(entry[0])...)
		body = append(body, name(entry[1])...)
		body = append(body, 0x00, 0x00) // function import of type 0
	}
	module = append(module, 2)
	module = append(module, leb(len(body))...)
	return append(module, body...)
}

func TestInspectClassifiesImports(t *testing.T) {
	cases := []struct {
		name        string
		imports     [][2]string
		target      Target
		unsupported []string
		web         bool
	}{
		{"pure computation", nil, TargetNone, nil, false},
		{"WASI only", [][2]string{{"wasi_snapshot_preview1", "fd_write"}, {"wasi_snapshot_preview1", "proc_exit"}}, TargetWASI, nil, false},
		{"wasm-bindgen glue", [][2]string{{"wbg", "__wbg_alert"}}, TargetHost, []string{"wbg"}, true},
		{"wasm-bindgen placeholder", [][2]string{{"__wbindgen_placeholder__", "__wbindgen_throw"}}, TargetHost, []string{"__wbindgen_placeholder__"}, true},
		{"Go js/wasm", [][2]string{{"gojs", "runtime.wasmExit"}}, TargetHost, []string{"gojs"}, true},
		{"Emscripten web runtime", [][2]string{{"env", "emscripten_resize_heap"}}, TargetHost, []string{"env"}, true},
		{"unknown host functions", [][2]string{{"env", "host_log"}}, TargetHost, []string{"env"}, false},
		{"WASI plus a host import", [][2]string{{"wasi_snapshot_preview1", "fd_write"}, {"env", "host_log"}}, TargetHost, []string{"env"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Inspect(context.Background(), moduleWithImports(c.imports))
			if err != nil {
				t.Fatal(err)
			}
			if got.Target != c.target || got.Web != c.web || (len(got.Unsupported)+len(c.unsupported) > 0 && !reflect.DeepEqual(got.Unsupported, c.unsupported)) {
				t.Fatalf("got %+v, want target=%s unsupported=%v web=%v", got, c.target, c.unsupported, c.web)
			}
		})
	}
}

func TestInspectRejectsInvalidModules(t *testing.T) {
	for _, module := range [][]byte{nil, []byte("not wasm")} {
		if _, err := Inspect(context.Background(), module); err == nil {
			t.Fatalf("%q was accepted", module)
		}
	}
}

func TestInspectRecognizesComponents(t *testing.T) {
	component := []byte{0, 'a', 's', 'm', 0x0d, 0, 0x01, 0}
	got, err := Inspect(context.Background(), component)
	if err != nil || got.Target != TargetComponent || len(got.Imports) != 0 {
		t.Fatalf("component: %+v %v", got, err)
	}
	// A core module has version 1, layer 0.
	core := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	if got, err := Inspect(context.Background(), core); err != nil || got.Target != TargetNone {
		t.Fatalf("core module: %+v %v", got, err)
	}
	// RunCommand runs core modules only, and says so instead of failing obscurely.
	if _, err := RunCommand(context.Background(), component, CommandOptions{}); err == nil {
		t.Fatal("a component must not run on the core engine")
	}
}
