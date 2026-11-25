package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Recipe is a declarative input to ccgo, not a shell command. Paths inside the
// source tree remain relative so the temporary checkout is reproducible.
type Recipe struct {
	Version       int      `json:"version"`
	SourceRoot    string   `json:"source_root"`
	Package       string   `json:"package"`
	Sources       []string `json:"sources"`
	IncludeDirs   []string `json:"include_dirs"`
	Defines       []string `json:"defines"`
	Undefines     []string `json:"undefines"`
	ForceIncludes []string `json:"force_includes"`
	BuildTag      string   `json:"build_tag"`
	Bindings      string   `json:"bindings"`
	path          string
}

var tagName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadRecipe reads and validates a version-1 JSON recipe, resolving paths from its directory.
func LoadRecipe(path string) (*Recipe, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	var r Recipe
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&r); err != nil {
		return nil, fmt.Errorf("recipe %s: %w", path, err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("recipe must contain exactly one JSON object")
	}
	return normalizeRecipe(r, abs, false)
}

func normalizeRecipe(r Recipe, abs string, inline bool) (*Recipe, error) {
	r.path = abs
	if inline && r.Version == 0 {
		r.Version = 1
	}
	if r.Version != 1 {
		return nil, fmt.Errorf("recipe version must be 1")
	}
	if r.Package == "" {
		r.Package = "ccgo"
	}
	if !token.IsIdentifier(r.Package) || r.Package == "_" {
		return nil, fmt.Errorf("invalid Go package %q", r.Package)
	}
	if r.BuildTag != "" && !tagName.MatchString(r.BuildTag) {
		return nil, fmt.Errorf("build_tag must be one Go build tag")
	}
	if len(r.Sources) == 0 {
		return nil, fmt.Errorf("recipe requires sources")
	}
	for _, group := range [][]string{r.Sources, r.IncludeDirs, r.ForceIncludes} {
		for _, p := range group {
			if !fs.ValidPath(p) || strings.Contains(p, "\\") || strings.HasPrefix(p, "-") {
				return nil, fmt.Errorf("recipe path must be relative to source_root: %q", p)
			}
		}
	}
	if r.SourceRoot == "" {
		r.SourceRoot = "."
	}
	if !filepath.IsAbs(r.SourceRoot) {
		r.SourceRoot = filepath.Join(filepath.Dir(abs), r.SourceRoot)
	}
	info, err := os.Stat(r.SourceRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source_root must be a directory")
	}
	return &r, nil
}
func (r *Recipe) bindingsPath() string {
	if r.Bindings == "" || filepath.IsAbs(r.Bindings) {
		return r.Bindings
	}
	return filepath.Join(filepath.Dir(r.path), r.Bindings)
}

func generateCustom(ctx context.Context, log io.Writer, r *Recipe, out string) error {
	work, err := os.MkdirTemp("", "ext-custom-translate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// Do not follow links while copying a caller's source tree.
	err = filepath.WalkDir(r.SourceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source tree contains symbolic link: %s", path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = copyTree(r.SourceRoot, filepath.Join(work, "csrc")); err != nil {
		return err
	}
	if err := prepareTranslationModule(work, "ext/translate"); err != nil {
		return err
	}
	args := []string{"ccgo", "-o", "translation.go", "--package-name", r.Package}
	for _, p := range r.IncludeDirs {
		args = append(args, "-I", "csrc/"+p)
	}
	for _, d := range r.Defines {
		args = append(args, "-D"+d)
	}
	for _, d := range r.Undefines {
		args = append(args, "-U"+d)
	}
	for _, p := range r.ForceIncludes {
		args = append(args, "-include", "csrc/"+p)
	}
	for _, p := range r.Sources {
		args = append(args, "csrc/"+p)
	}
	if err := runCCGO(ctx, work, args, log); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(work, "translation.go"))
	if err != nil {
		return err
	}
	if r.BuildTag != "" {
		data = bytes.Replace(data, []byte("//go:build "), []byte("//go:build "+r.BuildTag+" && "), 1)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	return os.WriteFile(out, data, 0644)
}
