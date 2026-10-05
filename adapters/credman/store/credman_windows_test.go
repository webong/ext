//go:build windows

package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/webong/ctx/res/credential/adapterkit"
)

func TestNativeCredentialManagerRoundTrip(t *testing.T) {
	if os.Getenv("CTX_CREDENTIAL_NATIVE_TESTS") != "1" {
		t.Skip("set CTX_CREDENTIAL_NATIVE_TESTS=1 for a synthetic Credential Manager fixture")
	}
	target := fmt.Sprintf("ctx/synthetic/%d/%d", os.Getpid(), time.Now().UnixNano())
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	deleteProc := credentialDLL.NewProc("CredDeleteW")
	t.Cleanup(func() {
		ok, _, callErr := deleteProc.Call(uintptr(unsafe.Pointer(name)), genericCredential, 0)
		if ok == 0 && !errors.Is(callErr, errorNotFound) {
			t.Errorf("remove synthetic Credential Manager item: %v", callErr)
		}
	})
	item := "target=" + url.QueryEscape(target)
	store := CredentialManager{}
	ctx := context.Background()
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
