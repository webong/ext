package ledger_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	ledger "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func volumeLibrary(t *testing.T) *ledger.Library {
	t.Helper()
	path := os.Getenv("EXT_LEDGER_LIBRARY")
	if path == "" {
		t.Skip("requires EXT_LEDGER_LIBRARY")
	}
	native, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := ledger.Bind(native)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func TestNativePrivateVolume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows native volume implementation is not available yet")
	}
	lib := volumeLibrary(t)
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "agent", "state")
	created, err := lib.CreatePrivateVolume(ctx, path, ledger.PrivateVolumeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Path != path || created.Identity == "" || created.Entries != 0 {
		t.Fatalf("created = %+v", created)
	}
	if err := os.WriteFile(filepath.Join(path, "item"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspected, err := lib.InspectPrivateVolume(ctx, path, ledger.PrivateVolumeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Created || inspected.Identity != created.Identity || inspected.Entries != 1 || inspected.Bytes != 4 {
		t.Fatalf("inspected = %+v", inspected)
	}
	if err := lib.RemovePrivateVolume(ctx, inspected, ledger.PrivateVolumeLimits{MaxBytes: 3}); !errors.Is(err, ledger.ErrVolumeLimit) {
		t.Fatalf("byte bound = %v", err)
	}
	if err := lib.RemovePrivateVolume(ctx, inspected, ledger.PrivateVolumeLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed directory = %v", err)
	}
	if err := lib.RemovePrivateVolume(ctx, ledger.PrivateVolume{Path: root}, ledger.PrivateVolumeLimits{}); !errors.Is(err, ledger.ErrInvalidVolume) {
		t.Fatalf("hand-built volume = %v", err)
	}
}

func TestNativePrivateVolumeSafety(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows native volume implementation is not available yet")
	}
	lib := volumeLibrary(t)
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "private")
	if _, err := lib.CreatePrivateVolume(ctx, "/", ledger.PrivateVolumeLimits{}); !errors.Is(err, ledger.ErrUnsafeVolumePath) {
		t.Fatalf("root target = %v", err)
	}
	if _, err := lib.InspectPrivateVolume(ctx, path, ledger.PrivateVolumeLimits{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory = %v", err)
	}
	first, err := lib.CreatePrivateVolume(ctx, path, ledger.PrivateVolumeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemovePrivateVolume(ctx, first, ledger.PrivateVolumeLimits{}); !errors.Is(err, ledger.ErrUnsafeVolumePath) {
		t.Fatalf("link inside volume = %v", err)
	}
	if err := os.Remove(filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	moved := path + "-old"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemovePrivateVolume(ctx, first, ledger.PrivateVolumeLimits{}); !errors.Is(err, ledger.ErrStaleVolume) {
		t.Fatalf("stale identity = %v", err)
	}
}
