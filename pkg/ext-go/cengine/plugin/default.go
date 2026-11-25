package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func nativeLibrary() (*library.Library, error) {
	var native *library.Library
	var err error
	if path := os.Getenv("EXT_PLUGIN_LIBRARY"); path != "" {
		native, err = library.Open(path)
	} else if _, ok := library.Provided("ext_plugin"); ok {
		native, err = library.OpenProvided("ext_plugin", "")
	} else {
		exe, e := os.Executable()
		if e != nil {
			return nil, e
		}
		name := "libext_plugin_host.so"
		if runtime.GOOS == "darwin" {
			name = "libext_plugin_host.dylib"
		}
		if runtime.GOOS == "windows" {
			name = "ext_plugin_host.dll"
		}
		native, err = library.Open(filepath.Join(filepath.Dir(exe), "ext-lib", name))
	}
	if err != nil {
		return nil, fmt.Errorf("canonical plugin engine unavailable (set EXT_PLUGIN_LIBRARY to its shared library): %w", err)
	}
	return native, nil
}

// Default opens an explicitly configured, embedded, or executable-bundled C
// engine. Missing libraries are errors; there is no legacy Go host fallback.
func Default() (*Library, error) {
	n, err := nativeLibrary()
	if err != nil {
		return nil, err
	}
	return Bind(n)
}
