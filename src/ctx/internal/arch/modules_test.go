package arch_test

// A module is published and installed on its own, so its go.mod has to say what
// it needs without the workspace. This offline check catches the usual defect:
// a module imports a sibling (directly or through another sibling) that its
// go.mod does not list, or it requires modules but has no go.sum.
//
// Versions are checked online by scripts/check-standalone-modules.sh, because
// that needs the published tags.

import (
	"bufio"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const siblingPrefix = "github.com/webong/ext/"

// productModules are built from source with the workspace; they are not go-get
// resolvable by design, so they are not checked here.
var productModules = map[string]bool{"src/ctx": true, "src/ctn": true}

type workspaceModule struct {
	dir     string
	path    string          // module path without the repository prefix
	listed  map[string]bool // sibling modules named in go.mod requires
	hasReqs bool
	imports map[string]bool // sibling packages imported by this module's own files, tests included
	// packages maps each package path in the module to the sibling packages its
	// non-test files import; a dependent's build reaches these, not test imports.
	packages map[string]map[string]bool
}

func workspaceModules(t *testing.T) map[string]*workspaceModule {
	t.Helper()
	root := repoRoot(t)
	file, err := os.Open(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var dirs []string
	inUse := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "use (":
			inUse = true
		case line == ")":
			inUse = false
		case inUse && strings.HasPrefix(line, "./"):
			dirs = append(dirs, strings.TrimPrefix(line, "./"))
		case strings.HasPrefix(line, "use ./"):
			dirs = append(dirs, strings.TrimPrefix(line, "use ./"))
		}
	}
	modules := map[string]*workspaceModule{}
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(root, dir, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		module := &workspaceModule{dir: dir, listed: map[string]bool{}, imports: map[string]bool{}, packages: map[string]map[string]bool{}}
		inRequire := false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
			switch {
			case strings.HasPrefix(line, "module "):
				module.path = strings.TrimPrefix(strings.TrimPrefix(line, "module "), siblingPrefix)
			case line == "require (":
				inRequire = true
			case line == ")":
				inRequire = false
			case strings.HasPrefix(line, "require "):
				module.noteRequire(strings.TrimPrefix(line, "require "))
			case inRequire && line != "":
				module.noteRequire(line)
			}
		}
		modules[module.path] = module
	}
	// Collect each module's own imports of sibling packages, tests included,
	// since `go vet ./...` compiles them.
	for _, module := range modules {
		base := filepath.Join(root, module.dir)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path != base {
					name := entry.Name()
					if name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
						return filepath.SkipDir // the go tool ignores these
					}
					if _, nested := os.Stat(filepath.Join(path, "go.mod")); nested == nil {
						return filepath.SkipDir // another module
					}
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(base, filepath.Dir(path))
			if err != nil {
				return err
			}
			pkg := module.path
			if relative != "." {
				pkg += "/" + filepath.ToSlash(relative)
			}
			isTest := strings.HasSuffix(path, "_test.go")
			for _, spec := range parsed.Imports {
				value, _ := strconv.Unquote(spec.Path.Value)
				if rest, ok := strings.CutPrefix(value, siblingPrefix); ok {
					module.imports[rest] = true
					if !isTest {
						if module.packages[pkg] == nil {
							module.packages[pkg] = map[string]bool{}
						}
						module.packages[pkg][rest] = true
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return modules
}

func (m *workspaceModule) noteRequire(spec string) {
	fields := strings.Fields(spec)
	if len(fields) < 2 {
		return
	}
	m.hasReqs = true
	if rest, ok := strings.CutPrefix(fields[0], siblingPrefix); ok {
		m.listed[rest] = true
	}
}

// owner returns the workspace module that holds a package path.
func owner(modules map[string]*workspaceModule, pkg string) string {
	best := ""
	for path := range modules {
		if (pkg == path || strings.HasPrefix(pkg, path+"/")) && len(path) > len(best) {
			best = path
		}
	}
	return best
}

func TestModulesRequireWhatTheyImport(t *testing.T) {
	modules := workspaceModules(t)
	// reach[m] is every sibling module whose packages m's build can reach: the
	// packages m's own files import (tests included), then the non-test imports
	// of those packages, and so on. Go's pruned module graph needs all of those
	// modules in m's go.mod.
	reach := map[string]map[string]bool{}
	for path, module := range modules {
		seenPackages := map[string]bool{}
		seenModules := map[string]bool{}
		queue := []string{}
		for pkg := range module.imports {
			queue = append(queue, pkg)
		}
		for len(queue) > 0 {
			pkg := queue[0]
			queue = queue[1:]
			if seenPackages[pkg] {
				continue
			}
			seenPackages[pkg] = true
			dep := owner(modules, pkg)
			if dep == "" {
				continue
			}
			if dep != path {
				seenModules[dep] = true
			}
			for next := range modules[dep].packages[pkg] {
				queue = append(queue, next)
			}
		}
		reach[path] = seenModules
	}
	var names []string
	for path := range modules {
		names = append(names, path)
	}
	sort.Strings(names)
	for _, path := range names {
		module := modules[path]
		if productModules[module.dir] {
			continue
		}
		for dep := range reach[path] {
			if !module.listed[dep] {
				t.Errorf("%s: go.mod does not require %s, which its build reaches", module.dir, dep)
			}
		}
		if module.hasReqs {
			info, err := os.Stat(filepath.Join(repoRoot(t), module.dir, "go.sum"))
			if err != nil || info.Size() == 0 {
				t.Errorf("%s: go.mod has requirements but there is no go.sum", module.dir)
			}
		}
	}
}
