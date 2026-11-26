//go:build windows

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/home"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
)

func TestWindowsClipboardGuestHandshake(t *testing.T) {
	binary := os.Getenv("EXT_CLIPBOARD_GUEST_BINARY")
	if binary == "" {
		t.Skip("set EXT_CLIPBOARD_GUEST_BINARY to the published single-file guest")
	}
	t.Setenv("EXT_HOME", t.TempDir())
	packageDir := filepath.Join(t.TempDir(), "clipboard-package")
	_, err := store.SealExecutable(store.ExecutablePackage{
		Descriptor: plugin.Descriptor{
			APIVersion: plugin.APIVersion,
			Identity:   plugin.Identity{ID: clipboardGuestID, Version: "1.0.0"},
			Contracts: []plugin.Contract{{
				ContractRef: clipboardContract,
				Operations: []plugin.Operation{
					{Name: "status", Surface: "observation"},
					{Name: "generation", Surface: "observation"},
					{Name: "read", Surface: "observation"},
					{Name: "write", Surface: "action"},
					{Name: "clear", Surface: "action"},
				},
			}},
		},
		Protocol: clipboardProtocol, Binary: binary, OutputDir: packageDir,
		OS: "windows", Arch: "amd64",
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
	digest, err := installed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	file, err := trust.New(filepath.Join(home.Packages(), ".package-trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Record(clipboardTrustScope, clipboardGuestID, digest); err != nil {
		t.Fatal(err)
	}
	backend, closeBackend, err := openClipboardBackend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if backend == nil {
		t.Fatal("guest handshake returned no backend")
	}
	closeBackend()
}
