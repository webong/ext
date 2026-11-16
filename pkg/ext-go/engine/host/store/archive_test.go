package store_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

// member is one archive entry. A nil body with a trailing slash is a directory;
// link makes it a symbolic link.
type member struct {
	name string
	body []byte
	link string
	mode fs.FileMode
}

// members lists a package directory as archive entries under prefix.
func members(t testing.TB, dir, prefix string) []member {
	t.Helper()
	var out []member
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		body, err := os.ReadFile(path)
		out = append(out, member{name: prefix + filepath.ToSlash(rel), body: body})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func tarGz(t testing.TB, entries []member) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.mode != 0 {
			header.Mode = int64(e.mode)
		}
		switch {
		case e.link != "":
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, e.link, 0
		case strings.HasSuffix(e.name, "/"):
			header.Typeflag, header.Mode = tar.TypeDir, 0o755
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(writer.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zipped(t testing.TB, entries []member) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		body := e.body
		if e.link != "" {
			mode, body = fs.ModeSymlink|0o777, []byte(e.link)
		}
		if !strings.HasSuffix(e.name, "/") {
			header.SetMode(mode)
		}
		out, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// pinned writes an archive under a name that says nothing about its format.
func pinned(t testing.TB, data []byte) store.Archive {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return store.Archive{Source: path, SHA256: sum(data)}
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestInstallArchiveBothFormats(t *testing.T) {
	ctx := context.Background()
	for name, build := range map[string]func(testing.TB, []member) []byte{"tar.gz": tarGz, "zip": zipped} {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			// r1: the manifest sits in one top-level directory, as a release tarball has it.
			first := append([]member{{name: "./"}, {name: "./pkg-1/"}}, members(t, source(t, "example/a", "r1", "one"), "./pkg-1/")...)
			if name == "zip" {
				first = members(t, source(t, "example/a", "r1", "one"), "pkg-1/")
			}
			archive := pinned(t, build(t, first))
			archive.SHA256 = strings.ToUpper(archive.SHA256)
			installed, err := s.InstallArchive(ctx, archive, store.ArchiveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if installed.Name() != "example/a" || installed.Manifest.Descriptor.Identity.Revision != "r1" || installed.Verify() != nil {
				t.Fatal(installed)
			}
			receipt, err := s.Receipt("example/a")
			if err != nil || receipt != (store.Receipt{ID: "example/a", Revision: "r1", SHA256: strings.ToLower(archive.SHA256)}) {
				t.Fatal(receipt, err)
			}
			// r2: the manifest at the top of the archive; an upgrade.
			second := pinned(t, build(t, members(t, source(t, "example/a", "r2", "two"), "")))
			second.ID = "example/a"
			upgraded, err := s.InstallArchive(ctx, second, store.ArchiveOptions{})
			if err != nil || upgraded.Manifest.Descriptor.Identity.Revision != "r2" {
				t.Fatal(upgraded, err)
			}
			if receipt, err := s.Receipt("example/a"); err != nil || receipt.Revision != "r2" || receipt.SHA256 != second.SHA256 {
				t.Fatal(receipt, err)
			}
			if entries, err := os.ReadDir(s.Root()); err != nil || len(entries) != 2 {
				t.Fatalf("staging left behind: %v %v", entries, err)
			}
			if list, err := s.List(); err != nil || len(list) != 1 {
				t.Fatal(list, err)
			}
			if err := s.Remove("example/a"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Receipt("example/a"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallArchiveRefusals(t *testing.T) {
	ctx := context.Background()
	good := members(t, source(t, "example/a", "r1", "one"), "")
	with := func(extra ...member) []member { return append(append([]member{}, good...), extra...) }
	outside := filepath.Join(t.TempDir(), "escaped")
	cases := []struct {
		name    string
		archive func(t *testing.T) store.Archive
		limits  store.Limits
		want    error
	}{
		{name: "hash mismatch", want: plugin.ErrMismatch, archive: func(t *testing.T) store.Archive {
			a := pinned(t, tarGz(t, good))
			a.SHA256 = sum([]byte("something else"))
			return a
		}},
		{name: "missing hash", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			a := pinned(t, tarGz(t, good))
			a.SHA256 = ""
			return a
		}},
		{name: "oversized archive file", want: plugin.ErrInvalid, limits: store.Limits{MaxArchiveBytes: 64},
			archive: func(t *testing.T) store.Archive { return pinned(t, tarGz(t, good)) }},
		{name: "oversized tar content", want: plugin.ErrInvalid, limits: store.Limits{MaxTreeBytes: 1024},
			archive: func(t *testing.T) store.Archive {
				return pinned(t, tarGz(t, with(member{name: "big", body: bytes.Repeat([]byte{0}, 1<<20)})))
			}},
		{name: "oversized zip content", want: plugin.ErrInvalid, limits: store.Limits{MaxTreeBytes: 1024},
			archive: func(t *testing.T) store.Archive {
				return pinned(t, zipped(t, with(member{name: "big", body: bytes.Repeat([]byte{0}, 1<<20)})))
			}},
		{name: "oversized single file", want: plugin.ErrInvalid, limits: store.Limits{MaxArtifactBytes: 1024},
			archive: func(t *testing.T) store.Archive {
				return pinned(t, zipped(t, with(member{name: "big", body: bytes.Repeat([]byte{0}, 1<<20)})))
			}},
		{name: "too many files", want: plugin.ErrInvalid, limits: store.Limits{MaxTreeFiles: 2},
			archive: func(t *testing.T) store.Archive {
				return pinned(t, tarGz(t, with(member{name: "x", body: []byte("x")})))
			}},
		{name: "tar link", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, tarGz(t, with(member{name: "link", link: outside})))
		}},
		{name: "zip link", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, zipped(t, with(member{name: "link", link: outside})))
		}},
		{name: "tar path escape", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, tarGz(t, with(member{name: "../../escaped", body: []byte("x")})))
		}},
		{name: "zip path escape", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, zipped(t, with(member{name: "src/../../../escaped", body: []byte("x")})))
		}},
		{name: "absolute path", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, tarGz(t, with(member{name: outside, body: []byte("x")})))
		}},
		{name: "duplicate path", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, tarGz(t, with(member{name: "SRC/MAIN.PY", body: []byte("x")})))
		}},
		{name: "unlisted file", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, tarGz(t, with(member{name: "extra", body: []byte("x")})))
		}},
		{name: "two top-level directories", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, zipped(t, append(members(t, source(t, "example/a", "r1", "one"), "a/"), member{name: "b/x", body: []byte("x")})))
		}},
		{name: "plain tar", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return pinned(t, []byte("not an archive at all"))
		}},
		{name: "another package", want: plugin.ErrMismatch, archive: func(t *testing.T) store.Archive {
			a := pinned(t, tarGz(t, good))
			a.ID = "example/b"
			return a
		}},
		{name: "plain http", want: plugin.ErrInvalid, archive: func(t *testing.T) store.Archive {
			return store.Archive{Source: "http://example.invalid/a.tar.gz", SHA256: sum(nil)}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := store.New(filepath.Join(t.TempDir(), "plugins"), c.limits)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.InstallArchive(ctx, c.archive(t), store.ArchiveOptions{}); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if list, err := s.List(); err != nil || len(list) != 0 {
				t.Fatal(list, err)
			}
			if _, err := os.Lstat(outside); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrote outside the store: %v", err)
			}
			if entries, _ := os.ReadDir(s.Root()); len(entries) != 0 {
				t.Fatalf("staging left behind: %v", entries)
			}
		})
	}
}

