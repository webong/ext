package mod

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ctx/pkg/adapter"
	"github.com/webong/ctx/pkg/plugin"
)

func TestAdapterUsesSharedPluginSelection(t *testing.T) {
	dir := fixtureAdapter(t, t.TempDir(), "example", "computer")
	a, err := LoadDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.PluginDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	ref := plugin.ContractRef{Name: adapter.PluginContractName, Version: adapter.APIVersion}
	if _, err := plugin.Select([]plugin.Descriptor{d}, plugin.Requirement{Contract: ref, Operation: "run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.Select([]plugin.Descriptor{d}, plugin.Requirement{Contract: ref, Operation: "remove"}); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatal(err)
	}
	command, err := a.Command(Invocation{Operation: "run", Selection: "local", Arguments: []string{"--version"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{a.ExecutablePath(), "run", "local", "--", "--version"}
	if len(command.Args) != len(want) {
		t.Fatal(command.Args)
	}
	for i := range want {
		if command.Args[i] != want[i] {
			t.Fatal(command.Args)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "asset"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := a.PluginDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if d.Identity.Revision == changed.Identity.Revision {
		t.Fatal("content changes did not change identity")
	}
}

func TestAdapterImplicitCapabilitiesStayInBinding(t *testing.T) {
	a := &Adapter{Manifest: Manifest{Name: "example", Runtime: "computer", Capabilities: []string{"run"}, ComputerCapabilities: []string{"hook", "plugin"}}}
	for _, op := range []string{"run", "hook", "plugin"} {
		if !a.HasCapability(op) {
			t.Fatal(op)
		}
	}
	a.Manifest.Runtime = "browser"
	a.Manifest.BrowserManagement = []string{"extension.prepare"}
	if !a.HasCapability("manage") {
		t.Fatal("missing declared management capability")
	}
}
