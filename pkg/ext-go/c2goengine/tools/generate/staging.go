package generate

import (
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// publishTranslation writes the c2go driver's Go directory for goos/goarch into dir: every file of
// the package that builds there with -tags ext_c2go (the translation and the binding beside it),
// each named for the platform (call.go becomes call_<goos>_<goarch>.go) and without its //go:build
// line, so the name alone constrains it. A project that fetches several targets gets one file per
// target and nothing clashes, and it needs no build tag. Test files are not published.
func publishTranslation(pkg, dir, goos, goarch string) (int, error) {
	return publishTaggedTranslation(pkg, dir, goos, goarch, "ext_c2go")
}
func publishTaggedTranslation(pkg, dir, goos, goarch, tag string) (int, error) {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.BuildTags, ctx.CgoEnabled = goos, goarch, strings.FieldsFunc(tag, func(r rune) bool { return r == ',' || r == ' ' }), false
	entries, err := os.ReadDir(pkg)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	suffix := "_" + goos + "_" + goarch
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if ok, err := ctx.MatchFile(pkg, name); err != nil {
			return 0, err
		} else if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pkg, name))
		if err != nil {
			return 0, err
		}
		data = goBuildLine.ReplaceAll(data, nil)
		base := strings.TrimSuffix(name, ".go")
		if !strings.HasSuffix(base, suffix) {
			base += suffix
		}
		if err := os.WriteFile(filepath.Join(dir, base+".go"), data, 0o644); err != nil {
			return 0, err
		}
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("nothing of %s builds for %s/%s", pkg, goos, goarch)
	}
	return n, nil
}

var goBuildLine = regexp.MustCompile(`(?m)^//go:build .*\n`)

// stageGenerated always stages the selected output, including -out overrides,
// rather than accidentally reusing a stale translation beside the binding.
func stageGenerated(bindings, generated, destination, tag string) (int, error) {
	if bindings != "" {
		if err := separatePaths(bindings, destination); err != nil {
			return 0, err
		}
	}
	if err := separatePaths(filepath.Dir(generated), destination); err != nil {
		return 0, err
	}
	work, err := os.MkdirTemp("", "ext-translation-stage-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(work)
	if bindings != "" {
		entries, err := os.ReadDir(bindings)
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, "generated_") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(bindings, name)
			abs, err := filepath.Abs(path)
			if err != nil {
				return 0, err
			}
			if abs == generated {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return 0, err
			}
			if err = os.WriteFile(filepath.Join(work, name), data, 0644); err != nil {
				return 0, err
			}
		}
	}
	data, err := os.ReadFile(generated)
	if err != nil {
		return 0, err
	}
	name := fmt.Sprintf("generated_%s_%s.go", runtime.GOOS, runtime.GOARCH)
	if err = os.WriteFile(filepath.Join(work, name), data, 0644); err != nil {
		return 0, err
	}
	return publishTaggedTranslation(work, destination, runtime.GOOS, runtime.GOARCH, tag)
}

func separatePaths(source, destination string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	// Resolve existing directories too, so a symlink alias cannot overwrite inputs.
	if resolved, e := filepath.EvalSymlinks(source); e == nil {
		source = resolved
	}
	if resolved, e := filepath.EvalSymlinks(destination); e == nil {
		destination = resolved
	}
	rel, err := filepath.Rel(source, destination)
	if err != nil {
		return err
	}
	if rel == "." {
		return fmt.Errorf("staging destination must differ from its source directory")
	}
	return nil
}
