// Command tools writes the Go translation of the C engine's portable core: the sources listed as
// core_sources in lib/plugin/CMakeLists.txt, run through ccgo for the platform it runs on.
//
//	GOWORK=off go run . [-out file]
//
// The result is internal/ccgo/generated_<goos>_<goarch>.go, which is not committed: ccgo translates
// for the machine it runs on, so each supported platform is generated on that platform, by the
// ext-plugin release (which publishes it with the hand-written binding beside it as the c2go
// driver's archive) and by the c2goengine workflow, which tests it. The binding builds only with
// -tags ext_c2go, once a translation is there. Needs a C compiler on the path, to preprocess, and a
// Go toolchain with gofmt, which ccgo runs on its output.
package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"go/build"
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
	"-DEXT_PLUGIN_HOST_STATIC", "-DEXT_PLUGIN_HOST_BUILD",
	"-Dyyjson_align(x)=", "-Dyyjson_inline=", "-Dyyjson_noinline=", "-U__SIZEOF_INT128__",
}

// libcVersion is the version of modernc.org/libc the generated code is written for. The tools and
// c2goengine modules both require exactly this version, and the generator refuses to run when they
// do not.
const libcVersion = "v1.79.0"

// hostSources are translated besides core_sources on a platform that has the host: the engine's
// session, filesystem, instance and stream code with its POSIX parts. The channel layer
// (channel_posix.c) is replaced by shim/channel_go.c and the Go code in internal/ccgo. Windows has the
// core only.
var hostSources = []string{"src/host.c", "src/fs_posix.c", "src/random_posix.c", "src/integrity.c", "src/instances.c", "src/streams.c"}
var hostShims = []string{"shim/channel_go.c", "shim/missing.c"}

// windowsShims are translated on Windows only; see shim/windows.c.
var windowsShims = []string{"shim/windows.c"}

// hasHost reports whether the platform gets the host profile.
func hasHost(goos string) bool { return goos != "windows" }

var coreSources = regexp.MustCompile(`(?s)set\(core_sources\s+([^)]*)\)`)

func main() {
	log := func(format string, args ...any) { fmt.Fprintf(os.Stderr, "c2goengine: "+format+"\n", args...) }
	out := flag.String("out", "", "file to write (default internal/ccgo/generated_<goos>_<goarch>.go in the module)")
	publish := flag.String("publish", "", "also write the c2go driver's Go directory here: the translation and the binding beside it, for this platform")
	flag.Parse()
	root, err := repositoryRoot()
	if err != nil {
		log("%v", err)
		os.Exit(1)
	}
	pkg := filepath.Join(root, "pkg", "ext-go", "c2goengine", "internal", "ccgo")
	if *out == "" {
		*out = filepath.Join(pkg, fmt.Sprintf("generated_%s_%s.go", runtime.GOOS, runtime.GOARCH))
	}
	digest, err := generate(root, *out)
	if err != nil {
		log("%v", err)
		os.Exit(1)
	}
	log("wrote %s (inputs %s)", *out, digest[:12])
	if *publish != "" {
		n, err := publishTranslation(pkg, *publish, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			log("%v", err)
			os.Exit(1)
		}
		log("published %d files for %s/%s in %s", n, runtime.GOOS, runtime.GOARCH, *publish)
	}
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
	host := hasHost(runtime.GOOS)
	if host {
		sources = append(sources, hostSources...)
		sources = append(sources, hostShims...)
	}
	if runtime.GOOS == "windows" {
		sources = append(sources, windowsShims...)
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
	// Hashed as the same list on every platform, whichever of it a platform translates.
	everything := append(append(append(append([]string{}, strings.Fields(string(match[1]))...), hostSources...), hostShims...), windowsShims...)
	digest, err = inputsDigest(filepath.Join(work, "csrc"), everything)
	if err != nil {
		return "", err
	}

	// ccgo type-checks what it writes, so it needs a module that provides the runtime library the
	// output imports: the version the c2goengine module must require.
	module := "module c2goengine/generate\n\ngo 1.26.0\n\nrequire modernc.org/libc " + libcVersion + "\n"
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(module), 0o644); err != nil {
		return "", err
	}
	sums, err := os.ReadFile(filepath.Join(root, "pkg", "ext-go", "c2goengine", "tools", "go.sum"))
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
	args = append(args, "-include", "csrc/shim/compat_core.h")
	if host {
		args = append(args, "-include", "csrc/shim/compat.h")
	}
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

// inputsDigest hashes the translation's inputs: the listed sources and every header under csrc in a
// fixed order, the flags, the source list, and the versions of ccgo and libc. A source the translator
// is not given (the store and trust code, the Windows host) is not an input, so adding or editing one
// does not make the translations stale; a header is, because any listed source may include it.
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
	// Only the C the translator reads counts, and without its comments (see stripComments): a README,
	// a license or a reworded comment does not change the translation, so it must not turn the
	// workflow red.
	fmt.Fprintf(h, "inputs: the listed sources and every header, comments and blank space stripped\n")
	listed := map[string]bool{}
	for _, s := range sources {
		listed[s] = true
	}
	var files []string
	err := filepath.WalkDir(csrc, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isCSource(path) {
			return err
		}
		rel, err := filepath.Rel(csrc, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if filepath.Ext(rel) == ".c" {
			if !listed[rel] {
				return nil
			}
			delete(listed, rel)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(listed) > 0 {
		missing := make([]string, 0, len(listed))
		for s := range listed {
			missing = append(missing, s)
		}
		sort.Strings(missing)
		return "", fmt.Errorf("listed sources are not in the copied tree: %s", strings.Join(missing, ", "))
	}
	sort.Strings(files)
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(csrc, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(canonicalSource(data))
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
		data, err := os.ReadFile(filepath.Join(root, "pkg", "ext-go", "c2goengine", filepath.FromSlash(file)))
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), want) {
			return fmt.Errorf("pkg/ext-go/c2goengine/%s must require %s: update both go.mod files and libcVersion together", file, want)
		}
	}
	return nil
}

// publishTranslation writes the c2go driver's Go directory for goos/goarch into dir: every file of
// the package that builds there with -tags ext_c2go (the translation and the binding beside it),
// each named for the platform (call.go becomes call_<goos>_<goarch>.go) and without its //go:build
// line, so the name alone constrains it. A project that fetches several targets gets one file per
// target and nothing clashes, and it needs no build tag. Test files are not published.
func publishTranslation(pkg, dir, goos, goarch string) (int, error) {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.BuildTags, ctx.CgoEnabled = goos, goarch, []string{"ext_c2go"}, false
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
