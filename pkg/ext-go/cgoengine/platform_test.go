//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// exeSuffix is ".exe" where a launch path must name the exact executable file.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// missingExecutable is an absolute path on this platform that names nothing.
func missingExecutable(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "missing"+exeSuffix())
}

// linkOrCopy makes path name the file at base: a symbolic link where the
// platform allows one, otherwise a copy.
func linkOrCopy(t *testing.T, base, path string) {
	t.Helper()
	if err := os.Symlink(base, path); err == nil {
		return
	}
	in, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
