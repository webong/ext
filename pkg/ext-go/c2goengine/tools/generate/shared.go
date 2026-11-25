package generate

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed shim
var shims embed.FS

// The embedded copy lets the compiler verify cached dependencies outside the
// tools checkout. TestCompilerChecksumsMatchModule guards it against drift.
//
//go:embed dependencies.sum
var compilerSums []byte

// libcVersion is the version of modernc.org/libc the generated code is written for. The tools and
// c2goengine modules both require exactly this version, and the generator refuses to run when they
// do not.
const libcVersion = "v1.79.0"

// repositoryRoot finds the directory that holds lib/plugin, from the working directory up.
func repositoryRoot(dir string) (string, error) {
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

// prepareTranslationModule provides ccgo's pinned runtime without depending on
// the caller's Go workspace or on files beside an installed generator.
// CompilerVersion and RuntimeVersion pin the build-time translator and generated runtime.
const CompilerVersion = "v4.38.0"
const RuntimeVersion = libcVersion

func prepareTranslationModule(work, name string) error {
	module := "module " + name + "\n\ngo 1.26.0\n\nrequire (\n modernc.org/ccgo/v4 " + CompilerVersion + "\n modernc.org/libc " + RuntimeVersion + "\n)\n"
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(module), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(work, "go.sum"), compilerSums, 0644)
}

// The worker uses the same v4 library as the former in-process compiler, not
// ccgo's CLI (which also imports its legacy v3 compiler).
const compilerWorker = `package main
import (
 "fmt"
 "os"
 "runtime"
 ccgo "modernc.org/ccgo/v4/lib"
)
func main() {
 args := append([]string{"ccgo"}, os.Args[1:]...)
 if err := ccgo.NewTask(runtime.GOOS,runtime.GOARCH,args,os.Stdout,os.Stderr,nil).Main(); err != nil {
  fmt.Fprintln(os.Stderr,err)
  os.Exit(1)
 }
}
`

// Build and run a private worker so generation never changes the caller's cwd.
func runCCGO(ctx context.Context, work string, args []string, log io.Writer) error {
	dir := filepath.Join(work, "worker")
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(compilerWorker), 0644); err != nil {
		return err
	}
	executable := filepath.Join(work, "ccgo-worker")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	commands := []*exec.Cmd{
		exec.CommandContext(ctx, "go", "build", "-mod=mod", "-o", executable, "./worker"),
		exec.CommandContext(ctx, executable, args[1:]...),
	}
	for _, command := range commands {
		command.Dir = work
		command.Env = append(os.Environ(), "GOWORK=off", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
		command.Stdout = log
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("ccgo: %w\n%s", err, tail(stderr.String(), 50))
		}
	}
	return ctx.Err()
}
