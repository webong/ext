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

// pkg/plugin-go is a tree of modules, not one module. Each heavy part is its own
// module so an application imports only what it needs:
//
//	contract    standard library only
//	cgoengine   the C engine binding and pure-Go build of the same API; needs contract
//	c2goengine  the C engine's portable core translated to Go; needs contract
//	wasm        the wazero backend; needs contract only, never cgoengine
//	hashicorp   the gRPC backend; needs contract only, never cgoengine
//	(umbrella)  pkg/plugin-go itself, the one module that may require them all; its engine package
//	            chooses between contract, cgoengine and c2goengine
const pluginPrefix = "github.com/webong/ext/pkg/plugin-go"

// A module's allowed requirements among the repository's own modules.
var pluginRequires = map[string][]string{
	"pkg/plugin-go/contract":   {},
	"pkg/plugin-go/cgoengine":  {"pkg/plugin-go/contract"},
	"pkg/plugin-go/c2goengine": {"pkg/plugin-go/contract"},
	"pkg/plugin-go/wasm":       {"pkg/plugin-go/contract"},
	"pkg/plugin-go/hashicorp":  {"pkg/plugin-go/contract"},
	"pkg/plugin-go":            {"pkg/plugin-go/contract", "pkg/plugin-go/cgoengine", "pkg/plugin-go/c2goengine", "pkg/plugin-go/wasm", "pkg/plugin-go/hashicorp", "res/system"},
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

// The backends reach the contract and nothing of the C engine, in source as well
// as in go.mod: a wazero or gRPC user must not need a C toolchain or even have the
// engine binding in the module graph.
func TestBackendsNeverImportTheCEngine(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"pkg/plugin-go/wasm", "pkg/plugin-go/hashicorp"} {
		forbidden := []string{pluginPrefix + "/cgoengine", pluginPrefix + "/c2goengine", pluginPrefix + "/engine", pluginPrefix}
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
						t.Errorf("%s imports %s; a backend depends on pkg/plugin-go/contract only", filepath.ToSlash(rel), name)
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

// pkg/plugin-go/contract is the lean choice: standard library only.
func TestContractModuleUsesOnlyTheStandardLibrary(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "pkg/plugin-go/contract"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir // fixtures, including guests that import backends
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
			if strings.Contains(first, ".") && !strings.HasPrefix(name, pluginPrefix+"/contract") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s; the contract module uses only the standard library", filepath.ToSlash(rel), name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// contract/host and contract/host/store are the pure-Go implementations an
// application takes with the contract module alone; cgoengine/host and
// cgoengine/host/store are the same packages routed through the engine. Swapping one import for the other must never
// need a code change, so their exported surfaces must be identical.
func TestEngineVariantsKeepTheSameAPI(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"host", "host/store"} {
		pure := exportedAPI(t, filepath.Join(root, "pkg/plugin-go/contract", name))
		engine := exportedAPI(t, filepath.Join(root, "pkg/plugin-go/cgoengine", name))
		for line := range pure {
			if !engine[line] {
				t.Errorf("%s: in contract but not in cgoengine: %s", name, line)
			}
		}
		for line := range engine {
			if !pure[line] {
				t.Errorf("%s: in cgoengine but not in contract: %s", name, line)
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

// The contract module is layered. transport (how frames travel) and validate (how values are
// checked) are leaves: they import neither host nor guest, and not each other. host (what runs
// plugins on a machine) and guest (what a plugin implementation uses) build on them but never
// import each other: a plugin must not need host code, and a host must not need a plugin's
// authoring helpers.
func TestContractGroupsKeepTheirLayering(t *testing.T) {
	root := repoRoot(t)
	forbidden := map[string][]string{
		"host":      {"guest"},
		"guest":     {"host"},
		"transport": {"host", "guest", "validate"},
		"validate":  {"host", "guest", "transport"},
	}
	for group, others := range forbidden {
		for _, other := range others {
			banned := pluginPrefix + "/contract/" + other
			err := filepath.WalkDir(filepath.Join(root, "pkg/plugin-go/contract", group), func(path string, entry fs.DirEntry, walkErr error) error {
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
						t.Errorf("%s imports %s; contract/%s must not import contract/%s", filepath.ToSlash(rel), name, group, other)
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
