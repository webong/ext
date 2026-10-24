// Command tools writes the Go translation of the C engine's portable core: the sources listed as
// core_sources in lib/plugin/CMakeLists.txt, run through ccgo for the platform it runs on.
//
//	GOWORK=off go run . [-out file]
//
// The result is internal/ccgo/generated_<goos>_<goarch>.go. ccgo translates for the machine it runs
// on, so each supported platform is generated on that platform (the c2goengine workflow does it for
// every one and compares the result with what is checked in). Needs a C compiler on the path, to
// preprocess, and a Go toolchain with gofmt, which ccgo runs on its output.
package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"go/build/constraint"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

	ccgo "modernc.org/ccgo/v4/lib"
)

//go:embed shim
var shims embed.FS

// The translation is made from a copy of the sources with these changes, which keep the repository's
// own files as they are. Each is checked, so a source that moves out from under one is an error and
// not a silently different translation.
var edits = []struct{ file, old, new string }{
	// yyjson leaves compiler barriers as inline assembly for GCC; ccgo translates no assembly.
	{"vendor/yyjson.h", "#   define YYJSON_IS_REAL_GCC 1", "#   define YYJSON_IS_REAL_GCC 0"},
	// ... and 128-bit integers, which ccgo does not support everywhere (not on windows/arm64). -U on
	// the compiler's own macro is not honored on every platform, so the switch is set in the source.
	{"vendor/yyjson.c", "#    define YYJSON_HAS_INT128 1", "#    define YYJSON_HAS_INT128 0"},
}

// defines and flags passed to ccgo, besides the include directories and the sources. They give the
// translator plain C where yyjson would use compiler extensions: no alignment or inlining attributes
// and no 128-bit integers (the last is also set in the source, see edits).
var flags = []string{
	"-DEXT_HOST_STATIC", "-DEXT_HOST_BUILD",
	"-Dyyjson_align(x)=", "-Dyyjson_inline=", "-Dyyjson_noinline=", "-U__SIZEOF_INT128__",
}

// libcVersion is the version of modernc.org/libc the generated code is written for. The tools and
// c2goengine modules both require exactly this version, and the generator refuses to run when they
// do not.
const libcVersion = "v1.79.0"

var coreSources = regexp.MustCompile(`(?s)set\(core_sources\s+([^)]*)\)`)

func main() {
	log := func(format string, args ...any) { fmt.Fprintf(os.Stderr, "c2goengine: "+format+"\n", args...) }
	out := flag.String("out", "", "file to write (default internal/ccgo/generated_<goos>_<goarch>.go in the module)")
	flag.Parse()
	root, err := repositoryRoot()
	if err != nil {
		log("%v", err)
		os.Exit(1)
	}
	pkg := filepath.Join(root, "pkg", "plugin-go", "c2goengine", "internal", "ccgo")
	sync := *out == ""
	if sync {
		*out = filepath.Join(pkg, fmt.Sprintf("generated_%s_%s.go", runtime.GOOS, runtime.GOARCH))
	}
	digest, err := generate(root, *out)
	if err != nil {
		log("%v", err)
		os.Exit(1)
	}
	log("wrote %s (inputs %s)", *out, digest[:12])
	// Writing into the module also brings the build constraints of call.go and call_unsupported.go
	// in line with the files that exist, so a platform joins by running this on it.
	if sync {
		if err := syncTargets(pkg); err != nil {
			log("%v", err)
			os.Exit(1)
		}
		// One digest for all platforms: each generated file names the one it was made from, and a
		// test requires them to agree.
		if err := os.WriteFile(filepath.Join(pkg, "inputs.sha256"), []byte(digest+"\n"), 0o644); err != nil {
			log("%v", err)
			os.Exit(1)
		}
	}
}

var generatedFile = regexp.MustCompile(`^generated_([a-z0-9]+)_([a-z0-9]+)\.go$`)
var buildLine = regexp.MustCompile(`(?m)^//go:build .*$`)

// targetsExpr is the build constraint that holds on exactly the platforms that have a generated file.
func targetsExpr(files []string) (constraint.Expr, error) {
	var targets []string
	for _, f := range files {
		if m := generatedFile.FindStringSubmatch(filepath.Base(f)); m != nil {
			targets = append(targets, m[1]+" "+m[2])
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no generated file")
	}
	sort.Strings(targets)
	var expr constraint.Expr
	for _, t := range targets {
		goos, goarch, _ := strings.Cut(t, " ")
		term := &constraint.AndExpr{X: &constraint.TagExpr{Tag: goos}, Y: &constraint.TagExpr{Tag: goarch}}
		if expr == nil {
			expr = term
		} else {
			expr = &constraint.OrExpr{X: expr, Y: term}
		}
	}
	return expr, nil
}

// syncTargets rewrites the //go:build line of call.go to the platforms with a generated file and
// that of call_unsupported.go to all the others.
func syncTargets(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "generated_*.go"))
	if err != nil {
		return err
	}
	expr, err := targetsExpr(files)
	if err != nil {
		return err
	}
	for file, line := range map[string]string{
		"call.go":             "//go:build " + expr.String(),
		"call_unsupported.go": "//go:build " + (&constraint.NotExpr{X: expr}).String(),
	} {
		path := filepath.Join(dir, file)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !buildLine.Match(data) {
			return fmt.Errorf("%s has no //go:build line", file)
		}
		updated := buildLine.ReplaceAllLiteral(data, []byte(line))
		if !bytes.Equal(updated, data) {
			if err := os.WriteFile(path, updated, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// repositoryRoot finds the directory that holds lib/plugin, from the working directory up.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "lib", "plugin", "CMakeLists.txt")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("lib/plugin/CMakeLists.txt not found above the working directory")
		}
		dir = parent
	}
}

