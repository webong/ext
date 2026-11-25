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

func TestResourceDiscovery(t *testing.T) {
	libraryPath := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if libraryPath == "" {
		t.Skip("run scripts/package-acceptance.sh for C resource discovery acceptance")
	}
	home, bin, outside := t.TempDir(), t.TempDir(), t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext := filepath.Join(bin, "ext"+suffix)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, libraryPath)
	marker := filepath.Join(outside, "provider-started")
	buildProbe := func(out, pkg string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-ldflags", "-X "+strconv.Quote("main.marker="+marker), "-o", out, pkg)
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("probe build: %v %s", err, data)
		}
	}
	probe := filepath.Join(bin, "resourceprobe"+suffix)
	buildProbe(probe, "./testdata/resourceprobe")
	_ = exec.Command(probe).Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("probe did not record launch: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	probeBytes, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	run := func(want int, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(ext, args...)
		cmd.Dir, cmd.Env = outside, append(acceptanceEnv(home), "EXT_RESOURCE_PROBE="+marker)
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
			t.Fatalf("%v: %d %s", args, code, out)
		}
		return out
	}
	if got := strings.TrimSpace(string(run(0, "resource", "providers"))); got != "[]" {
		t.Fatalf("empty: %s", got)
	}
	makePackage := func(id, version string, ops ...string) string {
		t.Helper()
		dir := t.TempDir()
		executable := filepath.Join(dir, "never-run"+suffix)
		// The executable records any attempted launch, even if the caller
		// ignores its failure and falls back to manifest metadata.
		if err := os.WriteFile(executable, probeBytes, 0700); err != nil {
			t.Fatal(err)
		}
		contract := plugin.Contract{ContractRef: plugin.ContractRef{Name: "ext.resource", Version: version}}
		for _, op := range ops {
			contract.Operations = append(contract.Operations, plugin.Operation{Name: op})
		}
		m, err := packagekit.ExecutableManifest(id, "1.0.0", []plugin.Contract{contract}, executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.WriteManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		run(0, "package", "install", dir)
		return dir
	}
	makePackage("example/zeta", "v1", "list", "inspect")
	makePackage("example/alpha", "v1", "list", "inspect")
	makePackage("example/partial", "v1", "list")
	makePackage("example/future", "v2", "list", "inspect")
	var providers []resourcecmd.Provider
	if err := json.Unmarshal(run(0, "resource", "providers"), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].Descriptor.Identity.ID != "example/alpha" || providers[1].Descriptor.Identity.ID != "example/zeta" {
		t.Fatalf("providers: %+v", providers)
	}
	for _, p := range providers {
		if p.Observation != "manifest" {
			t.Fatalf("claimed live observation: %+v", p)
		}
		if !strings.Contains(string(run(0, "package", "verify", p.Descriptor.Identity.ID)), "trusted=false") {
			t.Fatal("discovery granted approval")
		}
	}
	run(2, "resource", "list")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("offline discovery launched a provider: %v", err)
	}
	// Verification covers installed bytes, not merely the manifest's claims.
	if err := os.WriteFile(filepath.Join(home, "packages", "example%2falpha", "never-run"+suffix), []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	run(1, "resource", "providers")

	// Remove the corrupted fixture explicitly; it must not poison later plans.
	run(0, "package", "remove", "example/alpha", "--force")
	dir := t.TempDir()
	live := filepath.Join(dir, "resourceguest"+suffix)
	buildProbe(live, "./testdata/resourceguest")
	manifest, err := packagekit.ExecutableManifest("example/resources", "1.0.0", []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "ext.resource", Version: "v1"}, Operations: []plugin.Operation{{Name: "list"}, {Name: "inspect"}}}}, live)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Descriptor.Contracts = append(manifest.Descriptor.Contracts, plugin.Contract{ContractRef: plugin.ContractRef{Name: "example.record", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}})
	manifest.Descriptor.Identity.Revision = "resource-v1"
	if err := store.WriteManifest(dir, manifest); err != nil {
		t.Fatal(err)
	}
	run(0, "package", "install", dir)
	run(1, "resource", "list", "example/resources", `{}`)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("untrusted discovery launched provider: %v", err)
	}
	run(0, "package", "trust", "example/resources")
	first := run(0, "resource", "list", "example/resources", `{"limit":1}`)
	if !strings.Contains(string(first), `"next":"page2"`) {
		t.Fatalf("first page: %s", first)
	}
	second := run(0, "resource", "list", "example/resources", `{"cursor":"page2"}`)
	if !strings.Contains(string(second), `"id":"two"`) {
		t.Fatalf("second page: %s", second)
	}
	request := `{"resource":{"provider":{"id":"example/resources","revision":"resource-v1","version":"1.0.0"},"type":"example.record","id":"one"}}`
	run(0, "resource", "inspect", "example/resources", request)
	callRequest := strings.TrimSuffix(request, "}") + `,"contract":{"name":"example.record","version":"v1"},"operation":"read","input":"name"}`
	if out := run(0, "resource", "call", "example/resources", callRequest); !strings.Contains(string(out), "value:one:name") {
		t.Fatalf("resource call lost identity/input: %s", out)
	}
	if err := os.Remove(marker + ".call"); err != nil {
		t.Fatal(err)
	}
	run(1, "resource", "call", "example/resources", strings.Replace(callRequest, `"input":"name"`, `"input":42`, 1))
	run(1, "resource", "call", "example/resources", strings.Replace(callRequest, `"operation":"read"`, `"operation":"write"`, 1))
	if _, err := os.Stat(marker + ".call"); !os.IsNotExist(err) {
		t.Fatalf("rejected call reached operation: %v", err)
	}
	run(1, "resource", "call", "example/resources", strings.Replace(callRequest, `"input":"name"`, `"input":"bad-result"`, 1))
	if out := run(1, "resource", "call", "example/resources", strings.Replace(callRequest, `"id":"one"`, `"id":"denied-on-call"`, 1)); !strings.Contains(string(out), "resource no longer authorized") {
		t.Fatalf("lost invocation denial: %s", out)
	}

	for id, code := range map[string]string{"missing": "not_found", "private": "denied"} {
		out := run(1, "resource", "inspect", "example/resources", strings.Replace(request, `"id":"one"`, `"id":"`+id+`"`, 1))
		if !strings.Contains(string(out), "plugin: "+code+":") {
			t.Fatalf("lost %s error: %s", code, out)
		}
	}
	if out := run(1, "resource", "list", "example/resources", `{"cursor":"domain-error"}`); !strings.Contains(string(out), "invalid resource error") {
		t.Fatalf("accepted domain error as discovery error: %s", out)
	}

	run(1, "resource", "list", "example/resources", `{"cursor":"bad"}`)
	if out := run(1, "resource", "list", "example/resources", `{"cursor":"expired"}`); !strings.Contains(string(out), "expired") {
		t.Fatalf("lost cursor error: %s", out)
	}
	if out := run(1, "resource", "list", "example/resources", `{"type":"test.timeout"}`); !strings.Contains(string(out), "timeout") {
		t.Fatalf("lost deadline error: %s", out)
	}
	// Windows needs a console-control-event harness; os.Process.Signal cannot
	// deliver os.Interrupt there. This exercises actual POSIX CLI interruption.
	if runtime.GOOS != "windows" {
		for _, scenario := range []struct{ op, request string }{
			{"list", `{"type":"test.cancel"}`},
			{"call", strings.Replace(callRequest, `"input":"name"`, `"input":"wait-for-cancel"`, 1)},
		} {
			t.Run("cancel-"+scenario.op, func(t *testing.T) {
				_ = os.Remove(marker + ".waiting")
				cmd := exec.Command(ext, "resource", scenario.op, "example/resources", scenario.request)
				cmd.Dir, cmd.Env = outside, acceptanceEnv(home)
				var output bytes.Buffer
				cmd.Stdout, cmd.Stderr = &output, &output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				joined := false
				defer func() {
					if !joined {
						_ = cmd.Process.Kill()
						<-done
					}
				}()
				readyBy := time.Now().Add(10 * time.Second)
				for {
					if _, err := os.Stat(marker + ".waiting"); err == nil {
						break
					}
					select {
					case err := <-done:
						joined = true
						t.Fatalf("exited before active call: %v %s", err, output.String())
					default:
					}
					if time.Now().After(readyBy) {
						t.Fatal("provider never entered the operation")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					joined = true
					if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(output.String(), "context canceled") {
						t.Fatalf("cancellation: %v %s", err, output.String())
					}
				case <-time.After(3 * time.Second):
					t.Fatal("interrupted call did not finish before its normal deadline")
				}
				pidBytes, err := os.ReadFile(marker + ".waiting")
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(pidBytes))
				if err != nil || pid <= 0 {
					t.Fatalf("invalid provider PID: %q (%v)", pidBytes, err)
				}
				provider, err := os.FindProcess(pid)
				if err != nil {
					t.Fatal(err)
				}
				err = provider.Signal(syscall.Signal(0))
				_ = provider.Release()
				if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
					t.Fatalf("provider remains after cancellation: PID %d, signal probe %v", pid, err)
				}
				// A fresh resource call acquires the writer and uses a new host.
				next := exec.Command(ext, "resource", "list", "example/resources", `{}`)
				next.Dir, next.Env = outside, acceptanceEnv(home)
				if output, err := next.CombinedOutput(); err != nil {
					t.Fatalf("call after cancellation: %v %s", err, output)
				}
			})
		}
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	run(1, "resource", "inspect", "example/resources", strings.Replace(request, "resource-v1", "wrong", 1))
	run(1, "resource", "list", "example/resources", `{"limit":0}`)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid request launched provider: %v", err)
	}
	run(0, "package", "untrust", "example/resources")
	run(1, "resource", "call", "example/resources", callRequest)
	run(1, "resource", "list", "example/resources", `{}`)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("revoked provider launched: %v", err)
	}
}
