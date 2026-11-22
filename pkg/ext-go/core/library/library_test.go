package library

import (
	"os"
	"path/filepath"
	"testing"
)

// The guest scripts/cshared-purego.sh builds: any C shared library exporting
// ext_plugin_abi_version, which returns 1.
func guest(t *testing.T) string {
	path := os.Getenv("EXT_CSHARED_GUEST")
	if path == "" {
		if os.Getenv("EXT_CSHARED_REQUIRED") == "1" {
			t.Fatal("EXT_CSHARED_GUEST is required")
		}
		t.Skip("EXT_CSHARED_GUEST is not set")
	}
	return path
}

func TestBindAndCall(t *testing.T) {
	if !Supported() {
		t.Skip("unsupported here")
	}
	lib, err := Open(guest(t))
	if err != nil {
		t.Fatal(err)
	}
	var version func() uint32
	if err := lib.Bind(&version, "ext_plugin_abi_version"); err != nil {
		t.Fatal(err)
	}
	if got := version(); got != 1 {
		t.Fatalf("ext_plugin_abi_version() = %d", got)
	}
	if _, err := lib.Symbol("ext_library_test_no_such_symbol"); err == nil {
		t.Fatal("a missing symbol was found")
	}
	var bad int
	if err := lib.Bind(&bad, "ext_plugin_abi_version"); err == nil {
		t.Fatal("bound a non-function")
	}
}

func TestOneLibraryPerCanonicalPath(t *testing.T) {
	if !Supported() {
		t.Skip("unsupported here")
	}
	path := guest(t)
	link := filepath.Join(t.TempDir(), "link"+filepath.Ext(path))
	if err := os.Symlink(path, link); err != nil {
		t.Skip("no symbolic links here")
	}
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(link)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("a symbolic link loaded the library twice")
	}
}

func TestOpenFailures(t *testing.T) {
	if !Supported() {
		if _, err := Open("x"); err != ErrUnsupported {
			t.Fatalf("Open on an unsupported build: %v", err)
		}
		return
	}
	if _, err := Open(filepath.Join(t.TempDir(), "absent.so")); err == nil {
		t.Fatal("opened an absent library")
	}
	notLibrary := filepath.Join(t.TempDir(), "plain.so")
	if err := os.WriteFile(notLibrary, []byte("not a library"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(notLibrary); err == nil {
		t.Fatal("opened a file that is not a library")
	}
}
