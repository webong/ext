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
	"github.com/webong/ext/src/ctx/system/capture/surface"
)

func TestSurfaceResourceGuest(t *testing.T) {
	binary := os.Getenv("EXT_SURFACE_GUEST_BINARY")
	if binary == "" {
		t.Skip("set EXT_SURFACE_GUEST_BINARY to a native surface resource guest")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native surface resource guests run on macOS and Windows")
	}
	id := "system.surface." + runtime.GOOS
	ref := contract.ContractRef{Name: "ext.system.surface", Version: "v1"}
	packageDir := filepath.Join(t.TempDir(), "surface-package")
	_, err := store.SealExecutable(store.ExecutablePackage{
		Descriptor: contract.Descriptor{
			APIVersion: contract.APIVersion,
			Identity:   contract.Identity{ID: id, Version: "1.0.0"},
			Contracts: []contract.Contract{{
				ContractRef: ref,
				Operations: []contract.Operation{
					{Name: "capture", Surface: "observation"},
					{Name: "input", Surface: "action"},
				},
			}},
		},
		Protocol: surface.ControlProtocol, Binary: binary, OutputDir: packageDir,
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
	selected, err := s.ResolveEntrypoint(id, ref.Name, ref.Version, surface.ControlProtocol, digest)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := plugin.Open(ctx, selected.Package.Manifest.Descriptor, contract.Options{
		Verify: func(_ context.Context, _ contract.Descriptor) error {
			_, err := s.ResolveEntrypoint(id, ref.Name, ref.Version, surface.ControlProtocol, digest)
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
	data, err := os.ReadFile(filepath.Join("..", "fixtures", "system", "surface", "v1", "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Capture json.RawMessage `json:"capture"`
		Input   json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for operation, input := range map[string]json.RawMessage{"capture": cases.Capture, "input": cases.Input} {
		_, err := session.Call(ctx, ref, operation, input)
		if err == nil {
			t.Errorf("%s accepted a nonexistent window", operation)
		}
	}
}
