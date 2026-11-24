// Package fetch brings a C library of lib/ into a Go project before it is built, so that the cgo,
// ccgo and purego drivers find it there. It reads a library.Citation, downloads each target's
// published archive (checking its SHA-256 before it uses a byte of it), and lays out a Go package in
// the project's folder:
//
//	cgo     include/ and lib/<goos>_<goarch>/<static library>, with a cgo file whose #cgo
//	        directives name them through ${SRCDIR}, so no CGO_* variable and no system install is
//	        needed
//	c2go    the translated Go source of each target, copied as published
//	purego  lib/<goos>_<goarch>/<shared library>, embedded into the program and registered with
//	        library.Provide, so it travels inside the binary
//
// The project imports that package once (a blank import) to use it. Go runs no code before a build,
// so this is a step the project runs first: go generate with `go tool ext lib fetch`.
package fetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

// The drivers fetch lays out.
const (
	CGo    = "cgo"
	C2Go   = "c2go"
	PureGo = "purego"
)

// Limits bound what an archive may hold.
type Limits struct {
	ArchiveBytes int64 // the downloaded archive; default 256 MiB
	FileBytes    int64 // one extracted file; default 256 MiB
	Files        int   // files extracted from one archive; default 10000
}

// Options say what to fetch and where.
type Options struct {
	Citation library.Citation
	// Drivers to lay out: CGo, C2Go and PureGo. At least one.
	Drivers []string
	// Targets are "goos/goarch" pairs; at least one.
	Targets []string
	// Dir is the package directory to write, inside the project's module.
	Dir string
	// Package is the package name of the files written; default the library name without "ext_".
	Package string
	// Fetch downloads a URL; nil uses an HTTPS GET with http.DefaultClient. A program with its own
	// downloader (ext has one) passes it here.
	Fetch func(ctx context.Context, url string) (io.ReadCloser, error)
	// Cache is a directory of verified archives, reused across runs; empty is the user cache's
	// ext-go/archives. Offline refuses any download not already there.
	Cache   string
	Offline bool
	Limits  Limits
}

// ErrOffline is an archive that is not cached when Options.Offline is set.
var ErrOffline = errors.New("fetch: not cached and offline")

var packageName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// packageClause is a Go file's package clause, at the start of a line.
var packageClause = regexp.MustCompile(`(?m)^(\s*)package [A-Za-z_][A-Za-z0-9_]*`)

// Fetch lays the library out in o.Dir for every target and driver, replacing what fetch wrote there
// before. It writes nothing until every archive has been downloaded and verified.
func Fetch(ctx context.Context, o Options) error {
	if err := o.Citation.Validate(); err != nil {
		return err
	}
	if len(o.Drivers) == 0 || len(o.Targets) == 0 || o.Dir == "" {
		return errors.New("fetch: drivers, targets and a directory are required")
	}
	for _, d := range o.Drivers {
		if d != CGo && d != C2Go && d != PureGo {
			return fmt.Errorf("fetch: unknown driver %q (want cgo, c2go or purego)", d)
		}
	}
	if o.Package == "" {
		o.Package = strings.TrimPrefix(o.Citation.Library, "ext_")
	}
	if !packageName.MatchString(o.Package) {
		return fmt.Errorf("fetch: package name %q", o.Package)
	}
	o.Limits = o.Limits.withDefaults()
	type plan struct {
		target string
		driver string
		asset  library.Asset
		files  map[string][]byte
	}
	var plans []plan
	archives := map[string]map[string][]byte{} // by sha256, so a universal archive is read once
	for _, target := range o.Targets {
		for _, d := range o.Drivers {
			asset, err := o.Citation.Asset(target, d)
			if err != nil {
				return fmt.Errorf("fetch: %s %s publishes no %s build for %s: %w", o.Citation.Library, o.Citation.Version, d, target, err)
			}
			files, ok := archives[asset.SHA256]
			if !ok {
				data, err := download(ctx, o, asset)
				if err != nil {
					return err
				}
				if files, err = extract(data, asset.Root, o.Limits); err != nil {
					return fmt.Errorf("fetch: %s: %w", asset.URL, err)
				}
				archives[asset.SHA256] = files
			}
			plans = append(plans, plan{target, d, asset, files})
		}
	}
	out := map[string][]byte{}
	for _, p := range plans {
		dir := strings.ReplaceAll(p.target, "/", "_")
		goos, goarch, _ := strings.Cut(p.target, "/")
		var err error
		switch p.driver {
		case CGo:
			err = layCGo(out, o, p.asset.Drivers.CGo, p.files, dir, goos, goarch)
		case C2Go:
			err = layC2Go(out, o, p.asset.Drivers.C2Go, p.files)
		case PureGo:
			err = layPureGo(out, o, p.asset.Drivers.PureGo, p.files, dir, goos, goarch)
		}
		if err != nil {
			return fmt.Errorf("fetch: %s %s: %w", p.driver, p.target, err)
		}
	}
	out["doc.go"] = []byte(fmt.Sprintf("// Package %s is %s %s, laid out by ext lib fetch for %s. Do not edit; run ext lib fetch again.\npackage %s\n",
		o.Package, o.Citation.Library, o.Citation.Version, strings.Join(o.Targets, ", "), o.Package))
	return write(o.Dir, out)
}

