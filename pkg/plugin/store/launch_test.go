package store_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/packagekit"
	"github.com/webong/ext/pkg/plugin/process"
	"github.com/webong/ext/pkg/plugin/store"
)

var launchDescriptor = plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/exec", Revision: "r1"},
	Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}}}}}

func TestMain(m *testing.M) {
	if os.Getenv("STORE_TEST_CHILD") == "1" {
		g, err := plugin.NewGuest(launchDescriptor, plugin.GuestOptions{Handler: func(_ context.Context, r plugin.Request) (json.RawMessage, error) { return r.Payload, nil }})
		if err != nil || jsonline.ServeStdio(context.Background(), g) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// An installed entrypoint artifact must be executable; other artifacts must not be.
func TestInstalledEntrypointLaunches(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	provider := "bin/provider"
	if runtime.GOOS == "windows" {
		provider += ".exe" // Windows only executes files with an executable extension.
	}
	bin := write(t, src, provider, string(body))
	data := write(t, src, "data/readme.txt", "doc")
	m := packagekit.Manifest{APIVersion: packagekit.Version, Descriptor: launchDescriptor,
		Artifacts:   []packagekit.Artifact{{Name: "provider", Path: provider, SHA256: bin}, {Name: "doc", Path: "data/readme.txt", SHA256: data}},
		Entrypoints: []packagekit.Entrypoint{{Name: "default", Runtime: "jsonline", Artifact: "provider", Protocols: []string{plugin.APIVersion}}}}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(src, store.ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := newStore(t).Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc, _ := p.ArtifactPath("doc"); doc != "" {
		if info, _ := os.Stat(doc); info.Mode().Perm()&0o111 != 0 && runtime.GOOS != "windows" {
			t.Fatalf("non-entrypoint artifact is executable: %v", info.Mode())
		}
	}
	path, err := p.ArtifactPath("provider")
	if err != nil {
		t.Fatal(err)
	}
	proc, err := process.Start(context.Background(), process.Command{Path: path, Dir: p.Directory, Env: []string{"STORE_TEST_CHILD=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Close()
	s, err := plugin.Open(context.Background(), launchDescriptor, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return p.Verify() },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return proc, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := s.Call(ctx, launchDescriptor.Contracts[0].ContractRef, "echo", json.RawMessage(`"hi"`))
	if err != nil || string(out) != `"hi"` {
		t.Fatal(string(out), err)
	}
}
