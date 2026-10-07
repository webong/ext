//go:build darwin && cgo

package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webong/ext/res/credential/adapterkit"
)

func TestNativeKeychainCredentialRoundTrip(t *testing.T) {
	if os.Getenv("CTX_CREDENTIAL_NATIVE_TESTS") != "1" {
		t.Skip("set CTX_CREDENTIAL_NATIVE_TESTS=1 for an isolated Keychain fixture")
	}
	path := filepath.Join(t.TempDir(), "ctx-credential-test.keychain-db")
	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		if output, err := exec.CommandContext(ctx, "/usr/bin/security", args...).CombinedOutput(); err != nil {
			t.Fatalf("isolated Keychain setup failed: %v: %s", err, output)
		}
	}
	run("create-keychain", "-p", "synthetic-keychain-password", path)
	t.Cleanup(func() {
		if err := exec.CommandContext(ctx, "/usr/bin/security", "delete-keychain", path).Run(); err != nil {
			t.Errorf("remove isolated Keychain: %v", err)
		}
	})
	run("unlock-keychain", "-p", "synthetic-keychain-password", path)
	store := Keychain{Path: path}
	item := "service=CTX+Synthetic&account=fixture"
	if err := store.Put(ctx, item, []byte("synthetic-secret"), false); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, item, []byte("second-secret"), false); !errors.Is(err, adapterkit.ErrExists) {
		t.Fatalf("non-replacing write: %v", err)
	}
	value, err := store.Get(ctx, item)
	if err != nil || string(value) != "synthetic-secret" {
		t.Fatalf("read=%q err=%v", value, err)
	}
	if err := store.Put(ctx, item, []byte("second-secret"), true); err != nil {
		t.Fatal(err)
	}
	value, err = store.Get(ctx, item)
	if err != nil || string(value) != "second-secret" {
		t.Fatalf("replaced read=%q err=%v", value, err)
	}
}
