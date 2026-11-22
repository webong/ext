// Package library loads a C shared library (.so, .dylib, .dll) into a Go
// program without cgo, through purego, and binds its exported functions to Go
// function variables. It works the same in a cgo and a cgo-free build, on
// Linux, macOS and Windows for amd64 and arm64; Supported reports whether this
// build can load at all.
//
// A library is loaded once per canonical path and never unloaded: unloading
// native code that a Go program has called into, or that started threads, is
// not safe. Use immutable, revision-specific paths.
//
// Only functions the Go side calls are bound. purego's callbacks (C calling
// back into Go) are limited in number and platform, so a C API meant for this
// package takes no function pointers it would call on a Go program's behalf.
package library

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

// ErrUnsupported is returned where this build cannot load a C library.
var ErrUnsupported = errors.New("loading a C shared library is unsupported in this build")

// Library is a loaded C shared library.
type Library struct {
	path   string
	handle uintptr
}

var (
	images sync.Map // canonical path -> *image
)

type image struct {
	once    sync.Once
	library *Library
	err     error
}

// Open loads the library at path, resolving symbolic links first, or returns
// the library already loaded from that canonical path. A failed load is
// remembered too: loading may have run native initialisers.
func Open(path string) (*Library, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if canonical, err = filepath.Abs(canonical); err != nil {
		return nil, err
	}
	value, _ := images.LoadOrStore(canonical, &image{})
	i := value.(*image)
	i.once.Do(func() {
		handle, err := loadImage(canonical)
		if err != nil {
			i.err = fmt.Errorf("load %s: %w", canonical, err)
			return
		}
		i.library = &Library{path: canonical, handle: handle}
	})
	return i.library, i.err
}

// Path is the canonical path the library was loaded from.
func (l *Library) Path() string { return l.path }

// Symbol is the address of an exported symbol, or an error when it is absent.
func (l *Library) Symbol(name string) (uintptr, error) {
	address, err := lookup(l.handle, name)
	if err != nil {
		return 0, fmt.Errorf("symbol %s: %w", name, err)
	}
	if address == 0 {
		return 0, fmt.Errorf("symbol %s is not exported", name)
	}
	return address, nil
}

// Bind sets fn, a pointer to a variable of function type, to call the exported
// function name with the C calling convention.
func (l *Library) Bind(fn any, name string) error {
	address, err := l.Symbol(name)
	if err != nil {
		return err
	}
	return register(fn, address)
}

// BindAll binds each function variable to the export of the same key.
func (l *Library) BindAll(functions map[string]any) error {
	for name, fn := range functions {
		if err := l.Bind(fn, name); err != nil {
			return err
		}
	}
	return nil
}
