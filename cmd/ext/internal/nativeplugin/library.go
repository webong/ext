// Package nativeplugin opens the canonical engine through its described interface.
package nativeplugin

import (
	"fmt"

	"github.com/webong/ext/cmd/ext/internal/nativelib"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func openLibrary() (*library.Library, error) {
	native, err := nativelib.Open(nativelib.Plugin)
	if err != nil {
		return nil, fmt.Errorf("canonical plugin engine unavailable: %w", err)
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
