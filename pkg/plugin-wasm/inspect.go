package wasm

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/webong/ext/pkg/plugin"
)

// Target classifies what a module needs from its host.
type Target string

const (
	// TargetNone modules import nothing: pure computation.
	TargetNone Target = "none"
	// TargetWASI modules import only WASI Preview 1, which RunCommand provides.
	TargetWASI Target = "wasi"
	// TargetHost modules import functions RunCommand cannot supply, typically
	// JavaScript glue for a browser or another embedder.
	TargetHost Target = "host"
	// TargetComponent is a WebAssembly component (the component model, WASI
	// Preview 2). RunCommand runs core modules only.
	TargetComponent Target = "component"
)

// Inspection summarizes a module's imports without running it.
type Inspection struct {
	Target Target `json:"target"`
	// Imports lists every imported module name once, sorted, WASI included.
	Imports []string `json:"imports"`
	// Unsupported lists the imported modules RunCommand cannot supply.
	Unsupported []string `json:"unsupported,omitempty"`
	// Web is true when the unsupported imports match a known web toolchain
	// (wasm-bindgen, Go's js/wasm, Emscripten's web runtime). It is a hint for
	// error messages, not a guarantee.
	Web bool `json:"web"`
}

// wasiModules are the import modules RunCommand provides.
var wasiModules = map[string]bool{"wasi_snapshot_preview1": true}

// Inspect compiles module and reports what it imports. It does not
// instantiate or run it.
func Inspect(ctx context.Context, module []byte) (Inspection, error) {
	if len(module) == 0 || len(module) > MaxModuleBytes {
		return Inspection{}, plugin.ErrInvalid
	}
	if isComponent(module) {
		return Inspection{Target: TargetComponent, Imports: []string{}}, nil
	}
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(context.Background())
	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		return Inspection{}, fmt.Errorf("%w: %v", plugin.ErrInvalid, err)
	}
	defer compiled.Close(context.Background())
	seen, unsupported := map[string]bool{}, map[string]bool{}
	web := false
	for _, function := range compiled.ImportedFunctions() {
		name, field, _ := function.Import()
		seen[name] = true
		if wasiModules[name] {
			continue
		}
		unsupported[name] = true
		web = web || looksWeb(name, field)
	}
	for _, memory := range compiled.ImportedMemories() {
		name, _, _ := memory.Import()
		seen[name] = true
		if !wasiModules[name] {
			unsupported[name] = true
		}
	}
	inspection := Inspection{Imports: sorted(seen), Unsupported: sorted(unsupported), Web: web}
	switch {
	case len(seen) == 0:
		inspection.Target = TargetNone
	case len(unsupported) == 0:
		inspection.Target = TargetWASI
	default:
		inspection.Target = TargetHost
	}
	if inspection.Imports == nil {
		inspection.Imports = []string{}
	}
	return inspection, nil
}

// isComponent reports whether the bytes are a component, whose header carries
// version 0x0d and layer 1 where a core module carries version 1 and layer 0.
func isComponent(module []byte) bool {
	return len(module) >= 8 && string(module[:4]) == "\x00asm" &&
		module[4] == 0x0d && module[5] == 0 && module[6] == 0x01 && module[7] == 0
}

// looksWeb recognizes the import modules and functions that browser-oriented
// toolchains generate.
func looksWeb(module, field string) bool {
	switch {
	case module == "wbg", module == "gojs", strings.HasPrefix(module, "__wbindgen"):
		return true
	case module == "env" && (strings.HasPrefix(field, "emscripten_") || strings.HasPrefix(field, "invoke_")):
		return true
	}
	return false
}

func sorted(set map[string]bool) []string {
	list := make([]string, 0, len(set))
	for name := range set {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}
