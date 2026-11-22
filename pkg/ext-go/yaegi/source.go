package yaegi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// source is a guest directory read into memory, with its digest.
type source struct {
	files  map[string][]byte // slash paths below the directory
	digest string
}

// readSource reads every file below dir, refusing links and special files as
// a tree artifact does, within the size bounds, and computes
// plugin.DirectoryDigest over the bytes it read.
func readSource(dir string, options Options) (*source, error) {
	maxBytes, maxFiles := options.MaxSourceBytes, options.MaxSourceFiles
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	if maxFiles <= 0 {
		maxFiles = 4096
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: Go source must be a directory", plugin.ErrInvalid)
	}
	s := &source{files: map[string][]byte{}}
	total := int64(0)
	err = filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: Go source entries must be regular files", plugin.ErrInvalid)
		}
		relative, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if strings.ContainsAny(relative, "\r\n") {
			return fmt.Errorf("%w: Go source paths cannot contain line breaks", plugin.ErrInvalid)
		}
		if len(s.files) >= maxFiles {
			return fmt.Errorf("%w: Go source has more than %d files", plugin.ErrInvalid, maxFiles)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxBytes-total+1))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		total += int64(len(data))
		if total > maxBytes {
			return fmt.Errorf("%w: Go source is larger than %d bytes", plugin.ErrInvalid, maxBytes)
		}
		s.files[relative] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(s.files))
	for name := range s.files {
		names = append(names, name)
	}
	sort.Strings(names)
	outer := sha256.New()
	for _, name := range names {
		inner := sha256.Sum256(s.files[name])
		fmt.Fprintf(outer, "./%s %s\n", name, hex.EncodeToString(inner[:]))
	}
	s.digest = hex.EncodeToString(outer.Sum(nil))
	return s, nil
}

// packageFiles are the Go files of the guest's package: those at the root of
// the directory, without tests. Files in subdirectories are not evaluated.
func (s *source) packageFiles() []string {
	var names []string
	for name := range s.files {
		if !strings.Contains(name, "/") && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// fs serves the package's files as a GOPATH directory for importPath.
func (s *source) fs(importPath string) fs.FS {
	out := fstest.MapFS{}
	for _, name := range s.packageFiles() {
		out[path.Join("src", importPath, name)] = &fstest.MapFile{Data: s.files[name], Mode: 0o444}
	}
	return out
}

// checkImports parses every file of the guest's package and refuses an
// import outside allowed, before any of it is evaluated.
func checkImports(s *source, allowed map[string]bool) error {
	files := s.packageFiles()
	if len(files) == 0 {
		return fmt.Errorf("%w: Go source has no .go file at its root", plugin.ErrInvalid)
	}
	set := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(set, name, s.files[name], parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("%w: parse %s: %v", plugin.ErrInvalid, name, err)
		}
		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%w: %s: import %s", plugin.ErrInvalid, name, spec.Path.Value)
			}
			if !allowed[p] {
				return fmt.Errorf("%w: %s imports %q, which the host does not allow", plugin.ErrDenied, name, p)
			}
		}
	}
	return nil
}
