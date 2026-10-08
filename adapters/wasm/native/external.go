package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// external describes a WebAssembly runtime that is a command-line program on
// the machine, used when a selection names it. Each is checked against the real
// binary: what it can enforce is what the adapter lets a caller ask for.
type external struct {
	name string
	// dirs lists where to look besides PATH, relative to the user's home.
	dirs []string
	// components reports whether it runs WASI Preview 2 components.
	components bool
	// memoryLimit reports whether it can cap linear memory.
	memoryLimit bool
}

var externals = []external{
	{name: "wasmtime", dirs: []string{".wasmtime/bin", ".cargo/bin"}, components: true, memoryLimit: true},
	{name: "wasmer", dirs: []string{".wasmer/bin"}},
}

func lookupExternal(name string) (external, bool) {
	for _, candidate := range externals {
		if candidate.name == name {
			return candidate, true
		}
	}
	return external{}, false
}

// locate finds the runtime's executable: PATH first, then the places its own
// installer uses, so a runtime installed for the user is found without setup.
func (e external) locate() (string, bool) {
	if path, err := exec.LookPath(e.name); err == nil {
		return path, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, directory := range e.dirs {
		path := filepath.Join(home, filepath.FromSlash(directory), e.name+suffix)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

// version asks the runtime for its version line, which is only reported and
// never parsed to decide behavior.
func (e external) version(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
}

// available lists the selections this machine offers, built-in first.
func available() []string {
	selections := []string{engine}
	for _, candidate := range externals {
		if _, ok := candidate.locate(); ok {
			selections = append(selections, candidate.name)
		}
	}
	return selections
}

func describeAvailable() string {
	selections := available()
	sort.Strings(selections)
	return strings.Join(selections, ", ")
}

// arguments translates the adapter's flags into the runtime's own. A flag the
// runtime cannot enforce is an error, never a quiet grant of more access.
func (e external) arguments(module string, rest []string, o externalOptions) ([]string, error) {
	var args []string
	switch e.name {
	case "wasmtime":
		args = []string{"run"}
		for _, guest := range sortedKeys(o.dirs) {
			args = append(args, "--dir", o.dirs[guest]+"::"+guest)
		}
		for _, key := range sortedKeys(o.env) {
			args = append(args, "--env", key+"="+o.env[key])
		}
		if o.pages > 0 {
			args = append(args, "-W", fmt.Sprintf("max-memory-size=%d", uint64(o.pages)*65536))
		}
		args = append(args, module)
		args = append(args, rest...)
	case "wasmer":
		args = []string{"run"}
		for _, guest := range sortedKeys(o.dirs) {
			args = append(args, "--volume", o.dirs[guest]+":"+guest)
		}
		for _, key := range sortedKeys(o.env) {
			args = append(args, "--env", key+"="+o.env[key])
		}
		args = append(args, module)
		if len(rest) > 0 {
			args = append(args, "--")
			args = append(args, rest...)
		}
	default:
		return nil, fmt.Errorf("no argument translation for %s", e.name)
	}
	return args, nil
}

// check refuses what the runtime cannot honor.
func (e external) check(o externalOptions) error {
	if len(o.readonly) > 0 {
		return fmt.Errorf("%s cannot mount a directory read-only (it ignores the request and allows writes), so --ro-dir is refused; "+
			"use the built-in %q engine, or mount with --dir if write access is acceptable", e.name, engine)
	}
	if o.pages > 0 && !e.memoryLimit {
		return fmt.Errorf("%s has no linear memory limit, so --memory-pages is refused; use wasmtime or the built-in %q engine", e.name, engine)
	}
	return nil
}

type externalOptions struct {
	dirs, readonly, env map[string]string
	pages               uint
	timeout             time.Duration
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// start runs the runtime and returns its exit status. The adapter enforces the
// timeout itself by stopping the process, so it behaves the same whichever
// runtime is selected, including one with no timeout of its own.
func (e external) start(path string, args []string, o externalOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	command.WaitDelay = 2 * time.Second
	err := command.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(stderr, "wasm: stopped %s after %s\n", e.name, o.timeout)
		return 124
	case err == nil:
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	fmt.Fprintf(stderr, "wasm: cannot run %s: %v\n", e.name, err)
	return 127
}
