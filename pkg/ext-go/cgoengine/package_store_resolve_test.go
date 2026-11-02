//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	gostore "github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ResolveEntrypoint in the C store is held against the pure-Go store's, the baseline. Both roots are
// given the same packages, and every case asks both for the same entrypoint: the selection, the
// digest and the path below the root must agree, and so must the class of every refusal. The
// platform is the machine's own (runtime.GOOS and runtime.GOARCH), so artifacts and entrypoints are
// written relative to it and the cases hold on every platform.

func resolveClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, plugin.ErrDenied):
		return "denied"
	case errors.Is(err, plugin.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, plugin.ErrNotFound):
		return "not found"
	case errors.Is(err, plugin.ErrMismatch):
		return "mismatch"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	}
	return "other: " + err.Error()
}

// otherOS is an operating system that entryMatchesOS knows and this machine is not.
func otherOS() string {
	for _, name := range []string{"windows", "linux", "darwin", "freebsd", "android"} {
		if name != runtime.GOOS {
			return name
		}
	}
	return ""
}

type resolveArtifact struct{ name, os, arch string }

// resolveSource writes a package at dir: a file artifact bin/<name> per artifact and the entrypoints.
func resolveSource(t *testing.T, dir, id string, artifacts []resolveArtifact, entrypoints []packagekit.Entrypoint) {
	t.Helper()
	var listed []packagekit.Artifact
	for _, a := range artifacts {
		path := filepath.Join(dir, "bin", a.name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(a.name), 0o700); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(a.name))
		listed = append(listed, packagekit.Artifact{Name: a.name, Path: "bin/" + a.name, SHA256: hex.EncodeToString(sum[:]), OS: a.os, Arch: a.arch})
	}
	manifest := packagekit.Manifest{
		APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: id, Revision: "r1"},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example", Version: "v1"}, Operations: []plugin.Operation{{Name: "run"}}}}},
		Artifacts:   listed,
		Entrypoints: entrypoints,
	}
	if err := gostore.WriteManifest(dir, manifest); err != nil {
		t.Fatal(err)
	}
}

func argv(name, artifact string, protocols ...string) packagekit.Entrypoint {
	return packagekit.Entrypoint{Name: name, Runtime: "argv", Artifact: artifact, Protocols: protocols}
}

