package library_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

const good = `{"schema":"ext.library.citation/v1","library":"ext_plugin","version":"0.4.0","assets":[
 {"targets":["linux/amd64"],"url":"https://example.com/a.tar.gz","sha256":"%s","root":"ext-plugin-0.4.0-linux-x86_64",
  "drivers":{"cgo":{"include":"include","static":"lib/libext_plugin_host_static.a","cflags":["-DEXT_PLUGIN_HOST_STATIC"]},"purego":{"shared":"lib/libext_plugin_host.so"}}},
 {"targets":["darwin/amd64","darwin/arm64"],"url":"https://example.com/m.tar.gz","sha256":"%s","drivers":{"purego":{"shared":"lib/libext_plugin_host.dylib"}}}]}`

func citation(replace ...string) string {
	s := strings.Replace(good, "%s", strings.Repeat("a", 64), 1)
	s = strings.Replace(s, "%s", strings.Repeat("b", 64), 1)
	for i := 0; i+1 < len(replace); i += 2 {
		s = strings.Replace(s, replace[i], replace[i+1], 1)
	}
	return s
}

func TestCitation(t *testing.T) {
	c, err := library.ParseCitation([]byte(citation()))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := c.Asset("darwin/arm64", "purego"); err != nil || a.Drivers.PureGo == nil || a.Drivers.CGo != nil {
		t.Errorf("darwin/arm64: %+v, %v", a, err)
	}
	if _, err := c.Asset("darwin/arm64", "cgo"); !errors.Is(err, library.ErrNoAsset) {
		t.Errorf("a driver the target's asset does not serve: %v", err)
	}
	// A target's translation is an asset of its own, beside its built library.
	withC2Go := strings.TrimSuffix(citation(), `]}`) + `,
 {"targets":["linux/amd64"],"url":"https://example.com/c.tar.gz","sha256":"` + strings.Repeat("c", 64) + `","drivers":{"c2go":{"go":"go"}}}]}`
	c, err = library.ParseCitation([]byte(withC2Go))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := c.Asset("linux/amd64", "c2go"); err != nil || a.Drivers.C2Go == nil {
		t.Errorf("linux/amd64 c2go: %+v, %v", a, err)
	}
	if a, err := c.Asset("linux/amd64", "cgo"); err != nil || a.Drivers.CGo == nil {
		t.Errorf("linux/amd64 cgo: %+v, %v", a, err)
	}
	for name, bad := range map[string]string{
		"a second document":  citation() + " {}",
		"trailing garbage":   citation() + " garbage",
		"an unknown field":   citation(`"version"`, `"extra":1,"version"`),
		"a plain http url":   citation("https://example.com/a", "http://example.com/a"),
		"a short digest":     citation(strings.Repeat("a", 64), "abc"),
		"a path that leaves": citation("lib/libext_plugin_host.so", "../libext_plugin_host.so"),
		"an absolute path":   citation(`"include"`, `"/usr/include"`),
		"a target twice":     citation(`"darwin/amd64"`, `"linux/amd64"`),
		"no driver":          citation(`"drivers":{"purego":{"shared":"lib/libext_plugin_host.dylib"}}`, `"drivers":{}`),
		"a schema":           citation("ext.library.citation/v1", "v2"),
		"a library name":     citation(`"ext_plugin"`, `"Ext-Plugin"`),
	} {
		if _, err := library.ParseCitation([]byte(bad)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestMaterialize(t *testing.T) {
	data := []byte("a shared library")
	sum := sha256.Sum256(data)
	c := library.Copy{File: "libext_demo.so", Data: data, SHA256: hex.EncodeToString(sum[:])}
	dir := t.TempDir()
	path, err := library.Materialize(c, "ext_demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "ext_demo", c.SHA256, "libext_demo.so"); path != want {
		t.Errorf("path %s, want %s", path, want)
	}
	if got, _ := os.ReadFile(path); string(got) != string(data) {
		t.Errorf("wrote %q", got)
	}
	if again, err := library.Materialize(c, "ext_demo", dir); err != nil || again != path {
		t.Errorf("a second materialize: %s, %v", again, err)
	}
	c.Data = []byte("tampered")
	if _, err := library.Materialize(c, "ext_demo", dir); err == nil {
		t.Error("a copy that does not match its digest was written")
	}
	c.Data, c.File = data, "../escape.so"
	if _, err := library.Materialize(c, "ext_demo", dir); err == nil {
		t.Error("a file name with a path was accepted")
	}
	if _, err := library.OpenProvided("ext_nothing", dir); err == nil {
		t.Error("a library nothing provides opened")
	}
}
