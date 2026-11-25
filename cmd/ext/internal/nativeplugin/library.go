// Package nativeplugin opens the canonical engine through its described interface.
package nativeplugin

import (
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"os"
	"path/filepath"
	"runtime"
)

func openLibrary() (*library.Library, error) {
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

// Path returns the canonical engine loaded by the CLI for its product guests.
func Path() (string, error) {
	native, err := openLibrary()
	if err != nil {
		return "", err
	}
	return native.Path(), nil
}

func Open() (*library.Described, error) {
	native, err := openLibrary()
	if err != nil {
		return nil, err
	}
	api, err := library.BindInterface(native, "ext_plugin", 1)
	if err != nil {
		return nil, err
	}
	return library.Describe(api)
}
