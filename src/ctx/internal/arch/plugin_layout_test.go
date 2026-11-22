package arch_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// pkg/ext-go is a tree of modules, not one module. Each heavy part is its own
// module so an application imports only what it needs:
//
//	engine      the pure-Go engine, the oracle and the shared data model; standard library only
//	            (plugin and ledger); its nested module engine/ledger/recorder, which projects
//	            res/system's observations onto the ledger, needs engine and res/system
//	cgoengine   the C engine binding and pure-Go build of the same API; needs engine
//	c2goengine  the C engine's portable core translated to Go; needs engine
//	wasm        the engine binding for res/web/host/engine/wasm; needs engine and res/web/host
//	hashicorp   the gRPC backend; needs engine only, never cgoengine
//	yaegi       the Go source interpreter backend; needs engine and yaegi only, never cgoengine
//	core        the switcher: driver picks go, purego, cgo or c2go for a library of lib/, and
//	            library finds, verifies and opens the built copy purego needs; purego only, no ext
//	            module, and knows no library by name
//	cshared     the backend for plugin guests built as C shared libraries; needs engine and core
//	(umbrella)  pkg/ext-go itself, the one module that may require them all; its plugin package
//	            chooses between engine, cgoengine and c2goengine through core/driver
//	res/web/host  portable web and WebAssembly tooling; never imports the plugin engine
const pluginPrefix = "github.com/webong/ext/pkg/ext-go"

// A module's allowed requirements among the repository's own modules.
var pluginRequires = map[string][]string{
	"pkg/ext-go/engine":                 {},
	"pkg/ext-go/cgoengine":              {"pkg/ext-go/engine"},
	"pkg/ext-go/c2goengine":             {"pkg/ext-go/engine"},
	"pkg/ext-go/wasm":                   {"pkg/ext-go/engine", "res/web/host"},
	"pkg/ext-go/hashicorp":              {"pkg/ext-go/engine"},
	"pkg/ext-go/yaegi":                  {"pkg/ext-go/engine"},
	"pkg/ext-go/core":                   {},
	"pkg/ext-go/cshared":                {"pkg/ext-go/engine", "pkg/ext-go/core"},
	"src/ctn/inference":                 {"pkg/ext-go/engine", "res/int"},
	"src/ctn/model":                     {"pkg/ext-go/engine", "res/int"},
	"src/ctn/perception":                {"pkg/ext-go/engine", "res/int"},
	"pkg/ext-go/realtime":               {"pkg/ext-go/engine", "res/int"},
	"res/int":                           {},
	"src/cto/sandbox":                   {},
	"res/web/host":                      {},
	"res/system":                        {},
	"pkg/ext-go/engine/ledger/recorder": {"pkg/ext-go/engine", "res/system"},
	"pkg/ext-go":                        {"pkg/ext-go/engine", "pkg/ext-go/cgoengine", "pkg/ext-go/c2goengine", "pkg/ext-go/wasm", "pkg/ext-go/hashicorp", "pkg/ext-go/realtime", "pkg/ext-go/core", "pkg/ext-go/cshared", "pkg/ext-go/engine/ledger/recorder", "res/web/host"},
}

var requireLine = regexp.MustCompile(`(?m)^\s*(?:require\s+)?(github\.com/webong/ext/\S+)\s+v\S+`)

func TestPluginGoModulesRequireOnlyTheirParts(t *testing.T) {
	root := repoRoot(t)
	for dir, allowed := range pluginRequires {
		data, err := os.ReadFile(filepath.Join(root, dir, "go.mod"))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		okay := map[string]bool{}
		for _, a := range allowed {
			okay["github.com/webong/ext/"+a] = true
		}
		var got []string
		for _, m := range requireLine.FindAllStringSubmatch(string(data), -1) {
			if strings.HasPrefix(m[1], "github.com/webong/ext/") && !okay[m[1]] {
				got = append(got, m[1])
			}
		}
		sort.Strings(got)
		if len(got) > 0 {
			t.Errorf("%s: go.mod requires %v; it may require only %v", dir, got, allowed)
		}
	}
}

