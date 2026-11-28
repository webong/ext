package browserhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/core/dirlock"
	adapter "github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	browser "github.com/webong/ext/res/web/host/manage/cookie"
)

func TestBrowserInvocationSnapshotAndReadmission(t *testing.T) {
	home := filepath.Join(t.TempDir(), "adapters")
	// The explicit provider store, not global EXT_HOME, owns trust and locking.
	t.Setenv("EXT_HOME", t.TempDir())
	owner := adapter.NewStore(home)
	lock := filepath.Join(filepath.Dir(home), ".package-write.lock")
	t.Setenv("EXT_TEST_BROWSER_LOCK", lock)
	marker := filepath.Join(t.TempDir(), "launched")
	t.Setenv("EXT_TEST_BROWSER_LAUNCH", marker)
	script := `mkdir "$EXT_TEST_BROWSER_LOCK" && rmdir "$EXT_TEST_BROWSER_LOCK" || exit 9
printf '%s' "$0" > "$EXT_TEST_BROWSER_LAUNCH"
printf '%s' "$0"`
	installQueryFixture(t, owner, "fixture", 0, false, script, false)
	sources, _, err := (Provider{AdapterHome: home}).Sources(context.Background(), browser.Options{Sources: []string{"fixture:default"}})
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %v %v", sources, err)
	}
	invoke := func() ([]byte, error) {
		return sources[0].Invoke(context.Background(), "share", []string{"cookie", "query"}, nil)
	}
	release, err := dirlock.Acquire(lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(); err == nil || !strings.Contains(err.Error(), "writer unavailable") {
		t.Fatalf("contention: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("contended invocation launched: %v", err)
	}
	out, err := invoke()
	if err != nil {
		t.Fatal(err)
	}
	path := string(out)
	if !strings.Contains(path, "ext-command-graph-") {
		t.Fatalf("not a snapshot: %q", path)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("copy retained after call: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := owner.RemoveTrust("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("cached source retained authority: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("revoked invocation launched: %v", err)
	}
	// A fresh approval must not make an old selection silently switch content.
	if err := owner.Remove("fixture"); err != nil {
		t.Fatal(err)
	}
	installQueryFixture(t, owner, "fixture", 0, false, script+"\n# changed executable", false)
	if _, err := invoke(); err == nil || !strings.Contains(err.Error(), "changed after selection") {
		t.Fatalf("stale selection accepted: %v", err)
	}
}

func TestBrowserSnapshotCancellationCleanup(t *testing.T) {
	home := filepath.Join(t.TempDir(), "adapters")
	owner := adapter.NewStore(home)
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("EXT_TEST_BROWSER_LAUNCH", marker)
	installQueryFixture(t, owner, "waiting", 0, false, `printf '%s' "$0" > "$EXT_TEST_BROWSER_LAUNCH"; exec sleep 30`, false)
	sources, _, err := (Provider{AdapterHome: home}).Sources(context.Background(), browser.Options{Sources: []string{"waiting:default"}})
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %v %v", sources, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := sources[0].Invoke(ctx, "share", []string{"cookie", "query"}, nil); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	var path []byte
	for {
		path, err = os.ReadFile(marker)
		if err == nil && len(path) > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("early invocation exit: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("guest did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled guest did not finish")
	}
	if _, err := os.Stat(filepath.Dir(string(path))); !os.IsNotExist(err) {
		t.Fatalf("canceled copy retained: %s %v", path, err)
	}
	release, err := dirlock.Acquire(filepath.Join(filepath.Dir(home), ".package-write.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserInvocationUsesDeclaredDependencies(t *testing.T) {
	for _, declared := range []bool{false, true} {
		name := "none"
		if declared {
			name = "declared"
		}
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "adapters")
			owner := adapter.NewStore(home)
			t.Setenv("EXT_DEPENDENCY_CREDENTIAL", "forged")
			t.Setenv("ext_dependency_network", "forged")
			t.Setenv("EXT_DEPENDENCY_OTHER", "forged")
			var dependencies []string
			if declared {
				dependencies = []string{"credential:vault@2.0"}
				source := t.TempDir()
				manifest := "api_version = \"2.0\"\nname = \"vault\"\nruntime = \"computer\"\nsurfaces = \"shell\"\nexecutable = \"guest\"\ncapabilities = \"share,validate,doctor\"\nshare_spaces = \"credential\"\nselectable = \"false\"\nself_contained = \"true\"\n"
				if err := adapter.WriteKeyValueManifest(source, manifest); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "guest"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
				dependency, err := owner.Install(source)
				if err != nil {
					t.Fatal(err)
				}
				if err := owner.Trust(dependency); err != nil {
					t.Fatal(err)
				}

			}
			script := `printf '%s|%s|%s' "${EXT_DEPENDENCY_CREDENTIAL-}" "${ext_dependency_network-}" "${EXT_DEPENDENCY_OTHER-}"`
			installQueryFixture(t, owner, "fixture", 0, false, script, false, dependencies...)
			sources, _, err := (Provider{AdapterHome: home}).Sources(context.Background(), browser.Options{Sources: []string{"fixture:default"}})
			if err != nil || len(sources) != 1 {
				t.Fatalf("sources: %v %v", sources, err)
			}
			out, err := sources[0].Invoke(context.Background(), "share", []string{"cookie", "query"}, nil)
			want := "||"
			if declared {
				want = "vault||"
			}
			if err != nil || string(out) != want {
				t.Fatalf("dependency handoff: %q %v; want %q", out, err, want)
			}
		})
	}
}

func TestBrowserSnapshotRejectsUnexpectedFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "adapters")
	owner := adapter.NewStore(home)
	installQueryFixture(t, owner, "fixture", 0, false, "printf should-not-run", false)
	sources, _, err := (Provider{AdapterHome: home}).Sources(context.Background(), browser.Options{Sources: []string{"fixture:default"}})
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %v %v", sources, err)
	}
	if err := os.WriteFile(filepath.Join(home, "unexpected"), []byte("not a package"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := sources[0].Invoke(context.Background(), "share", []string{"cookie", "query"}, nil)
	if err == nil || len(out) != 0 {
		t.Fatalf("unexpected file admitted: %q %v", out, err)
	}
}