func generate(root, out string) (digest string, err error) {
	plugin := filepath.Join(root, "lib", "plugin")
	cmake, err := os.ReadFile(filepath.Join(plugin, "CMakeLists.txt"))
	if err != nil {
		return "", err
	}
	match := coreSources.FindSubmatch(cmake)
	if match == nil {
		return "", fmt.Errorf("core_sources not found in lib/plugin/CMakeLists.txt")
	}
	sources := strings.Fields(string(match[1]))
	if len(sources) == 0 {
		return "", fmt.Errorf("core_sources is empty")
	}

	work, err := os.MkdirTemp("", "c2goengine-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	for _, dir := range []string{"src", "include", "vendor"} {
		if err := copyTree(filepath.Join(plugin, dir), filepath.Join(work, "csrc", dir)); err != nil {
			return "", err
		}
	}
	if err := copyFS(shims, "shim", filepath.Join(work, "csrc", "shim")); err != nil {
		return "", err
	}
	for _, e := range edits {
		path := filepath.Join(work, "csrc", filepath.FromSlash(e.file))
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if n := bytes.Count(data, []byte(e.old)); n != 1 {
			return "", fmt.Errorf("%s: %q occurs %d times, want once; the source changed under the translation", e.file, e.old, n)
		}
		if err := os.WriteFile(path, bytes.Replace(data, []byte(e.old), []byte(e.new), 1), 0o644); err != nil {
			return "", err
		}
	}

	// The digest names what the translation is made from: the sources as ccgo sees them (after the
	// edits and with the shims), the flags, and the versions of the translator and its runtime. It
	// does not depend on the platform, so a stale translation is one made from another digest, whatever
	// bytes the platform's headers happen to add to it.
	digest, err = inputsDigest(filepath.Join(work, "csrc"), sources)
	if err != nil {
		return "", err
	}

	// ccgo type-checks what it writes, so it needs a module that provides the runtime library the
	// output imports: the version the c2goengine module must require.
	module := "module c2goengine/generate\n\ngo 1.26.0\n\nrequire modernc.org/libc " + libcVersion + "\n"
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(module), 0o644); err != nil {
		return "", err
	}
	sums, err := os.ReadFile(filepath.Join(root, "pkg", "plugin-go", "c2goengine", "tools", "go.sum"))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(work, "go.sum"), sums, 0o644); err != nil {
		return "", err
	}
	if err := checkRuntimeVersion(root, libcVersion); err != nil {
		return "", err
	}

	// ccgo works from the directory it is given and writes that directory's relative paths into its
	// output, so the same sources give the same bytes wherever the copy lives.
	args := []string{"ccgo", "-o", "engine.go", "--package-name", "ccgo",
		"-I", "csrc/shim", "-I", "csrc/src", "-I", "csrc/vendor", "-I", "csrc/include"}
	args = append(args, flags...)
	for _, s := range sources {
		args = append(args, "csrc/"+s)
	}
	previous, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if err := os.Chdir(work); err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	err = ccgo.NewTask(runtime.GOOS, runtime.GOARCH, args, os.Stdout, &stderr, nil).Main()
	if chdir := os.Chdir(previous); chdir != nil && err == nil {
		err = chdir
	}
	if err != nil {
		return "", fmt.Errorf("ccgo: %v\n%s", err, tail(stderr.String(), 40))
	}
	generated, err := os.ReadFile(filepath.Join(work, "engine.go"))
	if err != nil {
		return "", err
	}
	first, rest, _ := bytes.Cut(generated, []byte("\n"))
	if !bytes.HasPrefix(first, []byte("// Code generated ")) || !bytes.HasSuffix(first, []byte("DO NOT EDIT.")) {
		return "", fmt.Errorf("the output does not start with a generated-code marker")
	}
	// The digest goes on the line after the marker, where internal/ccgo's tests read it.
	var marked bytes.Buffer
	marked.Write(first)
	marked.WriteString("\n" + inputsMarker + digest + "\n")
	marked.Write(rest)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	return digest, os.WriteFile(out, marked.Bytes(), 0o644)
}

// inputsMarker starts the line of a generated file that names the digest it was made from.
const inputsMarker = "// c2goengine inputs sha256:"

// inputsDigest hashes the translation's inputs: every file under csrc in a fixed order, the flags,
// the source list, and the versions of ccgo and libc.
func inputsDigest(csrc string, sources []string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "libc %s\nflags %q\nsources %q\n", libcVersion, flags, sources)
	ccgoVersion := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "modernc.org/ccgo/v4" {
				ccgoVersion = dep.Version
			}
		}
	}
	fmt.Fprintf(h, "ccgo %s\n", ccgoVersion)
	var files []string
	err := filepath.WalkDir(csrc, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(csrc, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(csrc, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "%s %s\n", rel, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func tail(s string, lines int) string {
	all := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func copyFS(fsys fs.FS, from, to string) error {
	return fs.WalkDir(fsys, from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, from), "/")
		target := filepath.Join(to, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// checkRuntimeVersion requires the c2goengine module to depend on the runtime library the output
// was written for: another version is another ABI of the same names.
func checkRuntimeVersion(root, libc string) error {
	want := "modernc.org/libc " + libc
	for _, file := range []string{"go.mod", "tools/go.mod"} {
		data, err := os.ReadFile(filepath.Join(root, "pkg", "plugin-go", "c2goengine", filepath.FromSlash(file)))
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), want) {
			return fmt.Errorf("pkg/plugin-go/c2goengine/%s must require %s: update both go.mod files and libcVersion together", file, want)
		}
	}
	return nil
}