// Runtime backends and CTN's provider bridges reach the engine module and
// nothing of the C engine. A wazero, gRPC, or CTN bridge user needs no C toolchain.
func TestBackendsNeverImportTheCEngine(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"pkg/ext-go/wasm", "pkg/ext-go/hashicorp", "pkg/ext-go/yaegi", "pkg/ext-go/cshared", "pkg/ext-go/core", "src/ctn/inference", "src/ctn/model", "src/ctn/perception", "pkg/ext-go/realtime"} {
		forbidden := []string{pluginPrefix + "/cgoengine", pluginPrefix + "/c2goengine", pluginPrefix + "/plugin", pluginPrefix}
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
				return walkErr
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				for _, f := range forbidden {
					if name == f || strings.HasPrefix(name, f+"/") && f != pluginPrefix {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s imports %s; a backend depends on pkg/ext-go/engine only", filepath.ToSlash(rel), name)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// pkg/ext-go/yaegi interprets guests in the host process, so what it links is
// what every interpreted guest runs beside: the engine, the standard library
// and yaegi itself, nothing else.
func TestYaegiBackendRequiresOnlyYaegi(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "pkg/ext-go/yaegi/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require "))
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") || !strings.Contains(fields[0], ".") {
			continue
		}
		if fields[0] != "github.com/traefik/yaegi" && fields[0] != pluginPrefix+"/engine" {
			got = append(got, fields[0])
		}
	}
	if len(got) > 0 {
		t.Errorf("pkg/ext-go/yaegi requires %v; it may require only the engine and github.com/traefik/yaegi", got)
	}
}

// pkg/ext-go/engine is the lean choice: standard library only.
func TestEngineModuleUsesOnlyTheStandardLibrary(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "pkg/ext-go/engine"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir // fixtures, including guests that import backends
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil && path != filepath.Join(root, "pkg/ext-go/engine") {
				return filepath.SkipDir // a nested module, such as engine/ledger/recorder, has its own rules
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(name, "/")
			if strings.Contains(first, ".") && !strings.HasPrefix(name, pluginPrefix+"/engine") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s; the engine module uses only the standard library", filepath.ToSlash(rel), name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// src/cto/sandbox is the contract that runners outside ext (github.com/webong/workload) depend on
// while staying on the standard library, so it imports only the standard library, nothing else
// in ext either, and its go.mod requires nothing.
func TestSandboxContractUsesOnlyTheStandardLibrary(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "src/cto/sandbox")
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "require") {
		t.Error("src/cto/sandbox/go.mod has a require; the contract module requires nothing")
	}
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return walkErr
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(name, "/")
			if strings.Contains(first, ".") && name != "github.com/webong/ext/src/cto/sandbox" && !strings.HasPrefix(name, "github.com/webong/ext/src/cto/sandbox/") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s; src/cto/sandbox uses only the standard library", filepath.ToSlash(rel), name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// engine/plugin/host and engine/plugin/host/store are the pure-Go implementations an
// application takes with the engine module alone; cgoengine/plugin/host and
// cgoengine/plugin/host/store are the same packages routed through the engine. Swapping one import for the other must never
// need a code change, so their exported surfaces must be identical.
func TestEngineVariantsKeepTheSameAPI(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"host", "host/store"} {
		pure := exportedAPI(t, filepath.Join(root, "pkg/ext-go/engine/plugin", name))
		engine := exportedAPI(t, filepath.Join(root, "pkg/ext-go/cgoengine/plugin", name))
		for line := range pure {
			if !engine[line] {
				t.Errorf("%s: in engine but not in cgoengine: %s", name, line)
			}
		}
		for line := range engine {
			if !pure[line] {
				t.Errorf("%s: in cgoengine but not in engine: %s", name, line)
			}
		}
		if len(pure) == 0 {
			t.Errorf("%s: no exported declarations found", name)
		}
	}
}

