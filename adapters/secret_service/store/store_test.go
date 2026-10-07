package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/res/credential/adapterkit"
)

func TestSecretToolCredentialRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock helper uses a POSIX shell")
	}
	root := t.TempDir()
	helper := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$CTX_TEST_SECRET_CALLS"
case "$1" in
  lookup)
    [ -f "$CTX_TEST_SECRET_VALUE" ] || exit 1
    cat "$CTX_TEST_SECRET_VALUE" ;;
  store) cat > "$CTX_TEST_SECRET_VALUE" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "secret-tool"), []byte(helper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CTX_TEST_SECRET_VALUE", filepath.Join(root, "value"))
	t.Setenv("CTX_TEST_SECRET_CALLS", filepath.Join(root, "calls"))
	store := SecretService{}
	ctx := context.Background()
	item := "service=ctx&account=work"
	if err := store.Put(ctx, item, []byte("private-value"), false); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, item, []byte("second-value"), false); !errors.Is(err, adapterkit.ErrExists) {
		t.Fatalf("unexpected non-replacing write: %v", err)
	}
	value, err := store.Get(ctx, item)
	if err != nil || string(value) != "private-value" {
		t.Fatalf("read=%q err=%v", value, err)
	}
	if err := store.Put(ctx, item, []byte("second-value\n"), true); err != nil {
		t.Fatal(err)
	}
	value, err = store.Get(ctx, item)
	if err != nil || string(value) != "second-value\n" {
		t.Fatalf("replaced read=%q err=%v", value, err)
	}
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil || strings.Contains(string(calls), "private-value") || strings.Contains(string(calls), "second-value") {
		t.Fatalf("credential appeared in helper arguments or call log unavailable: %v", err)
	}
}

func TestSecretServiceRejectsMalformedAttributes(t *testing.T) {
	for _, item := range []string{"", "service=", "service=one&service=two", "bad%00key=value", "service=a%00b"} {
		if _, err := parseItem(item); err == nil {
			t.Fatalf("accepted invalid item %q", item)
		}
	}
}
