package library_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

var ledger struct {
	once sync.Once
	path string
	err  error
}

// ledgerLibrary is lib/ledger's shared library: EXT_LEDGER_LIBRARY, or one built with CMake.
func ledgerLibrary(t *testing.T) string {
	t.Helper()
	if !library.Supported() {
		t.Skip("this build cannot load a C library")
	}
	if p := os.Getenv("EXT_LEDGER_LIBRARY"); p != "" {
		return p
	}
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("set EXT_LEDGER_LIBRARY or install cmake")
	}
	ledger.once.Do(func() {
		source, err := filepath.Abs("../../../../lib/ledger")
		if err != nil {
			ledger.err = err
			return
		}
		if _, err := os.Stat(filepath.Join(source, "CMakeLists.txt")); err != nil {
			ledger.err = errors.New("lib/ledger is not beside this module")
			return
		}
		build, err := os.MkdirTemp("", "ext-ledger-build")
		if err != nil {
			ledger.err = err
			return
		}
		for _, args := range [][]string{
			{"-S", source, "-B", build, "-DCMAKE_BUILD_TYPE=Release", "-DEXT_LEDGER_SHARED=ON"},
			{"--build", build, "-j", "8", "--target", "ext_ledger_shared"},
		} {
			if out, err := exec.Command("cmake", args...).CombinedOutput(); err != nil {
				ledger.err = errors.New(string(out))
				return
			}
		}
		name := map[string]string{"darwin": "libext_ledger.dylib", "windows": "ext_ledger.dll"}[runtime.GOOS]
		if name == "" {
			name = "libext_ledger.so"
		}
		ledger.path = filepath.Join(build, name)
	})
	if ledger.err != nil {
		t.Fatalf("build lib/ledger: %v", ledger.err)
	}
	return ledger.path
}

func TestTheLedgerInterface(t *testing.T) {
	lib, err := library.Open(ledgerLibrary(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.BindInterface(lib, "ext_ledger", 99); err == nil || !strings.Contains(err.Error(), "ABI version") {
		t.Errorf("a wrong ABI version: %v", err)
	}
	if _, err := library.BindInterface(lib, "ext_nothing", 1); err == nil {
		t.Error("a prefix the library does not export bound")
	}
	l, err := library.BindInterface(lib, "ext_ledger", 1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := l.Open([]byte(`{"object":"store","backend":"memory"}`))
	if err != nil {
		t.Fatal(err)
	}
	call := func(h uint64, op, in string) string {
		t.Helper()
		out, err := l.Call(h, op, []byte(in))
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return string(out)
	}
	if got := call(store, "register", `{"schema":{"namespace":"demo","version":"1"}}`); got != "null" {
		t.Errorf("register answered %s", got)
	}
	call(store, "apply", `{"transaction":{"namespace":"demo","vertices":[{"id":"a","kind":"demo/node"}]}}`)
	if got := call(store, "get_vertex", `{"namespace":"demo","id":"a"}`); !strings.Contains(got, `"demo/node"`) {
		t.Errorf("get_vertex answered %s", got)
	}
	if got := call(0, "code_name", `{"code":2}`); got != `"not_found"` {
		t.Errorf("code_name answered %s", got)
	}
	_, err = l.Call(store, "get_vertex", []byte(`{"namespace":"demo","id":"missing"}`))
	var code *library.CodeError
	if !errors.As(err, &code) || code.Name != "not_found" || code.Op != "get_vertex" {
		t.Errorf("a missing vertex: %v", err)
	}
	if err := l.Close(store); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(store); !errors.As(err, &code) || code.Name != "not_found" {
		t.Errorf("closing twice: %v", err)
	}
	if _, err := l.Call(0, "a\x00b", nil); err == nil {
		t.Error("an operation name with NUL")
	}
}
