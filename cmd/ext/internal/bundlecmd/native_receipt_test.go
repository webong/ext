package bundlecmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/cmd/ext/internal/packagelock"
)

func TestNativeUpgradeRecovery(t *testing.T) {
	libPath := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if libPath == "" {
		t.Skip("run scripts/package-acceptance.sh for C native digest acceptance")
	}
	t.Setenv("EXT_PLUGIN_LIBRARY", libPath)
	lib, err := nativeplugin.Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"after-backup", "after-publication"} {
		t.Run(phase, func(t *testing.T) {
			project := t.TempDir()
			prepare := func(body string) preparedNative {
				t.Helper()
				stage, err := os.MkdirTemp(project, ".prepared-")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stage, "generated.go"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				var digest string
				if err := lib.Library().Call("directory_digest", map[string]any{"root": stage}, &digest); err != nil {
					t.Fatal(err)
				}
				return preparedNative{ID: "native:ext_demo", Root: project, Relative: "internal/demo", Stage: stage, Digest: digest, lib: lib}
			}
			old := prepare("original")
			if err := old.publish(); err != nil {
				t.Fatal(err)
			}
			next := prepare("replacement")
			if phase == "after-backup" {
				next.afterBackup = func() error { return context.Canceled }
			} else {
				next.afterPublish = func() error { return context.Canceled }
			}
			if err := next.publish(); !errors.Is(err, context.Canceled) {
				t.Fatalf("interruption: %v", err)
			}
			receiptPath, backup, err := next.statePaths()
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := next.readReceipt(receiptPath)
			if err != nil || receipt.Pending != next.Digest || receipt.Current != old.Digest {
				t.Fatalf("pending receipt: %+v %v", receipt, err)
			}
			wrong := prepare("different release")
			if err := wrong.publish(); err == nil {
				t.Fatal("another release bypassed pending recovery")
			}
			// A crash discards staging. Retry reconstructs it from the pinned inputs.
			if err := os.RemoveAll(next.Stage); err != nil {
				t.Fatal(err)
			}
			retry := prepare("replacement")
			if err := retry.publish(); err != nil {
				t.Fatal(err)
			}
			receipt, err = retry.readReceipt(receiptPath)
			if err != nil || receipt.Pending != "" || receipt.Current != retry.Digest {
				t.Fatalf("completed receipt: %+v %v", receipt, err)
			}
			if _, err := os.Stat(backup); !os.IsNotExist(err) {
				t.Fatalf("backup not reconciled: %v", err)
			}
			dest := filepath.Join(project, "internal", "demo", "generated.go")
			if err := os.WriteFile(dest, []byte("user changes"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := prepare("third release").publish(); err == nil {
				t.Fatal("overwrote edited output")
			}
			data, err := os.ReadFile(dest)
			if err != nil || string(data) != "user changes" {
				t.Fatalf("lost user changes: %s %v", data, err)
			}
		})
	}
}

func TestNativeReceiptSymlinkRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".ext-bundle-state")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := (preparedNative{Root: root, Relative: "internal/demo"}).statePaths(); err == nil {
		t.Fatal("followed symlinked receipt directory")
	}
}

// Unlike a returned error, os.Exit skips every defer, including writer cleanup.
func TestNativeUpgradeProcessLoss(t *testing.T) {
	if os.Getenv("EXT_ACCEPTANCE_LIBRARY") == "" {
		t.Skip("run scripts/package-acceptance.sh for C native digest acceptance")
	}
	t.Setenv("EXT_PLUGIN_LIBRARY", os.Getenv("EXT_ACCEPTANCE_LIBRARY"))
	lib, err := nativeplugin.Open()
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(t *testing.T, root, body string) preparedNative {
		t.Helper()
		stage, err := os.MkdirTemp(root, ".prepared-")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "generated.go"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		var digest string
		if err := lib.Library().Call("directory_digest", map[string]any{"root": stage}, &digest); err != nil {
			t.Fatal(err)
		}
		return preparedNative{ID: "native:ext_demo", Root: root, Relative: "internal/demo", Stage: stage, Digest: digest, lib: lib}
	}
	if root := os.Getenv("EXT_NATIVE_CRASH_ROOT"); root != "" {
		release, err := packagelock.Acquire(root)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		next := prepare(t, root, "replacement")
		crash := func() error { os.Exit(73); return nil }
		switch os.Getenv("EXT_NATIVE_CRASH_PHASE") {
		case "after-backup":
			next.afterBackup = crash
		case "after-publication":
			next.afterPublish = crash
		default:
			t.Fatal("unknown crash phase")
		}
		if err := next.publish(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash hook did not exit")
	}
	for _, phase := range []string{"after-backup", "after-publication"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			old := prepare(t, root, "original")
			if err := old.publish(); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestNativeUpgradeProcessLoss$", "-test.timeout=20s")
			command.Dir = t.TempDir()
			command.Env = append(os.Environ(), "GOWORK=off", "EXT_HOME="+root, "EXT_NATIVE_CRASH_ROOT="+root, "EXT_NATIVE_CRASH_PHASE="+phase)
			out, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("child did not reach crash hook: %v\n%s", err, out)
			}
			if release, err := packagelock.Acquire(root); err == nil {
				release()
				t.Fatal("crashed writer lock was stolen")
			}
			// Wait has established that this exact child exited; deliberate recovery is
			// now safe. No production code infers abandonment from lock age.
			if err := os.Remove(filepath.Join(root, ".package-write.lock")); err != nil {
				t.Fatal(err)
			}
			release, err := packagelock.Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			retry := prepare(t, root, "replacement")
			receiptPath, backup, err := retry.statePaths()
			if err != nil {
				t.Fatal(err)
			}
			pending, err := retry.readReceipt(receiptPath)
			if err != nil || pending.Pending != retry.Digest || pending.Current != old.Digest {
				t.Fatalf("lost pending state: %+v %v", pending, err)
			}
			if err := retry.publish(); err != nil {
				t.Fatal(err)
			}
			receipt, err := retry.readReceipt(receiptPath)
			if err != nil || receipt.Pending != "" || receipt.Current != retry.Digest {
				t.Fatalf("unreconciled receipt: %+v %v", receipt, err)
			}
			if _, err := os.Stat(backup); !os.IsNotExist(err) {
				t.Fatalf("backup remains: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(root, "internal", "demo", "generated.go"))
			if err != nil || string(body) != "replacement" {
				t.Fatalf("wrong recovered contents: %s %v", body, err)
			}
		})
	}
}