// exportedAPI describes every exported declaration of a package's non-test files:
// functions and methods with their signatures, types with their exported fields,
// constants and variables by name.
func exportedAPI(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	render := func(n any) string {
		var b bytes.Buffer
		if err := printer.Fprint(&b, fset, n); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(b.String()), " ")
	}
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if !d.Name.IsExported() {
						continue
					}
					recv := ""
					if d.Recv != nil {
						recv = render(d.Recv.List[0].Type)
						if !ast.IsExported(strings.TrimLeft(strings.SplitN(recv, "[", 2)[0], "*")) {
							continue
						}
						recv = "(" + recv + ") "
					}
					out["func "+recv+d.Name.Name+strings.TrimPrefix(render(d.Type), "func")] = true
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if !s.Name.IsExported() {
								continue
							}
							line := "type " + s.Name.Name
							if st, ok := s.Type.(*ast.StructType); ok {
								var fields []string
								for _, f := range st.Fields.List {
									for _, n := range f.Names {
										if n.IsExported() {
											fields = append(fields, n.Name+" "+render(f.Type))
										}
									}
								}
								sort.Strings(fields)
								line += " struct{" + strings.Join(fields, "; ") + "}"
							} else {
								line += " " + render(s.Type)
							}
							out[line] = true
						case *ast.ValueSpec:
							for _, n := range s.Names {
								if n.IsExported() {
									out[d.Tok.String()+" "+n.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	return out
}

// The plugin engine is layered. transport (how frames travel) and validate (how values are
// checked) are leaves: they import neither host nor guest, and not each other. host (what runs
// plugins on a machine) and guest (what a plugin implementation uses) build on them but never
// import each other: a plugin must not need host code, and a host must not need a plugin's
// authoring helpers.
func TestEngineGroupsKeepTheirLayering(t *testing.T) {
	root := repoRoot(t)
	forbidden := map[string][]string{
		"host":      {"guest"},
		"guest":     {"host"},
		"transport": {"host", "guest", "validate"},
		"validate":  {"host", "guest", "transport"},
	}
	for group, others := range forbidden {
		for _, other := range others {
			banned := pluginPrefix + "/engine/plugin/" + other
			err := filepath.WalkDir(filepath.Join(root, "pkg/ext-go/engine/plugin", group), func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
					return walkErr
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
				if err != nil {
					return err
				}
				for _, imp := range file.Imports {
					name, _ := strconv.Unquote(imp.Path.Value)
					if name == banned || strings.HasPrefix(name, banned+"/") {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s imports %s; engine/plugin/%s must not import engine/plugin/%s", filepath.ToSlash(rel), name, group, other)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// core/driver and core/library are generic: they know no library of lib/ by name, so they import
// nothing of ext. A library's own Go package lists its drivers and uses them.
func TestCoreSwitcherAndOpenerKnowNoLibrary(t *testing.T) {
	root := repoRoot(t)
	// The umbrella published a package at pkg/ext-go/core up to v0.12.0, so the core module has no
	// root package: a program that requires both never sees an ambiguous import.
	if matches, _ := filepath.Glob(filepath.Join(root, "pkg/ext-go/core", "*.go")); len(matches) > 0 {
		t.Errorf("pkg/ext-go/core has Go files at its root (%v); the core module has no root package", matches)
	}
	for _, dir := range []string{"pkg/ext-go/core/driver", "pkg/ext-go/core/library"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
				continue
			}
			path := filepath.Join(root, dir, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasPrefix(name, "github.com/webong/ext/") && !strings.HasPrefix(name, pluginPrefix+"/core/") {
					t.Errorf("%s/%s imports %s; the switcher and the opener know no library", dir, entry.Name(), name)
				}
			}
		}
	}
}
