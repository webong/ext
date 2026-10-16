//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/interop"
	"github.com/webong/ext/pkg/plugin-go/contract/packagekit"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
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
