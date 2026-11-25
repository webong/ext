package library_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func TestDescribedPackageWriter(t *testing.T) {
	lib, build := describedPlugin(t)
	if home := os.Getenv("EXT_TEST_C_WRITER_HOME"); home != "" {
		writer, err := lib.Open("package_writer", map[string]any{"root": home}, nil)
		if os.Getenv("EXT_TEST_C_WRITER_MODE") == "abandon" {
			if err != nil {
				t.Fatal(err)
			}
			_ = writer
			os.Exit(0) // Simulate process loss: no Close, so the directory must remain.
		}
		var code *library.CodeError
		if !errors.As(err, &code) || code.Name != "exists" {
			t.Fatalf("child acquired writer or unexpected error: %v", err)
		}
		return
	}
	home := t.TempDir()
	path := filepath.Join(home, ".package-write.lock")
	child := func(mode string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestDescribedPackageWriter$")
		cmd.Env = append(os.Environ(), "EXT_PLUGIN_BUILD="+build, "EXT_TEST_C_WRITER_HOME="+home, "EXT_TEST_C_WRITER_MODE="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v %s", err, out)
		}
	}
	open := func() *library.Object {
		t.Helper()
		w, err := lib.Open("package_writer", map[string]any{"root": home}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	writer := open()
	child("contend")
	if err := os.Mkdir(path, 0700); !os.IsExist(err) {
		t.Fatalf("CLI-style lock did not contend: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	child("contend")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writer = open()
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	other := open()
	var code *library.CodeError
	if err := writer.Close(); !errors.As(err, &code) || code.Name != "mismatch" {
		t.Fatalf("replaced lock release: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed other owner's lock: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + "-old"); err != nil {
		t.Fatal(err)
	}
	child("abandon")
	child("contend")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writer = open()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}
