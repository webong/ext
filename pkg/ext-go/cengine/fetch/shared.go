package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

// SharedOptions say which shared library to bring in at run time and where to keep it.
type SharedOptions struct {
	Citation library.Citation
	// Target is the "goos/goarch" pair; default this program's.
	Target string
	// Dir holds the extracted libraries; empty is the user cache's ext-go/library. A library is kept at
	// Dir/<library>/<archive sha256>/<file>, beside <file>.sha256, the digest of the file itself.
	Dir string
	// Fetch, Cache, Offline and Limits are as in Options.
	Fetch   func(ctx context.Context, url string) (io.ReadCloser, error)
	Cache   string
	Offline bool
	Limits  Limits
}

// Shared returns the path of the purego shared library the citation publishes for o.Target, verified
// against the citation. A copy kept by an earlier call is used when it still matches its digest;
// otherwise the archive is taken from the archive cache or downloaded, checked against the
// citation's SHA-256, and the library is extracted again. It writes by renaming complete files into
// place, so processes that bring in the same library at once agree on its contents.
func Shared(ctx context.Context, so SharedOptions) (string, error) {
	citation, target, dir := so.Citation, so.Target, so.Dir
	o := Options{Fetch: so.Fetch, Cache: so.Cache, Offline: so.Offline, Limits: so.Limits}
	if target == "" {
		target = runtime.GOOS + "/" + runtime.GOARCH
	}
	selections, err := Plan(citation, []string{PureGo}, []string{target})
	if err != nil {
		return "", err
	}
	asset := selections[0].Asset
	file := path.Base(asset.Drivers.PureGo.Shared)
	if file == "" || file == "." || file == "/" {
		return "", fmt.Errorf("fetch: %s names no shared library for %s", citation.Library, target)
	}
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cache, "ext-go", "library")
	}
	at := filepath.Join(dir, citation.Library, asset.SHA256)
	shared := filepath.Join(at, file)
	if kept(shared) {
		return shared, nil
	}
	o.Limits = o.Limits.withDefaults()
	data, err := download(ctx, o, asset)
	if err != nil {
		return "", err
	}
	files, err := extract(data, asset.Root, o.Limits)
	if err != nil {
		return "", fmt.Errorf("fetch: %s: %w", asset.URL, err)
	}
	content, ok := files[asset.Drivers.PureGo.Shared]
	if !ok {
		return "", fmt.Errorf("fetch: %s: no %s in the archive", asset.URL, asset.Drivers.PureGo.Shared)
	}
	if err := os.MkdirAll(at, 0o755); err != nil {
		return "", err
	}
	// The library first, then its digest: a digest is only ever beside the file it describes.
	if err := place(shared, content, 0o755); err != nil {
		return "", err
	}
	if err := place(shared+".sha256", []byte(sumOf(content)+"\n"), 0o644); err != nil {
		return "", err
	}
	if !kept(shared) {
		return "", fmt.Errorf("fetch: %s changed while it was written", shared)
	}
	return shared, nil
}

// kept reports whether a library extracted earlier is still the one its digest names.
func kept(shared string) bool {
	want, err := os.ReadFile(shared + ".sha256")
	if err != nil {
		return false
	}
	data, err := os.ReadFile(shared)
	if err != nil {
		return false
	}
	return string(bytes.TrimSpace(want)) == sumOf(data)
}

// place writes data at target through a complete temporary file. Where target already holds data
// (another process placed it, and on Windows may have it loaded, so it cannot be replaced) that is
// success.
func place(target string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".fetch-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		if existing, rerr := os.ReadFile(target); rerr == nil && bytes.Equal(existing, data) {
			return nil
		}
		return err
	}
	return nil
}
