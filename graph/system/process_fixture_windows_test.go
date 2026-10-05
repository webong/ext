//go:build windows

package systemgraph

import (
	"golang.org/x/sys/windows"
	"os"
)

func mapProcessFixture(file *os.File) (func() error, error) {
	h, err := windows.CreateFileMapping(windows.Handle(file.Fd()), nil, windows.PAGE_READONLY, 0, 4096, nil)
	if err != nil {
		return nil, err
	}
	address, err := windows.MapViewOfFile(h, windows.FILE_MAP_READ, 0, 0, 4096)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return func() error {
		err := windows.UnmapViewOfFile(address)
		closeErr := windows.CloseHandle(h)
		if err != nil {
			return err
		}
		return closeErr
	}, nil
}