func TestInstallArchiveSameRevision(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	dir := source(t, "example/a", "r1", "one")
	if _, err := s.Install(dir); err != nil {
		t.Fatal(err)
	}
	// The same content at the same revision is adopted and receipted.
	if _, err := s.InstallArchive(ctx, pinned(t, zipped(t, members(t, dir, ""))), store.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	if receipt, err := s.Receipt("example/a"); err != nil || receipt.Revision != "r1" {
		t.Fatal(receipt, err)
	}
	// Other content under the same revision is not.
	other := pinned(t, zipped(t, members(t, source(t, "example/a", "r1", "changed"), "")))
	if _, err := s.InstallArchive(ctx, other, store.ArchiveOptions{}); !errors.Is(err, plugin.ErrMismatch) {
		t.Fatal(err)
	}
}

// origin serves archives over TLS and returns a download client that trusts it.
func origin(t testing.TB, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client := store.NewDownloadClient()
	client.Transport = server.Client().Transport
	return server, client
}

func TestInstallArchiveReceiptSkipsDownload(t *testing.T) {
	ctx := context.Background()
	data := tarGz(t, members(t, source(t, "example/a", "r1", "one"), "pkg/"))
	var requests atomic.Int32
	server, client := origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(data)
	}))
	s := newStore(t)
	archive := store.Archive{Source: server.URL + "/a", SHA256: sum(data)}
	options := store.ArchiveOptions{Client: client}
	first, err := s.InstallArchive(ctx, archive, options)
	if err != nil || requests.Load() != 1 {
		t.Fatal(err, requests.Load())
	}
	again, err := s.InstallArchive(ctx, archive, options)
	if err != nil || requests.Load() != 1 || again.Directory != first.Directory {
		t.Fatalf("repeat install: %v after %d requests", err, requests.Load())
	}
	// A receipt is not trusted over the content: a changed package is not skipped.
	path, err := first.ArtifactPath("main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstallArchive(ctx, archive, options); err == nil || requests.Load() != 2 {
		t.Fatalf("tampered package: %v after %d requests", err, requests.Load())
	}
	// Nor over the revision: a package replaced by other means is not skipped.
	if err := os.RemoveAll(first.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(source(t, "example/a", "r0", "zero")); err != nil {
		t.Fatal(err)
	}
	back, err := s.InstallArchive(ctx, archive, options)
	if err != nil || requests.Load() != 3 || back.Manifest.Descriptor.Identity.Revision != "r1" {
		t.Fatalf("replaced package: %v after %d requests", err, requests.Load())
	}
}

