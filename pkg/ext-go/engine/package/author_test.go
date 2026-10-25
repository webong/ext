package packagekit_test

import (
	"encoding/json"
	"errors"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/package"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	manifestPath := filepath.Join(dir, "plugin.json")
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

func TestSourceManifestTreeArtifact(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"provider.py": "x", "vendor/LICENSE": "l", "vendor/Pkg/METADATA": "m"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, []byte(body), 0o600)
	}
	m, err := packagekit.SourceManifest("example/t", "", contracts(), root, []string{"vendor", "provider.py"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 2 || m.Artifacts[0].Kind != "" || m.Artifacts[1].Kind != packagekit.ArtifactTree || m.Artifacts[1].Path != "vendor" {
		t.Fatalf("artifacts: %+v", m.Artifacts)
	}
	digest, err := packagekit.TreeDigest(root, "vendor")
	if err != nil || digest != m.Artifacts[1].SHA256 {
		t.Fatalf("digest: %q %v", digest, err)
	}
	if err := m.VerifyArtifacts(root); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "vendor", "LICENSE"), []byte("changed"), 0o600)
	if err := m.VerifyArtifacts(root); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatalf("tamper: %v", err)
	}
	// A manifest with a tree survives JSON and keeps its kind.
	raw, _ := jsonMarshal(m)
	back, err := packagekit.Decode(raw)
	if err != nil || back.Artifacts[1].Kind != packagekit.ArtifactTree {
		t.Fatalf("round trip: %+v %v", back.Artifacts, err)
	}
}

func TestTreeRulesRejected(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "vendor", "sub"), 0o700)
	os.MkdirAll(filepath.Join(root, "empty"), 0o700)
	os.WriteFile(filepath.Join(root, "vendor", "a"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(root, "vendor", "sub", "b"), []byte("b"), 0o600)
	os.WriteFile(filepath.Join(root, "main.py"), []byte("x"), 0o600)
	if _, err := packagekit.SourceManifest("example/t", "", contracts(), root, []string{"empty"}); err == nil {
		t.Fatal("empty tree accepted")
	}
	if _, err := packagekit.SourceManifest("example/t", "", contracts(), root, []string{"vendor", "vendor/a"}); err == nil {
		t.Fatal("file inside a listed tree accepted")
	}
	if _, err := packagekit.SourceManifest("example/t", "", contracts(), root, []string{"vendor", "vendor/sub"}); err == nil {
		t.Fatal("nested trees accepted")
	}
	if _, err := packagekit.TreeDigest(root, "main.py"); err == nil {
		t.Fatal("file accepted as a tree")
	}
	if _, err := packagekit.TreeDigest(root, "../escape"); err == nil {
		t.Fatal("escaping tree accepted")
	}
	if runtime.GOOS != "windows" {
		os.Symlink(filepath.Join(root, "vendor"), filepath.Join(root, "link"))
		if _, err := packagekit.TreeDigest(root, "link"); err == nil {
			t.Fatal("symlinked tree accepted")
		}
		os.Symlink(filepath.Join(root, "main.py"), filepath.Join(root, "vendor", "sym"))
		if _, err := packagekit.TreeDigest(root, "vendor"); err == nil {
			t.Fatal("tree with a symlink accepted")
		}
	}
	m, err := packagekit.SourceManifest("example/t", "", contracts(), root, []string{"main.py"})
	if err != nil {
		t.Fatal(err)
	}
	// An entrypoint cannot name a tree.
	os.Remove(filepath.Join(root, "vendor", "sym"))
	tree, _ := packagekit.TreeDigest(root, "vendor")
	m.Artifacts = append(m.Artifacts, packagekit.Artifact{Name: "vendor", Path: "vendor", Kind: packagekit.ArtifactTree, SHA256: tree})
	m.Entrypoints = []packagekit.Entrypoint{{Name: "main", Runtime: "process", Artifact: "vendor", Protocols: []string{plugin.APIVersion}}}
	if err := m.Validate(); err == nil {
		t.Fatal("tree entrypoint accepted")
	}
	m.Entrypoints = nil
	m.Artifacts[len(m.Artifacts)-1].Kind = "bundle"
	if err := m.Validate(); err == nil {
		t.Fatal("unknown artifact kind accepted")
	}
}

func TestWithReverseDeclaresHostOperations(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	contracts := []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "work", Version: "v1"}, Operations: []plugin.Operation{{Name: "run", Progress: true}}}}
	m, err := packagekit.ExecutableManifest("p", "1", contracts, exe)
	if err != nil {
		t.Fatal(err)
	}
	reverse := []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "host.svc", Version: "v1"}, Operations: []plugin.Operation{{Name: "sign"}}}}
	withReverse, err := m.WithReverse(reverse)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Descriptor.Reverse) != 0 {
		t.Fatal("WithReverse changed its receiver")
	}
	if _, err := withReverse.Descriptor.LookupReverse(reverse[0].ContractRef, "sign"); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(withReverse)
	if err != nil || !strings.Contains(string(data), `"reverse":[`) || !strings.Contains(string(data), `"progress":true`) {
		t.Fatalf("manifest JSON must carry both declarations: %s %v", data, err)
	}
	bad := []plugin.Contract{{ContractRef: reverse[0].ContractRef}}
	if _, err := m.WithReverse(bad); err == nil {
		t.Fatal("a reverse contract without operations was accepted")
	}
}
