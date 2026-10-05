//go:build windows

package graph

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	lockKernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileExProc   = lockKernel32.NewProc("LockFileEx")
	unlockFileExProc = lockKernel32.NewProc("UnlockFileEx")
)

const lockExclusive = 0x00000002

type fileLock struct {
	file       *os.File
	overlapped syscall.Overlapped
}

func acquireFileLock(path string) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	lock := &fileLock{file: file}
	r1, _, callErr := lockFileExProc.Call(file.Fd(), lockExclusive, 0, 1, 0, uintptr(unsafe.Pointer(&lock.overlapped)))
	if r1 == 0 {
		_ = file.Close()
		return nil, callErr
	}
	return lock, nil
}
func (l *fileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	r1, _, callErr := unlockFileExProc.Call(l.file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&l.overlapped)))
	closeErr := l.file.Close()
	if r1 == 0 {
		return callErr
	}
	return closeErr
}
