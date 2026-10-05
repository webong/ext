//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/interop"
	"github.com/webong/ctx/pkg/plugin/jsonline"
	"github.com/webong/ctx/pkg/plugin/packagekit"
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
	bridge := interop.Bridge{Name: "ctx-bridge", Frontend: h, Backend: g}
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
