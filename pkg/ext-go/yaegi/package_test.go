package yaegi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest"
	"github.com/webong/ext/pkg/ext-go/yaegi"
)

// install seals the conformance guest as a package whose go-source
// entrypoint names a tree, installs it and returns it.
func install(t *testing.T) store.Installed {
	t.Helper()
	source := t.TempDir()
	guest := filepath.Join(source, "guest")
	if err := os.Mkdir(guest, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range conformanceSource(t) {
		if err := os.WriteFile(filepath.Join(guest, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := packagekit.TreeDigest(source, "guest")
	if err != nil {
		t.Fatal(err)
	}
	m := packagekit.Manifest{
		APIVersion:  packagekit.Version,
		Descriptor:  plugintest.Descriptor(),
		Artifacts:   []packagekit.Artifact{{Name: "guest", Path: "guest", Kind: packagekit.ArtifactTree, SHA256: digest}},
		Entrypoints: []packagekit.Entrypoint{{Name: "main", Runtime: yaegi.Runtime, Artifact: "guest", Protocols: []string{plugin.APIVersion}}},
	}
	if err := store.WriteManifest(source, m); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(t.TempDir(), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	return installed
}

func TestOpenPackage(t *testing.T) {
	installed := install(t)
	verified := 0
	session, err := openPackage(context.Background(), installed, "", yaegi.Options{}, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { verified++; return nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	if verified != 1 {
		t.Fatalf("caller verify ran %d times", verified)
	}
	ref := plugintest.Descriptor().Contracts[0].ContractRef
	got, err := session.Call(context.Background(), ref, "echo", json.RawMessage(`{"value":7}`))
	if err != nil || string(got) != `{"value":7}` {
		t.Fatalf("echo: %s %v", got, err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The installed package is re-verified before anything is evaluated.
	if err := os.WriteFile(filepath.Join(installed.Directory, "guest", "guest.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = openPackage(context.Background(), installed, "main", yaegi.Options{}, plugin.Options{
		Verify: func(context.Context, plugin.Descriptor) error {
			t.Fatal("caller verify ran on a changed package")
			return nil
		},
		Authorize: func(context.Context, plugin.Request) error { return nil },
	})
	if err == nil {
		t.Fatal("changed package opened")
	}
}

func TestOpenPackageSelectsGoSource(t *testing.T) {
	installed := install(t)
	if _, err := openPackage(context.Background(), installed, "other", yaegi.Options{}, plugin.Options{}); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("missing entrypoint: %v", err)
	}
	installed.Manifest.Entrypoints[0].Runtime = "process"
	if _, err := openPackage(context.Background(), installed, "", yaegi.Options{}, plugin.Options{}); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("no go-source entrypoint: %v", err)
	}
}

// The fixture oracle is chosen by the test; the backend no longer owns a host.
func openPackage(ctx context.Context, p store.Installed, entry string, o yaegi.Options, session plugin.Options) (*plugin.Session, error) {
	opts, err := yaegi.PackageOptions(p, entry, o, session)
	if err != nil {
		return nil, err
	}
	return plugin.Open(ctx, p.Manifest.Descriptor, opts)
}
