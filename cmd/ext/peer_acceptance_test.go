package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/cmd/ext/internal/resourcecmd"
	"github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestSameMachinePeers(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("same-user Unix sockets are supported on macOS and Linux")
	}
	libraryPath := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if libraryPath == "" {
		t.Skip("run scripts/package-acceptance.sh")
	}
	consumer, provider, bin, outside := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	ledgerPath := filepath.Join(outside, "ledger")
	ext := filepath.Join(bin, "ext")
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, libraryPath)
	command := func(home string, args ...string) *exec.Cmd {
		cmd := exec.Command(ext, args...)
		cmd.Dir, cmd.Env = outside, append(acceptanceEnv(home), "EXT_LEDGER_PATH="+ledgerPath)
		return cmd
	}
	run := func(home string, success bool, args ...string) string {
		t.Helper()
		out, err := command(home, args...).CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("%v: %v: %s", args, err, out)
		}
		return string(out)
	}
	contract := func(name string, operations ...string) plugin.Contract {
		c := plugin.Contract{ContractRef: plugin.ContractRef{Name: name, Version: "v1"}}
		for _, operation := range operations {
			c.Operations = append(c.Operations, plugin.Operation{Name: operation})
		}
		return c
	}
	resource := contract("ext.resource", "list", "inspect")
	record := contract("example.record", "read")
	dependent := contract("example.dependent", "read")
	value := contract("example.value", "read")
	install := func(home, guest, id, revision string, contracts []plugin.Contract, reverse *plugin.Contract, dependencyID string) {
		t.Helper()
		dir := t.TempDir()
		executable := filepath.Join(dir, "guest")
		if guest == "resourceguest" {
			cmd := exec.Command("go", "build", "-ldflags", "-X main.dependencies=enabled", "-o", executable, "./testdata/"+guest)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, out)
			}
		} else {
			goBuild(t, executable, "./testdata/"+guest)
		}
		manifest, err := packagekit.ExecutableManifest(id, "1.0.0", contracts, executable)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Descriptor.Identity.Revision = revision
		if reverse != nil {
			manifest.Descriptor.Reverse = []plugin.Contract{*reverse}
			manifest.Requires = []packagekit.Dependency{{Contract: reverse.ContractRef, Operation: "read", Provider: dependencyID}}
		}
		if err := store.WriteManifestAt(dir, storelayout.ExtManifest, manifest); err != nil {
			t.Fatal(err)
		}
		run(home, true, "package", "install", dir)
	}
	install(provider, "provider", "example/provider", "provider-v1", []plugin.Contract{value}, nil, "")
	install(consumer, "dependent", "example/dependent", "dependent-v1", []plugin.Contract{dependent}, &value, "example/provider")
	install(consumer, "resourceguest", "example/resources", "resource-v1", []plugin.Contract{resource, record}, &dependent, "example/dependent")
	run(provider, false, "which", "peer")
	if output := run(provider, false, "package", "serve"); !strings.Contains(output, "usage: ext package serve ") {
		t.Fatalf("unexpected package serve usage: %s", output)
	}
	serveArgs := []string{"package", "serve", "example/provider", "example.value@v1"}
	run(provider, false, serveArgs...)
	run(provider, true, "package", "trust", "example/provider")
	start := func() func(os.Signal) {
		t.Helper()
		cmd := command(provider, serveArgs...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waited := false
		t.Cleanup(func() {
			if !waited {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		ready := make(chan string, 1)
		go func() {
			line, _ := bufio.NewReader(stdout).ReadString('\n')
			ready <- line
		}()
		select {
		case line := <-ready:
			if !strings.HasPrefix(line, "serving ") {
				_ = cmd.Wait()
				waited = true
				t.Fatalf("provider did not start: %s %s", line, stderr.String())
			}
		case <-time.After(10 * time.Second):
			t.Fatal("peer readiness timed out")
		}
		return func(signal os.Signal) {
			t.Helper()
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			err := cmd.Wait()
			waited = true
			if signal != os.Kill && err != nil {
				t.Fatalf("peer shutdown: %v: %s", err, stderr.String())
			}
		}
	}
	listPeers := func() []nativeplugin.Peer {
		t.Helper()
		peers, err := nativeplugin.LivePeers(context.Background(), ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		return peers
	}

	stop := start()
	peers := listPeers()
	if len(peers) != 1 || peers[0].Session.Namespace == "" || peers[0].Descriptor.Contracts[0].ContractRef != value.ContractRef {
		t.Fatalf("unexpected peer registrations: %+v", peers)
	}
	providerSession := peers[0].Session
	run(consumer, true, "package", "trust", "example/resources")
	run(consumer, true, "package", "trust", "example/dependent")
	request := `{"resource":{"provider":{"id":"example/resources","revision":"resource-v1","version":"1.0.0"},"type":"example.record","id":"one"},"contract":{"name":"example.record","version":"v1"},"operation":"read","input":"dependency"}`
	grant := func(id, revision string, c plugin.ContractRef) string {
		data, err := json.Marshal(resourcecmd.DependencyGrant{
			Provider: plugin.Identity{ID: id, Revision: revision, Version: "1.0.0"}, Contract: c, Operation: "read",
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	call := []string{"resource", "call", "example/resources", request, "--allow-dependency", grant("example/dependent", "dependent-v1", dependent.ContractRef)}
	run(consumer, false, call...)
	run(consumer, false, append(append([]string{}, call...), "--allow-dependency", grant("example/provider", "wrong-revision", value.ContractRef))...)
	call = append(call, "--allow-dependency", grant("example/provider", "provider-v1", value.ContractRef))
	if out := run(consumer, true, call...); !strings.Contains(out, "from-provider") {
		t.Fatalf("unexpected peer result: %s", out)
	}
	if current := listPeers(); len(current) != 1 || current[0].Session != providerSession {
		t.Fatalf("consumer shutdown changed provider registration: %+v", current)
	}
	checkPeerRoute(t, ledgerPath, providerSession)
	run(provider, true, "package", "untrust", "example/provider")
	run(consumer, false, call...)
	run(provider, true, "package", "trust", "example/provider")
	stop(syscall.SIGTERM)
	if len(listPeers()) != 0 {
		t.Fatal("provider registration survived graceful stop")
	}

	stop = start()
	stop(os.Kill)
	run(consumer, false, call...)
	deadline := time.Now().Add(5 * time.Second)
	for len(listPeers()) > 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if len(listPeers()) != 0 {
		t.Fatal("crashed provider registration was still discoverable after lease expiry")
	}
	stop = start()
	if out := run(consumer, true, call...); !strings.Contains(out, "from-provider") {
		t.Fatalf("peer restart result: %s", out)
	}
	stop(syscall.SIGTERM)
}

func checkPeerRoute(t *testing.T, path string, provider ledger.VertexRef) {
	t.Helper()
	lib, err := library.Open(os.Getenv("EXT_LEDGER_LIBRARY"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ledger.Bind(lib)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := binding.OpenStore(ctx, "reader", path, ledger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	changes, err := db.Changes(ctx, "", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		if !strings.HasPrefix(change.Namespace, "ext.execution.") || change.Namespace == provider.Namespace {
			continue
		}
		for _, vertex := range change.Vertices {
			ref, _ := vertex.Attributes["reference"].(map[string]any)
			if ref["namespace"] != provider.Namespace || ref["id"] != provider.ID {
				continue
			}
			for _, edge := range change.Edges {
				if edge.To == vertex.ID && strings.HasSuffix(edge.Type, "/routes-to") {
					return
				}
			}
		}
	}
	t.Fatalf("consumer graph did not link to provider session %+v", provider)
}