func TestDownloadRedirects(t *testing.T) {
	ctx := context.Background()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed a redirect to plain HTTP")
	}))
	defer plain.Close()
	var landed http.Header
	server, client := origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/downgrade":
			http.Redirect(w, r, plain.URL+"/a", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/start":
			http.Redirect(w, r, "/landed", http.StatusFound)
		case "/landed":
			landed = r.Header.Clone()
			_, _ = w.Write([]byte("payload"))
		case "/large":
			_, _ = w.Write(bytes.Repeat([]byte{1}, 4096))
		default:
			http.NotFound(w, r)
		}
	}))
	target := func() string { return filepath.Join(t.TempDir(), "out") }
	absent := func(path string) {
		t.Helper()
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a failed download left %s", path)
		}
	}

	out := target()
	if err := store.Download(ctx, client, server.URL+"/downgrade", out, 0); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("redirect to http: %v", err)
	}
	absent(out)
	if err := store.Download(ctx, client, server.URL+"/loop", out, 0); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("redirect loop: %v", err)
	}
	if err := store.Download(ctx, client, server.URL+"/missing", out, 0); err == nil {
		t.Fatal("a 404 was accepted")
	}
	if err := store.Download(ctx, client, server.URL+"/large", out, 1024); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("oversized download: %v", err)
	}
	absent(out)
	for _, bad := range []string{
		strings.Replace(server.URL, "https://", "http://", 1) + "/landed",
		strings.Replace(server.URL, "https://", "https://user:secret@", 1) + "/landed",
		server.URL + "/landed#fragment",
		"file:///etc/passwd",
	} {
		if err := store.Download(ctx, client, bad, out, 0); !errors.Is(err, plugin.ErrInvalid) {
			t.Fatalf("%s: %v", bad, err)
		}
	}

	// A caller's client may carry credentials for the first origin; none of them
	// follow a redirect.
	credentialed := doer(func(request *http.Request) (*http.Response, error) {
		request.Header.Set("Authorization", "Bearer secret")
		request.Header.Set("Cookie", "session=secret")
		request.Header.Set("Referer", "https://private.example/page")
		return client.Do(request)
	})
	if err := store.Download(ctx, credentialed, server.URL+"/start", out, 0); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "payload" {
		t.Fatal(string(data), err)
	}
	for _, name := range []string{"Authorization", "Cookie", "Referer"} {
		if value := landed.Get(name); value != "" {
			t.Errorf("%s followed the redirect: %q", name, value)
		}
	}
	// An existing file is never overwritten.
	if err := store.Download(ctx, client, server.URL+"/landed", out, 0); err == nil {
		t.Fatal("overwrote an existing file")
	}
}

type doer func(*http.Request) (*http.Response, error)

func (d doer) Do(request *http.Request) (*http.Response, error) { return d(request) }

func TestExtractArchiveKeepsOnlyTheExecuteBit(t *testing.T) {
	data := tarGz(t, append(members(t, source(t, "example/a", "r1", "one"), ""),
		member{name: "run", body: []byte("#!/bin/sh\n"), mode: 0o4755}))
	archive := pinned(t, data)
	destination := filepath.Join(t.TempDir(), "out")
	root, err := store.ExtractArchive(archive.Source, "", destination, store.Limits{})
	if err != nil || root != destination {
		t.Fatal(root, err)
	}
	if info, err := os.Stat(filepath.Join(root, "run")); err != nil || info.Mode()&fs.ModeSetuid != 0 {
		t.Fatal(info, err)
	} else if filepath.Separator == '/' && info.Mode().Perm() != 0o700 {
		t.Fatal(info.Mode())
	}
	if _, err := store.ExtractArchive(archive.Source, "", destination, store.Limits{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("extracted into a directory that is not empty: %v", err)
	}
	for _, name := range []string{"", ".", "a//b", "a/../b", "/a", `a\b`, "c:a", "a/NUL.txt", "a/b.", "a\nb"} {
		if store.SafeArchivePath(name) {
			t.Errorf("%q accepted", name)
		}
	}
	if !store.SafeArchivePath("a/b-c_d.e") {
		t.Error("ordinary path refused")
	}
}