// hasDriver reports whether the asset publishes driver.
func hasDriver(a library.Asset, d string) bool {
	switch d {
	case CGo:
		return a.Drivers.CGo != nil
	case C2Go:
		return a.Drivers.C2Go != nil
	case PureGo:
		return a.Drivers.PureGo != nil
	}
	return false
}

func layCGo(out map[string][]byte, o Options, d *library.CGoFiles, files map[string][]byte, dir, goos, goarch string) error {
	included := false
	for name, data := range files {
		if rest, ok := strings.CutPrefix(name, d.Include+"/"); ok {
			out["include/"+rest] = data
			included = true
		}
	}
	if !included {
		return fmt.Errorf("no headers under %s", d.Include)
	}
	static, ok := files[d.Static]
	if !ok {
		return fmt.Errorf("no %s in the archive", d.Static)
	}
	staticPath := "lib/" + dir + "/" + path.Base(d.Static)
	out[staticPath] = static
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by ext lib fetch. DO NOT EDIT.\n\n//go:build cgo && %s && %s\n\npackage %s\n\n/*\n", goos, goarch, o.Package)
	fmt.Fprintf(&b, "#cgo CFLAGS: -I${SRCDIR}/include")
	for _, f := range d.CFlags {
		fmt.Fprintf(&b, " %s", f)
	}
	fmt.Fprintf(&b, "\n#cgo LDFLAGS: ${SRCDIR}/%s", staticPath)
	for _, f := range d.LDFlags {
		fmt.Fprintf(&b, " %s", f)
	}
	b.WriteString("\n*/\nimport \"C\"\n")
	out["cgo_"+dir+".go"] = []byte(b.String())
	return nil
}

// layC2Go copies the translation's Go files into the package, which takes the package's name: the
// first package clause of each file is rewritten.
func layC2Go(out map[string][]byte, o Options, d *library.C2GoFiles, files map[string][]byte) error {
	found := false
	for name, data := range files {
		rest, ok := strings.CutPrefix(name, d.Go+"/")
		if !ok || strings.Contains(rest, "/") || !strings.HasSuffix(rest, ".go") || strings.HasSuffix(rest, "_test.go") {
			continue
		}
		loc := packageClause.FindSubmatchIndex(data)
		if loc == nil {
			return fmt.Errorf("%s has no package clause", name)
		}
		renamed := append(append(append([]byte{}, data[:loc[3]]...), "package "+o.Package...), data[loc[1]:]...)
		out[rest] = renamed
		found = true
	}
	if !found {
		return fmt.Errorf("no Go files under %s", d.Go)
	}
	return nil
}

func layPureGo(out map[string][]byte, o Options, d *library.PureGoFiles, files map[string][]byte, dir, goos, goarch string) error {
	data, ok := files[d.Shared]
	if !ok {
		return fmt.Errorf("no %s in the archive", d.Shared)
	}
	file := path.Base(d.Shared)
	sharedPath := "lib/" + dir + "/" + file
	out[sharedPath] = data
	sum := sha256.Sum256(data)
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by ext lib fetch. DO NOT EDIT.\n\n//go:build %s && %s\n\npackage %s\n\n", goos, goarch, o.Package)
	b.WriteString("import (\n\t_ \"embed\"\n\n\t\"github.com/webong/ext/pkg/ext-go/cengine/library\"\n)\n\n")
	fmt.Fprintf(&b, "//go:embed %s\nvar shared_%s []byte\n\n", sharedPath, dir)
	fmt.Fprintf(&b, "func init() {\n\tlibrary.Provide(%q, library.Copy{File: %q, Data: shared_%s, SHA256: %q})\n}\n",
		o.Citation.Library, file, dir, hex.EncodeToString(sum[:]))
	out["purego_"+dir+".go"] = []byte(b.String())
	return nil
}

