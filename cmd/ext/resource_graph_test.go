package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/webong/ext/cmd/ext/internal/resourcecmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// Exercise native routing through three separately installed processes, from a
// directory outside the workspace. Trust and reverse-call authority are distinct.
func TestResourceDependencyGraph(t *testing.T) {
	libraryPath := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if libraryPath == "" {
		t.Skip("run scripts/package-acceptance.sh")
	}
	home, bin, outside := t.TempDir(), t.TempDir(), t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext := filepath.Join(bin, "ext"+suffix)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, libraryPath)
	run := func(want int, args ...string) string {
		t.Helper()
		cmd := exec.Command(ext, args...)
		cmd.Dir, cmd.Env = outside, acceptanceEnv(home)
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want {
			t.Fatalf("%v: exit %d: %s", args, code, out)
		}
		return string(out)
	}
	contract := func(name string, ops ...string) plugin.Contract {
		c := plugin.Contract{ContractRef: plugin.ContractRef{Name: name, Version: "v1"}}
		for _, op := range ops {
			c.Operations = append(c.Operations, plugin.Operation{Name: op})
		}
		return c
	}
	resource, record, dependent, value := contract("ext.resource", "list", "inspect"), contract("example.record", "read"), contract("example.dependent", "read"), contract("example.value", "read")
	type fixture struct {
		id, revision, pkg  string
		contracts, reverse []plugin.Contract
		requires           []packagekit.Dependency
	}
	fixtures := []fixture{
		{"example/provider", "provider-v1", "provider", []plugin.Contract{value}, nil, nil},
		{"example/dependent", "dependent-v1", "dependent", []plugin.Contract{dependent}, []plugin.Contract{value}, []packagekit.Dependency{{Contract: value.ContractRef, Operation: "read", Provider: "example/provider"}}},
		{"example/resources", "resource-v1", "resourceguest", []plugin.Contract{resource, record}, []plugin.Contract{dependent}, []packagekit.Dependency{{Contract: dependent.ContractRef, Operation: "read", Provider: "example/dependent"}}},
	}
	var markers []string
	var middleDir string
	var middleManifest packagekit.Manifest
	for _, f := range fixtures {
		dir := t.TempDir()
		executable := filepath.Join(dir, "guest"+suffix)
		marker := filepath.Join(outside, f.pkg+".pid")
		markers = append(markers, marker)
		flags := "-X " + strconv.Quote("main.marker="+marker)
		if f.pkg == "resourceguest" {
			flags += " -X main.dependencies=enabled"
		}
		cmd := exec.Command("go", "build", "-ldflags", flags, "-o", executable, "./testdata/"+f.pkg)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
		m, err := packagekit.ExecutableManifest(f.id, "1.0.0", f.contracts, executable)
		if err != nil {
			t.Fatal(err)
		}
		m.Descriptor.Identity.Revision = f.revision
		m.Descriptor.Reverse = f.reverse
		m.Requires = f.requires
		if err := store.WriteManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		run(0, "package", "install", dir)
		if f.pkg == "dependent" {
			middleDir, middleManifest = dir, m
		}
	}
	run(0, "package", "trust", "example/resources")
	run(0, "package", "trust", "example/dependent")
	request := `{"resource":{"provider":{"id":"example/resources","revision":"resource-v1","version":"1.0.0"},"type":"example.record","id":"one"},"contract":{"name":"example.record","version":"v1"},"operation":"read","input":"dependency"}`
	call := []string{"resource", "call", "example/resources", request}
	run(1, call...)
	for _, marker := range markers {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("preflight launched %s: %v", marker, err)
		}
	}
	run(0, "package", "trust", "example/provider")
	grant := func(id, revision string, c plugin.ContractRef) string {
		data, err := json.Marshal(resourcecmd.DependencyGrant{Provider: plugin.Identity{ID: id, Revision: revision, Version: "1.0.0"}, Contract: c, Operation: "read"})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	middle := grant("example/dependent", "dependent-v1", dependent.ContractRef)
	leaf := grant("example/provider", "provider-v1", value.ContractRef)
	assertStopped := func(paths ...string) {
		t.Helper()
		var snapshotRoot string
		for _, marker := range paths {
			data, err := os.ReadFile(marker + ".executable")
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Dir(filepath.Dir(string(data)))
			if !strings.HasPrefix(filepath.Base(root), "ext-graph-snapshot-") {
				t.Fatalf("provider did not execute from a graph snapshot: %s", data)
			}
			if snapshotRoot != "" && root != snapshotRoot {
				t.Fatalf("providers used different graph snapshots: %s and %s", root, snapshotRoot)
			}
			snapshotRoot = root
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("graph snapshot survived host shutdown: %s: %v", root, err)
			}
		}
		if runtime.GOOS == "windows" {
			return
		}
		for _, marker := range paths {
			data, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil || pid <= 0 {
				t.Fatalf("invalid PID: %q", data)
			}
			process, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			err = process.Signal(syscall.Signal(0))
			_ = process.Release()
			if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("guest %d still alive: %v", pid, err)
			}
		}
	}
	for _, args := range [][]string{nil, {"--allow-dependency", middle}, {"--allow-dependency", middle, "--allow-dependency", grant("example/provider", "wrong-revision", value.ContractRef)}} {
		out := run(1, append(append([]string{}, call...), args...)...)
		if !strings.Contains(out, "denied") {
			t.Fatalf("expected denied: %s", out)
		}
		assertStopped(markers...)
	}
	args := append(append([]string{}, call...), "--allow-dependency", middle, "--allow-dependency", leaf)
	for range 2 {
		if out := run(0, args...); !strings.Contains(out, "from-provider") {
			t.Fatalf("missing routed result: %s", out)
		}
		assertStopped(markers...)
	}
	t.Run("startup-failure", func(t *testing.T) {
		// The package is valid and explicitly trusted, but the executable announces
		// a different revision. The leaf starts first; the root must never launch.
		for _, marker := range markers {
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
		}
		changed := middleManifest
		changed.Descriptor.Identity.Revision = "mismatched-revision"
		if err := store.WriteManifest(middleDir, changed); err != nil {
			t.Fatal(err)
		}
		run(0, "package", "upgrade", middleDir)
		run(0, "package", "trust", "example/dependent")
		out := run(1, args...)
		if !strings.Contains(out, "mismatch") {
			t.Fatalf("unexpected startup failure: %s", out)
		}
		for _, marker := range markers[:2] {
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("expected started guest %s: %v", marker, err)
			}
		}
		if _, err := os.Stat(markers[2]); !os.IsNotExist(err) {
			t.Fatalf("root started after failed dependency: %v", err)
		}
		assertStopped(markers[:2]...)
		// Restoration must acquire the writer lock and a fresh graph must work.
		if err := store.WriteManifest(middleDir, middleManifest); err != nil {
			t.Fatal(err)
		}
		run(0, "package", "upgrade", middleDir)
		run(0, "package", "trust", "example/dependent")
		if out := run(0, args...); !strings.Contains(out, "from-provider") {
			t.Fatalf("call after startup failure: %s", out)
		}
		assertStopped(markers...)
	})

	if runtime.GOOS != "windows" {
		t.Run("cancel-chain", func(t *testing.T) {
			waitArgs := append([]string{}, args...)
			waitArgs[3] = strings.Replace(request, `"input":"dependency"`, `"input":"dependency-wait"`, 1)
			waiting := markers[0] + ".waiting"
			cmd := exec.Command(ext, waitArgs...)
			cmd.Dir, cmd.Env = outside, acceptanceEnv(home)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			finished := false
			defer func() {
				if !finished {
					_ = cmd.Process.Kill()
					<-done
				}
			}()
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(waiting); err == nil {
					break
				}
				select {
				case err := <-done:
					finished = true
					t.Fatalf("early exit: %v %s", err, output.String())
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("leaf did not receive routed call")
				}
				time.Sleep(10 * time.Millisecond)
			}
			for _, marker := range markers {
				path, err := os.ReadFile(marker + ".executable")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(string(path)); err != nil {
					t.Fatalf("live graph lost its executable snapshot: %v", err)
				}
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				finished = true
				if err == nil || !strings.Contains(output.String(), "context canceled") {
					t.Fatalf("cancellation: %v %s", err, output.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("graph cancellation did not join hosts promptly")
			}
			assertStopped(markers...)
			if out := run(0, args...); !strings.Contains(out, "from-provider") {
				t.Fatalf("call after cancellation: %s", out)
			}
			assertStopped(markers...)
		})
	}

}
