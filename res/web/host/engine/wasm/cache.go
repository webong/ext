package wasm

import (
	"context"

	"github.com/tetratelabs/wazero"
)

// CompilationCache keeps the compiled form of the modules opened with it, so opening the same
// module again skips compilation, which is most of the cost of Open for a large module. A nil
// cache in Options, ReactorOptions or CommandOptions compiles every time.
//
// Share one among the sessions of a process, and Close it after they have closed. It is safe for
// concurrent use. It holds every module it compiles until it is closed, so it is not a budget on
// the number of distinct modules: a host that loads many unrelated modules decides for itself
// which sessions share one.
type CompilationCache struct{ cache wazero.CompilationCache }

// NewCompilationCache returns an empty cache that lives in memory.
func NewCompilationCache() *CompilationCache {
	return &CompilationCache{cache: wazero.NewCompilationCache()}
}

// Close releases the compiled modules. The cache must not be used afterwards.
func (c *CompilationCache) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.cache.Close(ctx)
}

// runtimeConfig is the configuration every execution path uses: guest memory bounded, a running
// module stopped when its context is done, and no DWARF.
//
// Stopping a running guest unwinds it with an error, and with debug information on (wazero's
// default) the unwinding looks up a source line in the module's DWARF for every frame. Each lookup
// reads the debug entries from the start, and the first one for a module reads its whole line
// table, so a guest built with debug information (a Zig WASI command is) kept Close, and the
// cleanup of the session around it, busy for seconds on a loaded runner. A trap still names the
// functions it passed through; it just no longer names their source lines.
func runtimeConfig(memoryLimitPages uint32, cache *CompilationCache) wazero.RuntimeConfig {
	config := wazero.NewRuntimeConfig().WithMemoryLimitPages(memoryLimitPages).WithCloseOnContextDone(true).
		WithDebugInfoEnabled(false)
	if cache != nil {
		config = config.WithCompilationCache(cache.cache)
	}
	return config
}
