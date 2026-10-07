package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"github.com/webong/ext/pkg/plugin"
)

// CommandOptions describes one run of a WASI Preview 1 command. Nothing is
// inherited from the host: no environment, no filesystem, no network and no
// stdin unless the caller grants it here.
type CommandOptions struct {
	// Name is argv[0] as the module sees it. Empty uses "module".
	Name string
	Args []string
	Env  map[string]string
	// Dirs maps a guest path to a host directory that the module may read and
	// write. ReadOnlyDirs maps a guest path to a directory it may only read.
	// Access is confined to the directory; links cannot leave it.
	Dirs         map[string]string
	ReadOnlyDirs map[string]string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	// MemoryLimitPages bounds linear memory (64 KiB/page). Zero uses
	// DefaultMemoryPages.
	MemoryLimitPages uint32
}

// RunCommand compiles module and runs its _start function to completion. The
// result is the module's exit status; a non-zero status is not an error.
// Cancelling ctx terminates the module, including a compute loop that does no
// I/O, and returns the context's error.
func RunCommand(ctx context.Context, module []byte, opts CommandOptions) (int, error) {
	if len(module) == 0 || len(module) > MaxModuleBytes || opts.MemoryLimitPages > 65536 {
		return 0, plugin.ErrInvalid
	}
	pages := opts.MemoryLimitPages
	if pages == 0 {
		pages = DefaultMemoryPages
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(pages).WithCloseOnContextDone(true))
	defer runtime.Close(context.Background())
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return 0, err
	}
	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", plugin.ErrInvalid, err)
	}
	name := opts.Name
	if name == "" {
		name = "module"
	}
	config := wazero.NewModuleConfig().
		WithName("").WithArgs(append([]string{name}, opts.Args...)...).
		WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	for _, key := range sortedKeys(opts.Env) {
		config = config.WithEnv(key, opts.Env[key])
	}
	filesystem := wazero.NewFSConfig()
	mounted := false
	for _, guest := range sortedKeys(opts.Dirs) {
		filesystem = filesystem.WithDirMount(opts.Dirs[guest], guest)
		mounted = true
	}
	for _, guest := range sortedKeys(opts.ReadOnlyDirs) {
		filesystem = filesystem.WithReadOnlyDirMount(opts.ReadOnlyDirs[guest], guest)
		mounted = true
	}
	if mounted {
		config = config.WithFSConfig(filesystem)
	}
	if opts.Stdin != nil {
		config = config.WithStdin(opts.Stdin)
	}
	if opts.Stdout != nil {
		config = config.WithStdout(opts.Stdout)
	}
	if opts.Stderr != nil {
		config = config.WithStderr(opts.Stderr)
	}
	instance, err := runtime.InstantiateModule(ctx, compiled, config)
	if instance != nil {
		defer instance.Close(context.Background())
	}
	var exit *sys.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		if exit.ExitCode() == sys.ExitCodeContextCanceled || exit.ExitCode() == sys.ExitCodeDeadlineExceeded {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
		}
		return int(exit.ExitCode()), nil
	case ctx.Err() != nil:
		return 0, ctx.Err()
	}
	return 0, err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
