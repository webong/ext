//go:build ext_cengine && cgo && (darwin || linux)

package goengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/interop"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/packagekit"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func packageFixture(id string) packagekit.Manifest {
	sum := sha256.Sum256([]byte("payload"))
	return packagekit.Manifest{APIVersion: packagekit.Version, Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: id, Revision: "r1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: id, Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}}, Artifacts: []packagekit.Artifact{{Name: "source", Path: "source.txt", SHA256: hex.EncodeToString(sum[:])}}, Entrypoints: []packagekit.Entrypoint{{Name: "default", Runtime: "jsonline", Artifact: "source", Protocols: []string{plugin.APIVersion}}}}
}
func sameError(t *testing.T, got, want error) {
	t.Helper()
	for _, class := range []error{nil, plugin.ErrInvalid, plugin.ErrUnsupported, plugin.ErrMismatch, plugin.ErrNotFound, plugin.ErrAmbiguous} {
		if errors.Is(got, class) != errors.Is(want, class) {
			t.Fatalf("C %v, Go %v", got, want)
		}
	}
}
func TestSharedSHA256(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, size := range []int{0, 1, 2, 3, 55, 56, 63, 64, 65, 127, 128, 1000, 1000000} {
		b := make([]byte, size)
		r.Read(b)
		if got, want := SHA256(b), sha256.Sum256(b); got != want {
			t.Fatalf("length %d: %x != %x", size, got, want)
		}
	}
}
func TestPackageDifferential(t *testing.T) {
	for _, path := range []string{"source.txt", "dir/source.txt", "é/File", ".", "..", "../escape", "/absolute", "a\\b", "a:stream", "a/../b", "a\n", "CON", "cOn.txt", "foo/LPT9.x", "foo/com0.x", "a//b", "a/", "a.", "a ", "a\x00b", strings.Repeat("a", 1025)} {
		m := packageFixture("example/a")
		m.Artifacts[0].Path = path
		sameError(t, ValidateManifest(m), m.Validate())
	}
	for _, paths := range [][2]string{{"File", "file"}, {"É", "é"}, {"İ", "i"}, {"Σ", "σ"}, {"Σ", "ς"}, {"Kelvin/K", "kelvin/k"}} {
		m := packageFixture("example/a")
		m.Artifacts[0].Path = paths[0]
		a := m.Artifacts[0]
		a.Name = "other"
		a.Path = paths[1]
		m.Artifacts = append(m.Artifacts, a)
		sameError(t, ValidateManifest(m), m.Validate())
	}
	a, b := packageFixture("example/a"), packageFixture("example/b")
	a.Requires = []packagekit.Dependency{{Contract: b.Descriptor.Contracts[0].ContractRef, Operation: "read"}}
	for _, ms := range [][]packagekit.Manifest{nil, {}, {a, b}, {b, a}, {a}, {b, b}} {
		got, ge := ResolvePackages(ms)
		want, we := packagekit.Resolve(ms)
		sameError(t, ge, we)
		if ge == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("C %#v, Go %#v", got, want)
		}
	}
	b.Requires = []packagekit.Dependency{{Contract: a.Descriptor.Contracts[0].ContractRef, Operation: "read"}}
	_, ge := ResolvePackages([]packagekit.Manifest{a, b})
	_, we := packagekit.Resolve([]packagekit.Manifest{a, b})
	sameError(t, ge, we)
	m := packageFixture("example/a")
	env := packagekit.Environment{Runtimes: map[string]plugin.BackendProfile{"jsonline": jsonline.Profile()}}
	check := func() {
		t.Helper()
		got, ge := SelectEntrypoint(m, "default", env)
		want, we := m.SelectEntrypoint("default", env)
		sameError(t, ge, we)
		if ge == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("C %#v Go %#v", got, want)
		}
	}
	check()
	m.SharedDependencies = []packagekit.SharedDependency{{Name: "ui", Versions: []string{"v1"}}}
	check()
	env.SharedVersions = map[string]string{"ui": "v1"}
	check()
	m.Artifacts[0].OS = "linux"
	check()
	env.OS = "linux"
	check()
	env.Runtimes = nil
	check()
}
func TestRouteDifferential(t *testing.T) {
	h := jsonline.Profile()
	g := h
	g.Name = "hashicorp-grpc"
	bridge := interop.Bridge{Name: "ext-bridge", Frontend: h, Backend: g}
	for _, guests := range [][]plugin.BackendProfile{{h}, {g}, {h, g}, nil, {g, g}} {
		for _, bridges := range [][]interop.Bridge{nil, {bridge}, {bridge, bridge}} {
			for _, w := range []interop.Requirements{{}, {Concurrent: true}, {NativeCallbacks: true}, {NativeStreaming: true}} {
				got, ge := ResolveRoute([]plugin.BackendProfile{h}, guests, bridges, w)
				want, we := interop.Resolve([]plugin.BackendProfile{h}, guests, bridges, w)
				sameError(t, ge, we)
				if ge == nil && !reflect.DeepEqual(got, want) {
					t.Fatalf("C %#v, Go %#v", got, want)
				}
			}
		}
	}
}
func TestSchemaIntegerLimits(t *testing.T) {
	for _, n := range []string{"1.0", "1e2", "9223372036854775808", "-1"} {
		if _, err := EngineCall("schema.validate", json.RawMessage(`{"type":"string","maxLength":`+n+`}`)); !errors.Is(err, plugin.ErrInvalid) {
			t.Fatalf("%s: %v", n, err)
		}
	}
}
func TestIntegrityDifferential(t *testing.T) {
	root := t.TempDir()
	m := packageFixture("example/a")
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		sameError(t, VerifyArtifacts(m, root), m.VerifyArtifacts(root))
		got, ge := DirectoryDigest(root)
		want, we := plugin.DirectoryDigest(root)
		sameError(t, ge, we)
		if ge == nil && got != want {
			t.Fatalf("%s != %s", got, want)
		}
	}
	write("source.txt", "payload")
	check()
	write("source.txt", "changed")
	check()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	write("sub/💡", "other")
	check()
	if err := os.Symlink(filepath.Join(root, "source.txt"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	check()
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	write("newline\n", "invalid")
	check()
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(root, linked); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{linked, linked + "/"} {
		if _, err := DirectoryDigest(p); !errors.Is(err, plugin.ErrInvalid) {
			t.Fatalf("symlink root %s: %v", p, err)
		}
	}
}

// manifestCase is one manifest and whether it must be valid. C and Go must agree
// with each other and with the expectation, so neither can drift unnoticed.
type manifestCase struct {
	name  string
	build func(m *packagekit.Manifest)
	valid bool
}

func metadataOf(entries map[string]string) func(m *packagekit.Manifest) {
	return func(m *packagekit.Manifest) {
		m.Metadata = map[string]json.RawMessage{}
		for k, v := range entries {
			m.Metadata[k] = json.RawMessage(v)
		}
	}
}

// padded returns a metadata object whose weight is exactly weight: the key "a"
// (1) plus the object (2) plus a string value of the remaining length.
func padded(weight int) string { return `{"a":"` + strings.Repeat("x", weight-3) + `"}` }

func TestManifestExtensionsDifferential(t *testing.T) {
	tree := func(name, path string) packagekit.Artifact {
		return packagekit.Artifact{Name: name, Path: path, Kind: packagekit.ArtifactTree, SHA256: strings.Repeat("a", 64)}
	}
	file := func(name, path string) packagekit.Artifact {
		return packagekit.Artifact{Name: name, Path: path, SHA256: strings.Repeat("b", 64)}
	}
	withArtifacts := func(extra ...packagekit.Artifact) func(m *packagekit.Manifest) {
		return func(m *packagekit.Manifest) { m.Artifacts = append(m.Artifacts, extra...) }
	}
	var cases []manifestCase
	add := func(name string, valid bool, build func(m *packagekit.Manifest)) {
		cases = append(cases, manifestCase{name, build, valid})
	}

	for _, l := range []string{"", "MIT", "Apache-2.0 OR MIT", "(MIT OR Apache-2.0)", "GPL-2.0+", "a", strings.Repeat("a", 128)} {
		l := l
		add("license "+l, true, func(m *packagekit.Manifest) { m.License = l })
	}
	for _, l := range []string{" MIT", "MIT ", "MIT  OR  Apache-2.0", "MIT;x", "MIT\n", "-MIT", ")MIT", "+MIT", ".MIT", "é", "MIT_2", strings.Repeat("a", 129)} {
		l := l
		add("license "+strings.TrimSpace(l), false, func(m *packagekit.Manifest) { m.License = l })
	}

	for _, k := range []string{"kvlite.module/v1", "a/v1", "0a/v12", "a.b-c_d/v123456789", strings.Repeat("a", 128) + "/v1"} {
		add("key "+k[:min(len(k), 24)], true, metadataOf(map[string]string{k: `{}`}))
	}
	for _, k := range []string{"kvlite", "KVLite/v1", "a/v0", "a/v1x", "a/v1234567890", "/v1", "a//v1", "a/b/v1", "a/v", "-a/v1", ".a/v1", strings.Repeat("a", 129) + "/v1"} {
		add("bad key "+k[:min(len(k), 24)], false, metadataOf(map[string]string{k: `{}`}))
	}

	for _, v := range []string{`{}`, `{"a":1}`, ` {} `, `{"a":{"b":[1,true,null,"s"]}}`, `{"a":"é"}`, `{"a":"é"}`, `{"a":1e400}`} {
		add("value "+v, true, metadataOf(map[string]string{"a.b/v1": v}))
	}
	for _, v := range []string{`[]`, `"s"`, `null`, `1`, `true`} {
		add("bad value "+v, false, metadataOf(map[string]string{"a.b/v1": v}))
	}

	// Size is a weight, so a string of escapes weighs its decoded bytes, not its spelling.
	add("weight at limit", true, metadataOf(map[string]string{"a.b/v1": padded(64 << 10)}))
	add("weight over limit", false, metadataOf(map[string]string{"a.b/v1": padded(64<<10 + 1)}))
	// Each \u00e9 escape is 6 bytes spelled but 2 decoded, so weight ignores the spelling.
	escaped := func(n int) string { return `{"a":"` + strings.Repeat(`\u00e9`, n) + `"}` }
	add("escaped spelling does not count", true, metadataOf(map[string]string{"a.b/v1": escaped(32<<10 - 2)}))
	add("escaped weight over limit", false, metadataOf(map[string]string{"a.b/v1": escaped(32<<10 - 1)}))
	four := func(last int) func(m *packagekit.Manifest) {
		return metadataOf(map[string]string{"k0/v1": padded(65531), "k1/v1": padded(65531), "k2/v1": padded(65531), "k3/v1": padded(last)})
	}
	add("total at limit", true, four(65531))
	add("total over limit", false, four(65532))
	many := func(n int) func(m *packagekit.Manifest) {
		return func(m *packagekit.Manifest) {
			m.Metadata = map[string]json.RawMessage{}
			for i := 0; i < n; i++ {
				m.Metadata["k"+strings.Repeat("a", i)+"/v1"] = json.RawMessage(`{}`)
			}
		}
	}
	add("64 keys", true, many(64))
	add("65 keys", false, many(65))

	add("kind file", true, func(m *packagekit.Manifest) { m.Artifacts[0].Kind = "" })
	add("tree", true, withArtifacts(tree("vendor", "vendor")))
	add("unknown kind", false, func(m *packagekit.Manifest) { m.Artifacts[0].Kind = "dir" })
	add("tree contains file", false, withArtifacts(tree("vendor", "vendor"), file("inner", "vendor/a.txt")))
	add("file is ancestor of tree", false, withArtifacts(file("outer", "vendor"), tree("lib", "vendor/lib")))
	add("tree beside prefix sibling", true, withArtifacts(tree("vendor", "vendor"), file("other", "vendorx/a.txt")))
	add("tree contains tree", false, withArtifacts(tree("outer", "a"), tree("inner", "a/b")))
	add("tree equals file path", false, withArtifacts(tree("vendor", "vendor"), file("same", "vendor")))
	add("tree overlap is case-insensitive", false, withArtifacts(tree("vendor", "Vendor"), file("inner", "vendor/x")))
	add("tree overlap with unicode case", false, withArtifacts(tree("t", "É"), file("inner", "é/x")))
	add("two separate trees", true, withArtifacts(tree("a", "a"), tree("b", "b")))
	add("entrypoint on tree", false, func(m *packagekit.Manifest) {
		m.Artifacts = append(m.Artifacts, tree("vendor", "vendor"))
		m.Entrypoints[0].Artifact = "vendor"
	})
	add("asset on tree", true, func(m *packagekit.Manifest) {
		m.Artifacts = append(m.Artifacts, tree("vendor", "vendor"))
		m.Assets = []packagekit.Asset{{Artifact: "vendor", Kind: "icons"}}
	})

	for _, c := range cases {
		m := packageFixture("example/a")
		c.build(&m)
		goErr := m.Validate()
		if (goErr == nil) != c.valid {
			t.Errorf("%s: Go validity = %v (%v), want %v", c.name, goErr == nil, goErr, c.valid)
		}
		cErr := ValidateManifest(m)
		if (cErr == nil) != c.valid {
			t.Errorf("%s: C validity = %v (%v), want %v", c.name, cErr == nil, cErr, c.valid)
		}
		sameError(t, cErr, goErr)
	}
}

// TestTreeIntegrityDifferential compares tree-artifact verification in C and Go
// across good, changed, empty, linked and missing trees.
func TestTreeIntegrityDifferential(t *testing.T) {
	root := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("source.txt", "payload")
	put("vendor/a.txt", "one")
	put("vendor/lib/b.txt", "two")
	put("vendor/lib/deep/c.txt", "three")
	if err := os.MkdirAll(filepath.Join(root, "empty/inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifestFor := func(path string) packagekit.Manifest {
		m := packageFixture("example/a")
		digest, err := packagekit.TreeDigest(root, path)
		if err != nil {
			digest = strings.Repeat("0", 64)
		}
		m.Artifacts = append(m.Artifacts, packagekit.Artifact{Name: "tree", Path: path, Kind: packagekit.ArtifactTree, SHA256: digest})
		return m
	}
	check := func(label, path string, wantOK bool) {
		t.Helper()
		m := manifestFor(path)
		goErr, cErr := m.VerifyArtifacts(root), VerifyArtifacts(m, root)
		if (goErr == nil) != wantOK || (cErr == nil) != wantOK {
			t.Errorf("%s: Go %v, C %v, want ok=%v", label, goErr, cErr, wantOK)
		}
		sameError(t, cErr, goErr)
	}

	check("good tree", "vendor", true)
	check("good nested tree", "vendor/lib", true)
	if err := os.WriteFile(filepath.Join(root, "vendor/lib/deep/c.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := manifestFor("vendor")
	// Recorded digest now stale: change the file back after taking it.
	stale := m.Artifacts[len(m.Artifacts)-1].SHA256
	put("vendor/lib/deep/c.txt", "three")
	m.Artifacts[len(m.Artifacts)-1].SHA256 = stale
	sameError(t, VerifyArtifacts(m, root), m.VerifyArtifacts(root))
	if !errors.Is(VerifyArtifacts(m, root), plugin.ErrMismatch) || !errors.Is(m.VerifyArtifacts(root), plugin.ErrMismatch) {
		t.Error("changed content must be a mismatch in both engines")
	}
	put("vendor/extra.txt", "x")
	check("added file changes the digest recorded earlier", "vendor", true) // manifest is rebuilt from the new content
	m = manifestFor("vendor")
	os.Remove(filepath.Join(root, "vendor/extra.txt"))
	sameError(t, VerifyArtifacts(m, root), m.VerifyArtifacts(root))
	if !errors.Is(VerifyArtifacts(m, root), plugin.ErrMismatch) {
		t.Error("removed file must be a mismatch")
	}

	check("tree holding no files", "empty", false)
	check("missing tree", "nothing", false)
	check("tree path is a file", "source.txt", false)
	if err := os.Symlink(filepath.Join(root, "vendor"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	check("tree path is a symlink", "link", false)
	check("tree behind a symlinked parent", "link/lib", false)
	put("vendor/lib/odd.txt", "x")
	if err := os.Symlink(filepath.Join(root, "source.txt"), filepath.Join(root, "vendor/lib/ln")); err != nil {
		t.Fatal(err)
	}
	check("tree containing a symlink", "vendor/lib", false)
}

func TestArtifactDigestDifferential(t *testing.T) {
	root := t.TempDir()
	put := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("source.txt", "payload")
	put("vendor/lib/b.txt", "two")
	put("vendor/a.txt", "one")
	os.MkdirAll(filepath.Join(root, "empty/inner"), 0o700)
	os.Symlink(filepath.Join(root, "source.txt"), filepath.Join(root, "link.txt"))
	os.Symlink(filepath.Join(root, "vendor"), filepath.Join(root, "linkdir"))

	for _, rel := range []string{"source.txt", "vendor/a.txt", "vendor/lib/b.txt"} {
		want, err := packagekit.FileDigest(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := ArtifactDigest(root, rel, false); err != nil || got != want {
			t.Errorf("file %s: C %q (%v), Go %q", rel, got, err, want)
		}
	}
	for _, rel := range []string{"vendor", "vendor/lib"} {
		want, err := packagekit.TreeDigest(root, rel)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := ArtifactDigest(root, rel, true); err != nil || got != want {
			t.Errorf("tree %s: C %q (%v), Go %q", rel, got, err, want)
		}
	}
	for _, rel := range []string{"empty", "missing", "source.txt", "linkdir", "linkdir/lib", "vendor/a.txt", "../escape", "/abs", "a\\b", "", "a//b", "CON", "a/", "é\xff"} {
		_, goErr := packagekit.TreeDigest(root, rel)
		_, cErr := ArtifactDigest(root, rel, true)
		if goErr == nil || cErr == nil {
			t.Errorf("tree %q: Go %v, C %v: both must fail", rel, goErr, cErr)
			continue
		}
		sameError(t, cErr, goErr)
	}
	for _, rel := range []string{"link.txt", "linkdir/a.txt", "vendor", "missing.txt", "../escape", "/abs", "", "a//b", "CON"} {
		if _, err := ArtifactDigest(root, rel, false); err == nil {
			t.Errorf("file %q accepted", rel)
		}
	}
	if _, err := ArtifactDigest(root+"\x00", "source.txt", false); !errors.Is(err, plugin.ErrInvalid) {
		t.Error("NUL in root accepted")
	}
}
