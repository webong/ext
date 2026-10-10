package packagekit_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/packagekit"
)

func contracts() []plugin.Contract {
	return []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}}}}
}

func TestExecutableManifest(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "provider")
	if err := os.WriteFile(exe, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := packagekit.ExecutableManifest("example/p", "1.2.3", contracts(), exe)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := packagekit.FileDigest(exe)
	if m.Name() != "example/p" || m.Version() != "1.2.3" || m.Descriptor.Identity.Revision != "sha256:"+digest {
		t.Fatalf("identity: %+v", m.Descriptor.Identity)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Name != "executable" || m.Artifacts[0].Path != "provider" || m.Artifacts[0].OS != runtime.GOOS || m.Artifacts[0].Arch != runtime.GOARCH {
		t.Fatalf("artifacts: %+v", m.Artifacts)
	}
	if len(m.Entrypoints) != 1 || m.Entrypoints[0].Name != "main" || m.Entrypoints[0].Runtime != packagekit.ExecutableRuntime || m.Entrypoints[0].Protocols[0] != plugin.APIVersion {
		t.Fatalf("entrypoints: %+v", m.Entrypoints)
	}
	if !m.Supports("example.echo", "v1") || m.Supports("example.echo", "v2") || m.Supports("other", "v1") || m.SourceOnly() {
		t.Fatal("Supports/SourceOnly")
	}
	// A rebuild is a new revision.
	if err := os.WriteFile(exe, []byte("binary2"), 0o700); err != nil {
		t.Fatal(err)
	}
	m2, err := packagekit.ExecutableManifest("example/p", "1.2.3", contracts(), exe)
	if err != nil || m2.Descriptor.Identity.Revision == m.Descriptor.Identity.Revision {
		t.Fatalf("revision did not change: %v", err)
	}
	// The input contracts are not aliased by the manifest.
	in := contracts()
	m3, _ := packagekit.ExecutableManifest("example/p", "", in, exe)
	in[0].Operations[0].Name = "changed"
	if m3.Descriptor.Contracts[0].Operations[0].Name != "echo" {
		t.Fatal("manifest aliases caller contracts")
	}
}

func TestExecutableManifestRejects(t *testing.T) {
	dir := t.TempDir()
	if _, err := packagekit.ExecutableManifest("example/p", "", contracts(), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("accepted a missing executable")
	}
	if _, err := packagekit.ExecutableManifest("example/p", "", contracts(), dir); err == nil {
		t.Fatal("accepted a directory")
	}
	exe := filepath.Join(dir, "provider")
	os.WriteFile(exe, []byte("x"), 0o700)
	if _, err := packagekit.ExecutableManifest("Bad ID", "", contracts(), exe); err == nil {
		t.Fatal("accepted an invalid ID")
	}
	if _, err := packagekit.ExecutableManifest("example/p", "", nil, exe); err == nil {
		t.Fatal("accepted no contracts")
	}
}

func TestSourceManifest(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"provider.py": "print(1)", "lib/util.py": "x = 1"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, []byte(body), 0o600)
	}
	a, err := packagekit.SourceManifest("example/src", "0.1.0", contracts(), root, []string{"provider.py", "lib/util.py"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := packagekit.SourceManifest("example/src", "0.1.0", contracts(), root, []string{"lib/util.py", "provider.py"})
	if err != nil || a.Descriptor.Identity.Revision != b.Descriptor.Identity.Revision {
		t.Fatalf("revision must not depend on file order: %v", err)
	}
	if !a.SourceOnly() || len(a.Artifacts) != 2 || a.Artifacts[0].Name != a.Artifacts[0].Path || !strings.HasPrefix(a.Descriptor.Identity.Revision, "sha256:") {
		t.Fatalf("manifest: %+v", a)
	}
	os.WriteFile(filepath.Join(root, "provider.py"), []byte("print(2)"), 0o600)
	c, _ := packagekit.SourceManifest("example/src", "0.1.0", contracts(), root, []string{"provider.py", "lib/util.py"})
	if c.Descriptor.Identity.Revision == a.Descriptor.Identity.Revision {
		t.Fatal("content change did not change the revision")
	}
	for name, files := range map[string][]string{"none": nil, "duplicate": {"provider.py", "provider.py"}, "escape": {"../x"}, "absolute": {"/etc/passwd"}, "missing": {"nope.py"}, "backslash": {`lib\util.py`}} {
		if _, err := packagekit.SourceManifest("example/src", "", contracts(), root, files); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestLoadDescriptorFromManifestOrBareFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "provider")
	os.WriteFile(exe, []byte("x"), 0o700)
	m, _ := packagekit.ExecutableManifest("example/p", "", contracts(), exe)
	raw, _ := jsonMarshal(m)
	manifestPath := filepath.Join(dir, "package.json")
	os.WriteFile(manifestPath, raw, 0o600)
	d, err := packagekit.LoadDescriptor(manifestPath)
	if err != nil || d.Identity != m.Descriptor.Identity {
		t.Fatalf("manifest: %+v %v", d, err)
	}
	bare, _ := jsonMarshal(m.Descriptor)
	barePath := filepath.Join(dir, "descriptor.json")
	os.WriteFile(barePath, bare, 0o600)
	d, err = packagekit.LoadDescriptor(barePath)
	if err != nil || d.Identity != m.Descriptor.Identity {
		t.Fatalf("bare: %+v %v", d, err)
	}
	os.WriteFile(barePath, []byte(`{"apiVersion":"ext.plugin/v1","extra":1}`), 0o600)
	if _, err := packagekit.LoadDescriptor(barePath); err == nil {
		t.Fatal("accepted an invalid descriptor")
	}
	if _, err := packagekit.LoadDescriptor(filepath.Join(dir, "none.json")); err == nil {
		t.Fatal("accepted a missing file")
	}
}
