package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
)

// CitationSchema names the citation format.
const CitationSchema = "ext.library.citation/v1"

// A Citation pins one release of a C library: where each platform's build is published and the
// SHA-256 it must have. It is plain data, published by the library's own releases and read by
// ext lib fetch; this package knows no library by name.
type Citation struct {
	Schema  string  `json:"schema"`
	Library string  `json:"library"` // the library's C prefix, such as ext_plugin or ext_ledger
	Version string  `json:"version"`
	Assets  []Asset `json:"assets"`
}

// An Asset is one published archive (a .tar.gz) and what each driver takes from it.
type Asset struct {
	// Targets are the GOOS/GOARCH pairs the archive serves, such as "darwin/arm64".
	Targets []string `json:"targets"`
	URL     string   `json:"url"`
	SHA256  string   `json:"sha256"`
	// Root is the directory inside the archive that holds its files, if it has one.
	Root    string  `json:"root,omitempty"`
	Drivers Drivers `json:"drivers"`
}

// Drivers says, per driver, which files of an asset it needs. A driver that is absent is not
// published for the asset's targets.
type Drivers struct {
	CGo    *CGoFiles    `json:"cgo,omitempty"`
	PureGo *PureGoFiles `json:"purego,omitempty"`
	C2Go   *C2GoFiles   `json:"c2go,omitempty"`
}

// CGoFiles are the headers and the static library a cgo build links.
type CGoFiles struct {
	Include string   `json:"include"`           // a directory of headers
	Static  string   `json:"static"`            // the static library
	CFlags  []string `json:"cflags,omitempty"`  // such as -DEXT_PLUGIN_HOST_STATIC
	LDFlags []string `json:"ldflags,omitempty"` // system libraries, such as -lpthread
}

// PureGoFiles is the shared library purego loads.
type PureGoFiles struct {
	Shared string `json:"shared"`
}

// C2GoFiles is a directory of Go source: the library translated by ccgo, with its binding.
type C2GoFiles struct {
	Go string `json:"go"`
}

var (
	libraryName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	targetName  = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)
	digest      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ErrNoAsset is a target the citation publishes nothing for.
var ErrNoAsset = errors.New("the citation publishes no build for this target")

// ReadCitation reads and validates a citation file.
func ReadCitation(file string) (Citation, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Citation{}, err
	}
	return ParseCitation(data)
}

// ParseCitation decodes and validates a citation, refusing unknown fields.
func ParseCitation(data []byte) (Citation, error) {
	var c Citation
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return Citation{}, fmt.Errorf("citation: %w", err)
	}
	return c, c.Validate()
}

// Validate checks the citation's names, digests, URLs and file paths.
func (c Citation) Validate() error {
	if c.Schema != CitationSchema {
		return fmt.Errorf("citation: schema %q, want %q", c.Schema, CitationSchema)
	}
	if !libraryName.MatchString(c.Library) {
		return fmt.Errorf("citation: library %q is not a C prefix", c.Library)
	}
	if c.Version == "" || strings.ContainsAny(c.Version, "/\\ ") {
		return fmt.Errorf("citation: version %q", c.Version)
	}
	seen := map[string]bool{}
	for i, a := range c.Assets {
		if len(a.Targets) == 0 {
			return fmt.Errorf("citation: asset %d has no targets", i)
		}
		for _, t := range a.Targets {
			if !targetName.MatchString(t) || seen[t] {
				return fmt.Errorf("citation: asset %d: target %q is malformed or served twice", i, t)
			}
			seen[t] = true
		}
		u, err := url.Parse(a.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("citation: asset %d: url must be plain https", i)
		}
		if !digest.MatchString(a.SHA256) {
			return fmt.Errorf("citation: asset %d: sha256 must be 64 lowercase hex digits", i)
		}
		paths := []string{a.Root}
		if d := a.Drivers.CGo; d != nil {
			if d.Include == "" || d.Static == "" {
				return fmt.Errorf("citation: asset %d: cgo needs include and static", i)
			}
			paths = append(paths, d.Include, d.Static)
		}
		if d := a.Drivers.PureGo; d != nil {
			if d.Shared == "" {
				return fmt.Errorf("citation: asset %d: purego needs shared", i)
			}
			paths = append(paths, d.Shared)
		}
		if d := a.Drivers.C2Go; d != nil {
			if d.Go == "" {
				return fmt.Errorf("citation: asset %d: c2go needs go", i)
			}
			paths = append(paths, d.Go)
		}
		for _, p := range paths {
			if p != "" && !LocalPath(p) {
				return fmt.Errorf("citation: asset %d: path %q leaves the archive", i, p)
			}
		}
	}
	return nil
}

// Asset returns the asset that serves target ("goos/goarch").
func (c Citation) Asset(target string) (Asset, error) {
	for _, a := range c.Assets {
		for _, t := range a.Targets {
			if t == target {
				return a, nil
			}
		}
	}
	return Asset{}, fmt.Errorf("%s %s for %s: %w", c.Library, c.Version, target, ErrNoAsset)
}

// LocalPath reports whether p is a relative, slash-separated path that stays inside its root.
func LocalPath(p string) bool {
	if p == "" || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) || path.IsAbs(p) {
		return false
	}
	clean := path.Clean(p)
	return clean == p && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}