func TestResolveEntrypointMatchesGo(t *testing.T) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	const p = "example/v1"
	scratch := t.TempDir()
	cRoot, goRoot := filepath.Join(scratch, "c"), filepath.Join(scratch, "go")
	c, err := OpenPackageStore(cRoot, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	g, err := openGoPackageStore(goRoot, PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	packages := map[string]struct {
		artifacts   []resolveArtifact
		entrypoints []packagekit.Entrypoint
	}{
		// an entrypoint named for this operating system beats the plain one; one named for another is skipped
		"names": {
			[]resolveArtifact{{"generic", "", ""}, {"native", "", ""}, {"foreign", "", ""}},
			[]packagekit.Entrypoint{argv("main", "generic", p), argv("main."+otherOS(), "foreign", p), argv("main."+goos, "native", p)},
		},
		// 3 for os and arch, 2 for os alone, 1 for arch alone; wrong platforms and other runtimes and protocols never count
		"score": {
			[]resolveArtifact{{"any", "", ""}, {"os", goos, ""}, {"both", goos, goarch}, {"arch", "", goarch}, {"wrongos", "neverix", ""}, {"wrongarch", "", "neverarch"}, {"again", goos, ""}},
			[]packagekit.Entrypoint{
				argv("e0", "any", p), argv("e1", "os", p), argv("e2", "wrongos", p), argv("e3", "wrongarch", p),
				{Name: "wasm", Runtime: "wasm", Artifact: "both", Protocols: []string{p}},
				argv("other", "both", "other/v1"),
				argv("e4", "both", p), argv("e5", "arch", p), argv("e6", "again", p),
			},
		},
		// two entrypoints at the same score: the first in manifest order
		"ties": {
			[]resolveArtifact{{"first", goos, ""}, {"second", goos, ""}},
			[]packagekit.Entrypoint{argv("a", "first", p), argv("b", "second", p)},
		},
		// nothing here can run on this platform
		"foreign": {
			[]resolveArtifact{{"elsewhere", "neverix", ""}},
			[]packagekit.Entrypoint{argv("main", "elsewhere", p)},
		},
		// no entrypoint at all
		"source": {
			[]resolveArtifact{{"script", "", ""}},
			nil,
		},
	}
	digests := map[string]string{}
	for id, spec := range packages {
		source := filepath.Join(scratch, "src-"+id)
		resolveSource(t, source, id, spec.artifacts, spec.entrypoints)
		if _, err := c.Install(source, false); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if _, err := g.Install(source, false); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		installed, err := gostore.New(goRoot, gostore.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := installed.Load(id)
		if err != nil {
			t.Fatal(err)
		}
		if digests[id], err = pkg.Digest(); err != nil {
			t.Fatal(err)
		}
	}

	type ask struct {
		label, id, contract, version, protocol, digest string
	}
	var asks []ask
	for id := range packages {
		d := digests[id]
		asks = append(asks,
			ask{"reviewed", id, "example", "v1", p, d},
			ask{"no digest", id, "example", "v1", p, ""},
			ask{"wrong digest", id, "example", "v1", p, "wrong"},
			ask{"upper digest", id, "example", "v1", p, strings.ToUpper(d)},
			ask{"other contract", id, "different", "v1", p, d},
			ask{"other version", id, "example", "v2", p, d},
			ask{"other protocol", id, "example", "v1", "none/v1", d},
			ask{"empty contract", id, "", "v1", p, d},
			ask{"the other protocol", id, "example", "v1", "other/v1", d},
		)
	}
	asks = append(asks,
		ask{"absent", "absent", "example", "v1", p, "x"},
		ask{"invalid id", "../bad", "example", "v1", p, "x"},
		ask{"invalid id, no digest", "../bad", "example", "v1", p, ""},
		ask{"empty id", "", "example", "v1", p, "x"},
	)
	selected := 0
	compare := func(stage string) {
		t.Helper()
		for _, a := range asks {
			want, wantErr := g.ResolveEntrypoint(a.id, a.contract, a.version, a.protocol, a.digest)
			got, gotErr := c.ResolveEntrypoint(a.id, a.contract, a.version, a.protocol, a.digest)
			if resolveClass(gotErr) != resolveClass(wantErr) {
				t.Errorf("%s %s [%s]: C %s (%v), Go %s (%v)", stage, a.id, a.label, resolveClass(gotErr), gotErr, resolveClass(wantErr), wantErr)
				continue
			}
			if wantErr != nil {
				if got != (PackageEntrypoint{}) {
					t.Errorf("%s %s [%s]: a refusal returned %+v", stage, a.id, a.label, got)
				}
				continue
			}
			selected++
			rel := func(root, path string) string {
				r, err := filepath.Rel(root, path)
				if err != nil || strings.HasPrefix(r, "..") {
					t.Errorf("%s %s [%s]: %q is not below %q", stage, a.id, a.label, path, root)
				}
				return filepath.ToSlash(r)
			}
			if got.Name != want.Name || got.Artifact != want.Artifact || got.Digest != want.Digest ||
				rel(cRoot, got.Path) != rel(goRoot, want.Path) {
				t.Errorf("%s %s [%s]: C %+v, Go %+v", stage, a.id, a.label, got, want)
			}
		}
	}
	compare("installed")
	if selected < 4 {
		t.Fatalf("only %d cases selected an entrypoint: the matrix tests nothing", selected)
	}

	// the selections the rules are meant to give, not just agreement
	for id, want := range map[string]string{"names": "main." + goos, "score": "e4", "ties": "a"} {
		got, err := c.ResolveEntrypoint(id, "example", "v1", p, digests[id])
		if err != nil || got.Name != want {
			t.Errorf("%s: selected %+v (%v), want %s", id, got, err, want)
		}
	}
	if _, err := c.ResolveEntrypoint("foreign", "example", "v1", p, digests["foreign"]); !errors.Is(err, plugin.ErrUnsupported) {
		t.Errorf("foreign: %v", err)
	}

	// a package changed behind the store's back is read first, so it is never selected
	for _, root := range []string{cRoot, goRoot} {
		if err := os.WriteFile(filepath.Join(root, "names", "bin", "generic"), []byte("tampered"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	selected = 0
	compare("tampered")
	if _, err := c.ResolveEntrypoint("names", "example", "v1", p, digests["names"]); !errors.Is(err, plugin.ErrMismatch) {
		t.Errorf("tampered package: %v", err)
	}
}

func TestResolveEntrypointRefusesAClosedStoreAndNulBytes(t *testing.T) {
	c, err := OpenPackageStore(filepath.Join(t.TempDir(), "store"), PackageStoreLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveEntrypoint("a\x00b", "example", "v1", "p/v1", "x"); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("a NUL in the id: %v", err)
	}
	if _, err := c.ResolveEntrypoint("a", "example", "v1", "p\x00", "x"); !errors.Is(err, plugin.ErrInvalid) {
		t.Errorf("a NUL in the protocol: %v", err)
	}
	c.Close()
	if _, err := c.ResolveEntrypoint("a", "example", "v1", "p/v1", "x"); !errors.Is(err, plugin.ErrClosed) {
		t.Errorf("a closed store: %v", err)
	}
}
