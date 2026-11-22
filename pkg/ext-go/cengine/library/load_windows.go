//go:build windows && (amd64 || arm64)

package library

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	loadLibrarySearchDLLLoadDir  = 0x00000100
	loadLibrarySearchDefaultDirs = 0x00001000
)

var loadLibraryExW = syscall.NewLazyDLL("kernel32.dll").NewProc("LoadLibraryExW")

func loadImage(path string) (uintptr, error) {
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("invalid UTF-8 library path")
	}
	// The library's own directory and the system's, never the working directory,
	// for the DLLs it depends on.
	h, _, errno := loadLibraryExW.Call(uintptr(unsafe.Pointer(wide)), 0, loadLibrarySearchDLLLoadDir|loadLibrarySearchDefaultDirs)
	if h == 0 {
		return 0, fmt.Errorf("LoadLibraryExW failed (%v)", errno)
	}
	return h, nil
}

func lookup(library uintptr, name string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(library), name)
}
