package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/ctx/internal/config"
)

// An adapter with a runtime this host does not define is installable and
// trustable but must never be executed by the host's own loops.
func TestInertAdapterIsNeverExecutedByInventoryOrDoctor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	t.Setenv("CTX_ADAPTER_HOME", filepath.Join(root, "state", "adapters"))
	log := filepath.Join(root, "invoked.log")
	source := filepath.Join(root, "src")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version = "2.0"
name = "zzz"
runtime = "futureruntime"
surfaces = "shell"
executable = "ctx-zzz"
capabilities = "list,observe,validate,run,doctor"
selectable = "false"
`
	script := "#!/bin/sh\necho \"$1\" >> " + log + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(source, "adapter.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "ctx-zzz"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	store := adapterStore()
	installed, err := store.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(installed); err != nil {
		t.Fatal(err)
	}
	if installed.IsKnownRuntime() {
		t.Fatal("the probe's runtime must be unknown to this host")
	}
	resolver, err := config.NewResolver(root, root, filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	observations, err := scanMachineInventory(resolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, observation := range observations {
		if observation.Name == "zzz" {
			found = true
			if observation.DiscoveryStatus != "inert" || observation.Listed {
				t.Errorf("an inert adapter is reported as %q, listed=%v", observation.DiscoveryStatus, observation.Listed)
			}
		}
	}
	if !found {
		t.Error("an inert adapter must still be visible in the inventory")
	}
	var out, errOut bytes.Buffer
	doctor(resolver, &out, &errOut)
	if data, err := os.ReadFile(log); err == nil {
		t.Fatalf("the inert adapter's executable was run: %q", data)
	}
	if bytes.Contains(out.Bytes(), []byte("zzz")) {
		t.Errorf("doctor mentions the inert adapter: %s", out.String())
	}
}
