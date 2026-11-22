//go:build (darwin || linux || windows) && (amd64 || arm64)

package library

import (
	"fmt"

	"github.com/ebitengine/purego"
)

func Supported() bool { return true }

func register(fn any, address uintptr) (err error) {
	defer func() {
		if r := recover(); r != nil { // purego panics on a type it cannot pass
			err = fmt.Errorf("bind: %v", r)
		}
	}()
	purego.RegisterFunc(fn, address)
	return nil
}
