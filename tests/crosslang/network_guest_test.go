package crosslang_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	contract "github.com/webong/ext/pkg/ext-go/engine/plugin"
	hostprocess "github.com/webong/ext/pkg/ext-go/engine/plugin/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
)

func TestNetworkResourceGuest(t *testing.T) {
	binary := os.Getenv("EXT_NETWORK_GUEST_BINARY")
	if binary == "" {
		t.Skip("set EXT_NETWORK_GUEST_BINARY to a published network resource guest")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("native network resource guests run on Windows and Linux")
	}
	id := "system.network." + runtime.GOOS
	ref := contract.ContractRef{Name: "ext.system.network", Version: "v1"}
	packageDir := filepath.Join(t.TempDir(), "network-package")
	_, err := store.SealExecutable(store.ExecutablePackage{
		Descriptor: contract.Descriptor{
			APIVersion: contract.APIVersion,
			Identity:   contract.Identity{ID: id, Version: "1.0.0"},
			Contracts: []contract.Contract{{
				ContractRef: ref,
				Operations:  []contract.Operation{{Name: "observe", Surface: "observation"}},
			}},
		},
		Protocol: "ext.system.network/v1", Binary: binary, OutputDir: packageDir,
		OS: runtime.GOOS, Arch: runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.NewWithLayout(t.TempDir(), store.Limits{}, storelayout.Ext())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.ResolveEntrypoint(installed.Name(), ref.Name, ref.Version,
		"ext.system.network/v1", digest)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := plugin.Open(ctx, selected.Package.Manifest.Descriptor, contract.Options{
		Verify: func(_ context.Context, _ contract.Descriptor) error {
			_, err := s.ResolveEntrypoint(installed.Name(), ref.Name, ref.Version,
				"ext.system.network/v1", digest)
			return err
		},
		Connect: func(ctx context.Context, _ contract.Descriptor) (contract.Backend, error) {
			return hostprocess.Start(ctx, hostprocess.Command{Path: selected.Path, Args: []string{"run"}, Env: os.Environ()})
		},
		Authorize: func(ctx context.Context, _ contract.Request) error { return ctx.Err() },
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	output, err := session.Call(ctx, ref, "observe", json.RawMessage("{\"max_interfaces\":256,\"max_addresses\":256,\"max_routes\":4096,\"timeout_ms\":5000}"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(output, &snapshot); err != nil {
		t.Fatal(err)
	}
	var platform string
	var interfaces []any
	var routeStatus map[string]any
	if json.Unmarshal(snapshot["platform"], &platform) != nil ||
		json.Unmarshal(snapshot["interfaces"], &interfaces) != nil ||
		json.Unmarshal(snapshot["route_status"], &routeStatus) != nil ||
		platform != runtime.GOOS || interfaces == nil || routeStatus["state"] == nil {
		t.Fatalf("invalid native network observation: %s", output)
	}
	if runtime.GOOS == "windows" && routeStatus["state"] != "unsupported" {
		t.Fatalf("Windows must report route observation as unsupported: %s", output)
	}
}
