package volume

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func privatePathForTest(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	return root, filepath.Join(root, "agent", "state")
}

func TestCreateInspectAndRemovePrivateVolume(t *testing.T) {
	root, path := privatePathForTest(t)
	volume, err := CreatePrivate(context.Background(), path, PrivateLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if !volume.Created || volume.Entries != 0 || volume.Bytes != 0 || volume.Identity == "" {
		t.Fatalf("created volume = %#v", volume)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("volume is not a directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("volume mode = %o", info.Mode().Perm())
	}
	if err = os.MkdirAll(filepath.Join(path, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "nested", "value.bin"), make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	inspected, err := InspectPrivate(context.Background(), path, PrivateLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Created || inspected.Entries != 2 || inspected.Bytes != 32 {
		t.Fatalf("inspected volume = %#v", inspected)
	}
	if err = RemovePrivate(context.Background(), inspected, PrivateLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("volume still exists: %v", err)
	}
	second, err := CreatePrivate(context.Background(), path, PrivateLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Created || !filepath.IsAbs(second.Path) {
		t.Fatalf("recreated volume = %#v", second)
	}
	if err = RemovePrivate(context.Background(), second, PrivateLimits{}); err != nil {
		t.Fatal(err)
	}
	_ = root
}

func TestCreateRejectsBroadAndRelativeTargets(t *testing.T) {
	home, _ := os.UserHomeDir()
	for name, path := range map[string]string{
		"relative":    "relative/path",
		"root":        "/",
		"temperature": os.TempDir(),
		"home":        home,
		"system":      "/Users",
	} {
		if path == "" {
			continue
		}
		if _, err := CreatePrivate(context.Background(), path, PrivateLimits{}); !errors.Is(err, ErrUnsafePath) && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s target = %v", name, err)
		}
	}
	if _, err := CreatePrivate(context.Background(), "/Users/../etc/../", PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("traversal target = %v", err)
	}
}

func TestCreateRejectsSymlinkComponentsAndTargets(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(root, "linked")
	if err := os.Symlink(real, component); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePrivate(context.Background(), filepath.Join(component, "state"), PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink component = %v", err)
	}
	target := filepath.Join(root, "target")
	if err := os.Symlink(real, target); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePrivate(context.Background(), target, PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink target = %v", err)
	}
	if _, err := InspectPrivate(context.Background(), target, PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("inspect symlink target = %v", err)
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatalf("symlink target was mutated: %v", err)
	}
}

func TestInspectRejectsBroadPermissionsAndFiles(t *testing.T) {
	root := t.TempDir()
	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPrivate(context.Background(), open, PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("broad permissions = %v", err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPrivate(context.Background(), file, PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("file target = %v", err)
	}
	if _, err := InspectPrivate(context.Background(), filepath.Join(root, "missing"), PrivateLimits{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing target = %v", err)
	}
}

func TestRemoveRefusesLinksWithoutTouchingThem(t *testing.T) {
	outer := t.TempDir()
	outside := filepath.Join(outer, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, path := privatePathForTest(t)
	volume, err := CreatePrivate(context.Background(), path, PrivateLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(path, "escape")); err != nil {
		t.Fatal(err)
	}
	if err = RemovePrivate(context.Background(), volume, PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink inside volume = %v", err)
	}
	if _, err = os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("volume was removed despite refusal: %v", err)
	}
	_ = root
}

func TestRemoveEnforcesBoundsBeforeDeleting(t *testing.T) {
	_, path := privatePathForTest(t)
	volume, err := CreatePrivate(context.Background(), path, PrivateLimits{})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		name := filepath.Join(path, "entry-"+string(rune('a'+index)))
		if err = os.WriteFile(name, make([]byte, 8), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err = RemovePrivate(context.Background(), volume, PrivateLimits{MaxEntries: 2}); !errors.Is(err, ErrLimit) {
		t.Fatalf("entry bound = %v", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("volume was removed despite the entry bound: %v", err)
	}
	if err = RemovePrivate(context.Background(), volume, PrivateLimits{MaxBytes: 8}); !errors.Is(err, ErrLimit) {
		t.Fatalf("byte bound = %v", err)
	}
	if err = RemovePrivate(context.Background(), volume, PrivateLimits{}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveRejectsStaleNativeIdentity(t *testing.T) {
	_, path := privatePathForTest(t)
	volume, err := CreatePrivate(context.Background(), path, PrivateLimits{})
	if err != nil {
		t.Fatal(err)
	}
	moved := path + "-old"
	if err = os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "replacement"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = RemovePrivate(context.Background(), volume, PrivateLimits{}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale identity = %v", err)
	}
	if _, err = os.Stat(filepath.Join(path, "replacement")); err != nil {
		t.Fatalf("replacement was removed: %v", err)
	}
	if err = os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(moved, path); err != nil {
		t.Fatal(err)
	}
	if err = RemovePrivate(context.Background(), volume, PrivateLimits{}); err != nil {
		t.Fatalf("restored identity = %v", err)
	}
}

func TestRemoveRequiresInspectedVolume(t *testing.T) {
	root, _ := privatePathForTest(t)
	if err := RemovePrivate(context.Background(), PrivateVolume{Path: root}, PrivateLimits{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hand-built volume = %v", err)
	}
}

func TestRemoveRevalidatesPathAfterInspection(t *testing.T) {
	_, path := privatePathForTest(t)
	volume, err := CreatePrivate(context.Background(), path, PrivateLimits{})
	if err != nil {
		t.Fatal(err)
	}
	volume.Path = "/"
	if err = RemovePrivate(context.Background(), volume, PrivateLimits{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("broad path after inspection = %v", err)
	}
}

func TestPrivateLimitsAndCancellation(t *testing.T) {
	for name, limits := range map[string]PrivateLimits{
		"entries": {MaxEntries: MaxEntries + 1},
		"bytes":   {MaxBytes: MaxBytes + 1},
		"depth":   {MaxDepth: MaxDepth + 1},
		"zero":    {MaxEntries: -1},
	} {
		if _, err := limits.normalize(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s limits = %v", name, err)
		}
	}
	root, _ := privatePathForTest(t)
	deep := root
	for index := 0; index < 6; index++ {
		deep = filepath.Join(deep, "level")
	}
	deep = filepath.Join(deep, "state")
	if _, err := CreatePrivate(context.Background(), deep, PrivateLimits{MaxDepth: 4}); !errors.Is(err, ErrLimit) {
		t.Fatalf("depth bound = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreatePrivate(ctx, filepath.Join(root, "canceled"), PrivateLimits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create = %v", err)
	}
	if _, err := InspectPrivate(ctx, root, PrivateLimits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inspect = %v", err)
	}
}

func TestBroadWindowsWriteSIDs(t *testing.T) {
	for _, sid := range []string{
		"S-1-1-0", "S-1-5-7", "S-1-5-11", "S-1-5-32-545", "S-1-5-32-546",
	} {
		if !broadWriteSID(sid) {
			t.Errorf("%s was not treated as a broad writer", sid)
		}
	}
	for _, sid := range []string{"S-1-5-18", "S-1-5-32-544", "S-1-5-21-1-2-3-1001"} {
		if broadWriteSID(sid) {
			t.Errorf("%s was treated as a broad writer", sid)
		}
	}
}
