package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/home"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
	"github.com/webong/ext/src/ctx/system/capture/surface"
)

func TestSurfaceBackendRequiresTrustedInstalledGuest(t *testing.T) {
	binary := os.Getenv("EXT_SURFACE_GUEST_BINARY")
	if binary == "" || runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("set EXT_SURFACE_GUEST_BINARY to a native surface resource guest")
	}
	t.Setenv("EXT_HOME", t.TempDir())
	id := "system.surface." + runtime.GOOS
	packageDir := filepath.Join(t.TempDir(), "surface-package")
	_, err := store.SealExecutable(store.ExecutablePackage{
		Descriptor: plugin.Descriptor{
			APIVersion: plugin.APIVersion,
			Identity:   plugin.Identity{ID: id, Version: "1.0.0"},
			Contracts: []plugin.Contract{{
				ContractRef: surfaceGuestContract,
				Operations: []plugin.Operation{
					{Name: "capture", Surface: "observation"},
					{Name: "input", Surface: "action"},
				},
			}},
		},
		Protocol: surfaceGuestProtocol, Binary: binary, OutputDir: packageDir,
		OS: runtime.GOOS, Arch: runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.NewWithLayout(home.Packages(), store.Limits{}, storelayout.Ext())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := openSurfaceBackend(context.Background()); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("untrusted surface guest = %v", err)
	}
	digest, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	file, err := trust.New(filepath.Join(home.Packages(), ".package-trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Record("ext.command/v1", id, digest); err != nil {
		t.Fatal(err)
	}
	backend, closeBackend, err := openSurfaceBackend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeBackend()
	request, err := surface.NewCaptureRequest(surface.WindowInfo{
		ID: "window:missing", PID: 1, ProcessStartID: "fixture",
		Bounds: surface.SurfaceBounds{Width: 1, Height: 1},
	}, surface.CaptureOptions{MaxPixels: 1, MaxBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Capture(context.Background(), request); err == nil {
		t.Fatal("guest accepted a nonexistent window")
	}
}
