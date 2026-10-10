package chromium

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/extension"
)

func writeSigner(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSigningKeepsSourceAndKeySeparate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	browser := writeSigner(t, "browser", `
for arg in "$@"; do
  case "$arg" in --pack-extension=*) extension="${arg#*=}";; esac
done
printf 'Cr24fixture' > "${extension}.crx"
printf 'private key' > "${extension}.pem"
`)
	output := filepath.Join(t.TempDir(), "built.crx")
	// The fake signer's output is not a valid CRX3, so signing must refuse it
	// without publishing anything.
	if _, err := SignChromiumCRX(context.Background(), "example", browser, source, output, prepared.Revision, ""); err == nil {
		t.Fatal("accepted a packaged file that is not a signed CRX3")
	}
	for _, path := range []string{output, strings.TrimSuffix(output, ".crx") + ".pem"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("failed signing left %s behind: %v", path, err)
		}
	}
	if current, err := extension.Inspect(source); err != nil || current.Revision != prepared.Revision {
		t.Fatalf("source changed: %+v, %v", current, err)
	}
}

func TestSigningRejectsUnsafeInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	browser := writeSigner(t, "browser", "exit 0\n")
	dir := t.TempDir()
	cases := map[string]func() error{
		"relative executable": func() error {
			_, err := SignChromiumCRX(context.Background(), "example", "browser", source, filepath.Join(dir, "a.crx"), prepared.Revision, "")
			return err
		},
		"output inside source": func() error {
			_, err := SignChromiumCRX(context.Background(), "example", browser, source, filepath.Join(source, "inside.crx"), prepared.Revision, "")
			return err
		},
		"wrong revision": func() error {
			_, err := SignChromiumCRX(context.Background(), "example", browser, source, filepath.Join(dir, "b.crx"), "sha256:wrong", "")
			return err
		},
		"relative key path": func() error {
			_, err := SignChromiumCRX(context.Background(), "example", browser, source, filepath.Join(dir, "c.crx"), prepared.Revision, "key.pem")
			return err
		},
	}
	for name, run := range cases {
		if err := run(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	existing := filepath.Join(dir, "exists.crx")
	if err := os.WriteFile(existing, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SignChromiumCRX(context.Background(), "example", browser, source, existing, prepared.Revision, ""); err == nil {
		t.Fatal("overwrote an existing CRX")
	}
}
