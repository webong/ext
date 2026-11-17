//go:build !cgo && (darwin || linux) && (amd64 || arm64)

package cshared

import "github.com/ebitengine/purego"

func loadImage(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func lookup(library uintptr, name string) (uintptr, error) { return purego.Dlsym(library, name) }
