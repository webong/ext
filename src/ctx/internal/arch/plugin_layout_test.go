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
//	engine      legacy Go implementations and shared-contract compatibility exports
//	cgoengine   the existing plugin API: linked C or the default shared native host
//	c2goengine  the C engine's portable core translated to Go; needs engine
//	wasm        the engine binding for pkg/ext-go/core/wasm; needs engine and res/web/host
//	hashicorp   the RPC backend uses core; its examples and tests retain engine
//	yaegi       the Go source interpreter backend; needs engine and yaegi only, never cgoengine
//	core        shared plugin/ledger contracts and driver selection; standard library only
//	cengine     the C side: library opens the built copy purego needs, cguest exchanges bytes
//	            with C ABI guests; its guest and ledger bindings consume shared core contracts
//	(umbrella)  pkg/ext-go itself, the one module that may require them all
//	res/web/host  portable web and WebAssembly tooling; never imports the plugin engine
const pluginPrefix = "github.com/webong/ext/pkg/ext-go"

// A module's allowed requirements among the repository's own modules.
var pluginRequires = map[string][]string{
	"pkg/ext-go/engine":     {"pkg/ext-go/core"},
	"pkg/ext-go/cgoengine":  {"pkg/ext-go/core", "pkg/ext-go/engine", "pkg/ext-go/cengine"},
	"pkg/ext-go/c2goengine": {"pkg/ext-go/core", "pkg/ext-go/engine", "pkg/ext-go/cengine"},
	"pkg/ext-go/wasm":       {"pkg/ext-go/core", "pkg/ext-go/engine", "res/web/host"},
	"pkg/ext-go/hashicorp":  {"pkg/ext-go/core", "pkg/ext-go/engine"},
	"pkg/ext-go/yaegi":      {"pkg/ext-go/core", "pkg/ext-go/engine"},
	"pkg/ext-go/core":       {},
	"pkg/ext-go/cengine":    {"pkg/ext-go/core", "pkg/ext-go/engine"},
	"src/ctn/inference":     {"pkg/ext-go/core", "pkg/ext-go/engine", "pkg/ext-go/cengine", "src/ctn/contract"},
	"src/ctn/model":         {"pkg/ext-go/core", "pkg/ext-go/engine", "src/ctn/contract"},
	"src/ctn/perception":    {"pkg/ext-go/core", "pkg/ext-go/engine", "src/ctn/contract"},
	"src/ctn/realtime":      {"pkg/ext-go/core", "pkg/ext-go/engine", "src/ctn/contract"},
	"src/ctn/contract":      {},
	"src/cto/sandbox":       {},
	"res/web/host":          {},
	"res/system":            {},
	"pkg/ext-go":            {"pkg/ext-go/engine", "pkg/ext-go/cgoengine", "pkg/ext-go/c2goengine", "pkg/ext-go/wasm", "pkg/ext-go/hashicorp", "pkg/ext-go/core", "pkg/ext-go/cengine", "res/web/host", "res/system"},
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
	for _, dir := range []string{"pkg/ext-go/wasm", "pkg/ext-go/hashicorp", "pkg/ext-go/yaegi", "pkg/ext-go/cengine", "pkg/ext-go/core", "src/ctn/inference", "src/ctn/model", "src/ctn/perception", "src/ctn/realtime"} {
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
						t.Errorf("%s imports %s; a backend uses core contracts and portable engine helpers, never C driver bindings", filepath.ToSlash(rel), name)
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
		if fields[0] != "github.com/traefik/yaegi" && fields[0] != pluginPrefix+"/engine" && fields[0] != pluginPrefix+"/core" {
			got = append(got, fields[0])
		}
	}
	if len(got) > 0 {
		t.Errorf("pkg/ext-go/yaegi requires %v; it may require only core, engine and github.com/traefik/yaegi", got)
	}
}

// The portable plugin engine uses the standard library and shared core plugin contracts.
func TestPluginEngineUsesOnlyTheStandardLibrary(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "pkg/ext-go/engine/plugin"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir // fixtures, including guests that import backends
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil && path != filepath.Join(root, "pkg/ext-go/engine") {
				return filepath.SkipDir // a nested module has its own rules
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
			if strings.Contains(first, ".") && !strings.HasPrefix(name, pluginPrefix+"/engine") && name != pluginPrefix+"/core/plugin" && !strings.HasPrefix(name, pluginPrefix+"/core/plugin/") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s; the portable plugin engine uses only the standard library and core/plugin", filepath.ToSlash(rel), name)
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
		base := "pkg/ext-go/engine/plugin"
		if name == "host" {
			base = "pkg/ext-go/cengine/plugin"
		}
		pure := exportedAPI(t, filepath.Join(root, base, name))
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

// core/driver and the generic C tooling stay independent of plugin behavior.
// The native guest and ledger bindings may consume shared contracts; generic loading stays independent.
func TestSwitcherAndCSideKnowNoLibrary(t *testing.T) {
	root := repoRoot(t)
	// The umbrella published a package at pkg/ext-go/core up to v0.12.0, so the core module has no
	// root package: a program that requires both never sees an ambiguous import.
	if matches, _ := filepath.Glob(filepath.Join(root, "pkg/ext-go/core", "*.go")); len(matches) > 0 {
		t.Errorf("pkg/ext-go/core has Go files at its root (%v); the core module has no root package", matches)
	}
	for dir, own := range map[string]string{"pkg/ext-go/core/driver": pluginPrefix + "/core/driver", "pkg/ext-go/cengine": pluginPrefix + "/cengine/"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr == nil && entry.IsDir() && (path == filepath.Join(root, "pkg/ext-go/cengine/cguest/backend") || path == filepath.Join(root, "pkg/ext-go/cengine/cguest/guest") || path == filepath.Join(root, "pkg/ext-go/cengine/ledger") || path == filepath.Join(root, "pkg/ext-go/cengine/plugin")) {
				return filepath.SkipDir
			}
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
				return walkErr
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasPrefix(name, "github.com/webong/ext/") && (!strings.HasPrefix(name, own) || strings.HasPrefix(name, pluginPrefix+"/cengine/cguest/backend") || strings.HasPrefix(name, pluginPrefix+"/cengine/cguest/guest")) {
					rel, _ := filepath.Rel(root, path)
					t.Errorf("%s imports %s; the switcher and the C side know no library", filepath.ToSlash(rel), name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Shared contracts cannot import either implementation, and the Go engine
// cannot reach the native binding. Cengine may build on core and engine.
func TestEngineLayersHaveNoBackEdges(t *testing.T) {
	root := repoRoot(t)
	for _, layer := range []string{"core", "engine", "cengine"} {
		base := filepath.Join(root, "pkg/ext-go", layer)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != base {
					if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasPrefix(name, "github.com/webong/ext/res/system") {
					t.Errorf("%s imports OS substrate %s; SDK bindings use ledger wire records", path, name)
				}
				forbidden := []string{}
				if layer != "cengine" {
					forbidden = []string{"cengine", "cgoengine", "c2goengine"}
				}
				if layer == "core" {
					forbidden = append(forbidden, "engine")
				}
				for _, target := range forbidden {
					prefix := pluginPrefix + "/" + target
					if name == prefix || strings.HasPrefix(name, prefix+"/") {
						t.Errorf("%s imports %s against core -> engine -> cengine layering", path, name)
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