func download(ctx context.Context, o Options, a library.Asset) ([]byte, error) {
	cache := o.Cache
	if cache == "" {
		c, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cache = filepath.Join(c, "ext-go", "archives")
	}
	cached := filepath.Join(cache, a.SHA256+".tar.gz")
	if data, err := os.ReadFile(cached); err == nil && sumOf(data) == a.SHA256 {
		return data, nil
	}
	if o.Offline {
		return nil, fmt.Errorf("%s: %w", a.URL, ErrOffline)
	}
	get := o.Fetch
	if get == nil {
		get = httpsGet
	}
	body, err := get(ctx, a.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %s: %w", a.URL, err)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, o.Limits.ArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch: %s: %w", a.URL, err)
	}
	if int64(len(data)) > o.Limits.ArchiveBytes {
		return nil, fmt.Errorf("fetch: %s is larger than %d bytes", a.URL, o.Limits.ArchiveBytes)
	}
	if got := sumOf(data); got != a.SHA256 {
		return nil, fmt.Errorf("fetch: %s has sha256 %s, the citation pins %s", a.URL, got, a.SHA256)
	}
	if err := os.MkdirAll(cache, 0o755); err == nil {
		if tmp, err := os.CreateTemp(cache, ".archive-*"); err == nil {
			_, werr := tmp.Write(data)
			cerr := tmp.Close()
			if werr == nil && cerr == nil {
				_ = os.Rename(tmp.Name(), cached)
			}
			_ = os.Remove(tmp.Name())
		}
	}
	return data, nil
}

func httpsGet(ctx context.Context, url string) (io.ReadCloser, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, errors.New("only https is fetched")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := *http.DefaultClient
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 || r.URL.Scheme != "https" || r.URL.User != nil {
			return errors.New("refused redirect")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return resp.Body, nil
}

// extract reads a .tar.gz into memory, refusing links, special files and paths that leave the
// archive, and strips root.
func extract(data []byte, root string, l Limits) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(h.Name, "./")
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == "." {
			continue
		}
		if !library.LocalPath(name) {
			return nil, fmt.Errorf("archive path %q leaves the archive", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		case tar.TypeSymlink:
			continue // the libraries' version links; every file a driver needs is a regular file
		default:
			return nil, fmt.Errorf("archive entry %q is not a regular file", h.Name)
		}
		if root != "" {
			rest, ok := strings.CutPrefix(name, root+"/")
			if !ok {
				continue
			}
			name = rest
		}
		if len(files) >= l.Files {
			return nil, fmt.Errorf("more than %d files", l.Files)
		}
		if h.Size > l.FileBytes {
			return nil, fmt.Errorf("%s is larger than %d bytes", h.Name, l.FileBytes)
		}
		content, err := io.ReadAll(io.LimitReader(tr, l.FileBytes+1))
		if err != nil {
			return nil, err
		}
		files[name] = content
	}
	return files, nil
}

// manifest lists, in a fetched directory, every file fetch wrote there, so the next run removes
// exactly those and nothing of the project's own.
const manifest = ".fetched"

// write replaces what fetch wrote in dir before with out.
func write(dir string, out map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if previous, err := os.ReadFile(filepath.Join(dir, manifest)); err == nil {
		for _, name := range strings.Split(string(previous), "\n") {
			if name == "" || !library.LocalPath(name) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		for _, owned := range []string{"include", "lib"} {
			removeEmpty(filepath.Join(dir, owned))
		}
	}
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, out[name], 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, manifest), []byte(strings.Join(names, "\n")+"\n"), 0o644)
}

// removeEmpty removes the empty directories under root, root included.
func removeEmpty(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err == nil && e.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // fails, and keeps it, when it is not empty
	}
}

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (l Limits) withDefaults() Limits {
	if l.ArchiveBytes <= 0 {
		l.ArchiveBytes = 256 << 20
	}
	if l.FileBytes <= 0 {
		l.FileBytes = 256 << 20
	}
	if l.Files <= 0 {
		l.Files = 10000
	}
	return l
}
