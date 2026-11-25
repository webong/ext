// Plugin-specific translation recipe.
package generate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

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

// hostSources are translated besides core_sources on a platform that has the host: the engine's
// session, filesystem, instance and stream code with its POSIX parts. The channel layer
// (channel_posix.c) is replaced by shim/channel_go.c and the Go code in plugin/internal/ccgo. Windows has the
// core only.
var hostSources = []string{"src/host.c", "src/fs_posix.c", "src/random_posix.c", "src/integrity.c", "src/instances.c", "src/streams.c"}
var hostShims = []string{"shim/channel_go.c", "shim/missing.c"}

// windowsShims are translated on Windows only; see shim/windows.c.
var windowsShims = []string{"shim/windows.c"}

// hasHost reports whether the platform gets the host profile.
func hasHost(goos string) bool { return goos != "windows" }

var coreSources = regexp.MustCompile(`(?s)set\(core_sources\s+([^)]*)\)`)

func generatePlugin(ctx context.Context, log io.Writer, root, out string) (digest string, err error) {
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
	if err := prepareTranslationModule(work, "c2goengine/generate"); err != nil {
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
	if err := runCCGO(ctx, work, args, log); err != nil {
		return "", err
	}
	generated, err := os.ReadFile(filepath.Join(work, "engine.go"))
	if err != nil {
		return "", err
	}
	first, rest, _ := bytes.Cut(generated, []byte("\n"))
	if !bytes.HasPrefix(first, []byte("// Code generated ")) || !bytes.HasSuffix(first, []byte("DO NOT EDIT.")) {
		return "", fmt.Errorf("the output does not start with a generated-code marker")
	}
	// The digest goes on the line after the marker, where plugin/internal/ccgo's tests read it.
	var marked bytes.Buffer
	marked.Write(first)
	marked.WriteString("\n" + inputsMarker + digest + "\n")
	marked.Write(rest)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	return digest, os.WriteFile(out, marked.Bytes(), 0o644)
}
