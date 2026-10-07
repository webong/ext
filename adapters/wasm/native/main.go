// Command ctx-wasm is the built-in WebAssembly engine adapter. It needs no
// system runtime: modules run in the embedded wazero engine, sandboxed.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/webong/ext/pkg/plugin"
	wasm "github.com/webong/ext/pkg/plugin-wasm"
)

const engine = "embedded"

// smokeModule is (module (func (export "_start"))): enough to prove the engine
// can compile and run a WASI command.
var smokeModule = []byte{
	0, 97, 115, 109, 1, 0, 0, 0,
	1, 4, 1, 96, 0, 0,
	3, 2, 1, 0,
	7, 10, 1, 6, '_', 's', 't', 'a', 'r', 't', 0, 0,
	10, 4, 1, 2, 0, 11,
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	request, err := plugin.ParseAdapterInvocation(args)
	if err != nil {
		fmt.Fprintf(stderr, "wasm: %v\n", err)
		return 2
	}
	switch request.Operation {
	case "list":
		fmt.Fprintln(stdout, engine)
	case "observe":
		return observe(stdout)
	case "validate":
		return validate(request.Selection, stderr)
	case "doctor":
		if code := validate(request.Selection, stderr); code != 0 {
			return code
		}
		if _, err := wasm.RunCommand(context.Background(), smokeModule, wasm.CommandOptions{}); err != nil {
			fmt.Fprintf(stderr, "wasm: the embedded engine cannot run a module: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "wasm: embedded wazero engine ready (WASI Preview 1)")
	case "run":
		if code := validate(request.Selection, stderr); code != 0 {
			return code
		}
		return execute(request.Arguments, stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "wasm: unsupported adapter operation %s\n", request.Operation)
		return 2
	}
	return 0
}

func engineVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range info.Deps {
			if dependency.Path == "github.com/tetratelabs/wazero" {
				return dependency.Version
			}
		}
	}
	return "unknown"
}

// observe reports the attributes every engine adapter provides (kind, version
// and isolation) plus what is specific to this engine.
func observe(stdout io.Writer) int {
	data, err := json.Marshal(map[string]any{"version": 1, "contexts": []any{map[string]any{
		"selection": engine,
		"attributes": map[string]string{
			"kind": "wasm", "version": engineVersion(), "isolation": "sandboxed",
			"engine": "wazero", "wasi": "preview1", "sandbox": "no host access unless granted",
		},
	}}})
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// validate accepts the one built-in engine, or no selection at all, so a
// project can run modules without choosing anything.
func validate(selection string, stderr io.Writer) int {
	if selection != "" && selection != engine {
		fmt.Fprintf(stderr, "wasm: engine %q is not available; the built-in engine is %q\n", selection, engine)
		return 1
	}
	return 0
}

type mounts map[string]string

func (m mounts) String() string { return fmt.Sprint(map[string]string(m)) }
func (m mounts) Set(value string) error {
	guest, host, ok := strings.Cut(value, "=")
	if !ok || !strings.HasPrefix(guest, "/") || host == "" {
		return errors.New("expected GUEST=HOST with an absolute guest path")
	}
	m[guest] = host
	return nil
}

type environment map[string]string

func (e environment) String() string { return fmt.Sprint(map[string]string(e)) }
func (e environment) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return errors.New("expected KEY=VALUE")
	}
	e[key] = val
	return nil
}

// execute runs `[flags] module.wasm [arguments...]`. Standard streams belong to
// the caller's command line and are connected. The environment and filesystem
// are not: the module sees only what a flag grants.
func execute(arguments []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("wasm", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dirs, readonly, env := mounts{}, mounts{}, environment{}
	flags.Var(dirs, "dir", "mount a host directory read-write: GUEST=HOST (repeatable)")
	flags.Var(readonly, "ro-dir", "mount a host directory read-only: GUEST=HOST (repeatable)")
	flags.Var(env, "env", "set an environment variable: KEY=VALUE (repeatable)")
	timeout := flags.Duration("timeout", 0, "stop the module after this long (0 means no limit)")
	inspect := flags.Bool("inspect", false, "report what the module imports and whether this engine can run it, without running it")
	pages := flags.Uint("memory-pages", 0, "linear memory limit in 64 KiB pages (0 uses the default)")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	rest := flags.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "wasm: usage: ctx run wasm [--dir GUEST=HOST] [--ro-dir GUEST=HOST] [--env K=V] [--timeout D] module.wasm [arguments...]")
		return 2
	}
	module, err := readModule(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "wasm: %v\n", err)
		return 1
	}
	// Choose by what the module needs, not by what the machine has: this engine
	// runs WASI and pure modules, and says precisely why it cannot run others.
	inspection, err := wasm.Inspect(context.Background(), module)
	if err != nil {
		fmt.Fprintf(stderr, "wasm: %v\n", err)
		return 1
	}
	if *inspect {
		return report(stdout, inspection)
	}
	if inspection.Target == wasm.TargetComponent || inspection.Target == wasm.TargetHost {
		fmt.Fprintf(stderr, "wasm: %s\n", refusal(rest[0], inspection))
		return 126
	}
	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	if *pages > 65536 {
		fmt.Fprintln(stderr, "wasm: --memory-pages must be at most 65536")
		return 2
	}
	code, err := wasm.RunCommand(ctx, module, wasm.CommandOptions{
		Name: rest[0], Args: rest[1:], Env: env, Dirs: dirs, ReadOnlyDirs: readonly,
		Stdin: stdin, Stdout: stdout, Stderr: stderr, MemoryLimitPages: uint32(*pages),
	})
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintf(stderr, "wasm: stopped after %s\n", *timeout)
		return 124
	case err != nil:
		fmt.Fprintf(stderr, "wasm: %v\n", err)
		return 1
	}
	return code
}

// report prints the inspection and whether the built-in engine can run it.
func report(stdout io.Writer, inspection wasm.Inspection) int {
	runnable := inspection.Target != wasm.TargetHost && inspection.Target != wasm.TargetComponent
	engineName := engine
	if !runnable {
		engineName = "none available"
	}
	data, err := json.Marshal(struct {
		wasm.Inspection
		Runnable bool   `json:"runnable"`
		Engine   string `json:"engine"`
	}{inspection, runnable, engineName})
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// refusal explains why the built-in engine cannot run a module.
func refusal(path string, inspection wasm.Inspection) string {
	if inspection.Target == wasm.TargetComponent {
		return fmt.Sprintf("%s is a WebAssembly component (the component model, WASI Preview 2). The built-in engine runs core "+
			"modules (WASI Preview 1) only, and running components needs a runtime that supports them, such as wasmtime, "+
			"which ctx does not offer yet.", path)
	}
	modules := strings.Join(inspection.Unsupported, ", ")
	if inspection.Web {
		return fmt.Sprintf("%s is a web module: it imports JavaScript glue (%s). The built-in engine runs WASI and pure modules only; "+
			"web modules run in a browser through the ext web engine (res/web/bundle).", path, modules)
	}
	return fmt.Sprintf("%s imports host functions the built-in engine does not provide (%s). "+
		"It runs WASI Preview 1 and pure modules only.", path, modules)
}

func readModule(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > wasm.MaxModuleBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, wasm.MaxModuleBytes)
	}
	return os.ReadFile(path)
}
