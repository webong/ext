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
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

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
		for _, selection := range available() {
			fmt.Fprintln(stdout, selection)
		}
	case "observe":
		return observe(stdout)
	case "validate":
		return validate(request.Selection, stderr)
	case "doctor":
		if code := validate(request.Selection, stderr); code != 0 {
			return code
		}
		if candidate, ok := lookupExternal(request.Selection); ok {
			return doctorExternal(candidate, stdout, stderr)
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
		return execute(request.Selection, request.Arguments, stdin, stdout, stderr)
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
// and isolation) plus what is specific to each engine, one context per engine
// this machine offers.
func observe(stdout io.Writer) int {
	contexts := []any{map[string]any{
		"selection": engine,
		"attributes": map[string]string{
			"kind": "wasm", "version": engineVersion(), "isolation": "sandboxed",
			"engine": "wazero", "wasi": "preview1", "components": "no", "sandbox": "no host access unless granted",
		},
	}}
	for _, candidate := range externals {
		path, ok := candidate.locate()
		if !ok {
			continue
		}
		components := "no"
		if candidate.components {
			components = "yes"
		}
		contexts = append(contexts, map[string]any{
			"selection": candidate.name,
			"attributes": map[string]string{
				"kind": "wasm", "version": candidate.version(path), "isolation": "sandboxed",
				"engine": candidate.name, "path": path, "components": components,
				"sandbox": "no host access unless granted; cannot mount read-only",
			},
		})
	}
	data, err := json.Marshal(map[string]any{"version": 1, "contexts": contexts})
	if err != nil {
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// validate accepts the built-in engine, an installed external runtime, or no
// selection at all, so a project can run modules without choosing anything. A
// known runtime that is not installed is reported as missing (127), which
// differs from an engine this adapter has never heard of (1).
func validate(selection string, stderr io.Writer) int {
	if selection == "" || selection == engine {
		return 0
	}
	if candidate, ok := lookupExternal(selection); ok {
		if _, found := candidate.locate(); found {
			return 0
		}
		fmt.Fprintf(stderr, "wasm: %s is not installed (available: %s)\n", selection, describeAvailable())
		return 127
	}
	fmt.Fprintf(stderr, "wasm: engine %q is not available (available: %s)\n", selection, describeAvailable())
	return 1
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
func execute(selection string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
	chosen, reason := choose(selection, inspection, rest[0])
	if *inspect {
		return report(stdout, inspection, chosen)
	}
	if chosen == "" {
		fmt.Fprintf(stderr, "wasm: %s\n", reason)
		return 126
	}
	if candidate, ok := lookupExternal(chosen); ok {
		path, found := candidate.locate()
		if !found {
			fmt.Fprintf(stderr, "wasm: %s is not installed (available: %s)\n", chosen, describeAvailable())
			return 127
		}
		options := externalOptions{dirs: dirs, readonly: readonly, env: env, pages: *pages, timeout: *timeout}
		if err := candidate.check(options); err != nil {
			fmt.Fprintf(stderr, "wasm: %v\n", err)
			return 2
		}
		args, err := candidate.arguments(rest[0], rest[1:], options)
		if err != nil {
			fmt.Fprintf(stderr, "wasm: %v\n", err)
			return 1
		}
		return candidate.start(path, args, options, stdin, stdout, stderr)
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

// choose picks the engine that will run a module, or returns an empty name and
// the reason none can. The module decides first (web modules run nowhere here,
// components need a runtime that has the component model); the selection decides
// among the engines that can run it.
func choose(selection string, inspection wasm.Inspection, path string) (string, string) {
	if inspection.Target == wasm.TargetHost {
		return "", refusal(path, inspection)
	}
	if inspection.Target == wasm.TargetComponent {
		return chooseForComponent(selection, path)
	}
	if selection == "" {
		return engine, ""
	}
	return selection, ""
}

func chooseForComponent(selection, path string) (string, string) {
	const what = "is a WebAssembly component (the component model, WASI Preview 2)"
	switch selection {
	case "":
		if candidate, _ := lookupExternal("wasmtime"); candidate.components {
			if _, found := candidate.locate(); found {
				return "wasmtime", ""
			}
		}
		return "", fmt.Sprintf("%s %s. The built-in engine runs core modules (WASI Preview 1) only, and no runtime that supports "+
			"components is installed. Install wasmtime and select it with `ctx set wasm wasmtime`.", path, what)
	case engine:
		return "", fmt.Sprintf("%s %s. The built-in engine runs core modules (WASI Preview 1) only; select wasmtime with "+
			"`ctx set wasm wasmtime` to run it.", path, what)
	}
	if candidate, ok := lookupExternal(selection); ok && !candidate.components {
		return "", fmt.Sprintf("%s %s, which %s does not support. Select wasmtime with `ctx set wasm wasmtime`.", path, what, selection)
	}
	return selection, ""
}

// report prints the inspection and which engine would run it.
func report(stdout io.Writer, inspection wasm.Inspection, chosen string) int {
	runnable := chosen != ""
	engineName := chosen
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

// refusal explains why no engine here can run a web or host-importing module.
func refusal(path string, inspection wasm.Inspection) string {
	modules := strings.Join(inspection.Unsupported, ", ")
	if inspection.Web {
		return fmt.Sprintf("%s is a web module: it imports JavaScript glue (%s). The engines here run WASI and pure modules only; "+
			"web modules run in a browser through the ext web engine (res/web/bundle).", path, modules)
	}
	return fmt.Sprintf("%s imports host functions no engine here provides (%s). "+
		"They run WASI Preview 1 and pure modules only.", path, modules)
}

// doctorExternal proves an external runtime can run something, not only that it
// exists, by running an empty command module through it.
func doctorExternal(candidate external, stdout, stderr io.Writer) int {
	path, found := candidate.locate()
	if !found {
		fmt.Fprintf(stderr, "wasm: %s is not installed\n", candidate.name)
		return 127
	}
	directory, err := os.MkdirTemp("", "ctx-wasm-doctor-")
	if err != nil {
		fmt.Fprintf(stderr, "wasm: %v\n", err)
		return 1
	}
	defer os.RemoveAll(directory)
	module := filepath.Join(directory, "smoke.wasm")
	if err := os.WriteFile(module, smokeModule, 0o600); err != nil {
		fmt.Fprintf(stderr, "wasm: %v\n", err)
		return 1
	}
	args, err := candidate.arguments(module, nil, externalOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "wasm: %v\n", err)
		return 1
	}
	if code := candidate.start(path, args, externalOptions{timeout: 30 * time.Second}, nil, io.Discard, stderr); code != 0 {
		fmt.Fprintf(stderr, "wasm: %s could not run a module (exit %d)\n", candidate.name, code)
		return 1
	}
	fmt.Fprintf(stdout, "wasm: %s ready (%s)\n", candidate.name, candidate.version(path))
	return 0
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
