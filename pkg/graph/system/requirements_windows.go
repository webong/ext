//go:build windows

package systemgraph

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	hostGetVolumePathName = hostKernel.NewProc("GetVolumePathNameW")
	hostGetVolumeName     = hostKernel.NewProc("GetVolumeNameForVolumeMountPointW")
)

func platformFilesystemAt(ctx context.Context, path string) (FilesystemInfo, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemInfo{}, err
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return FilesystemInfo{}, err
	}
	buffer := make([]uint16, 32768)
	ok, _, err := hostGetVolumePathName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if ok == 0 {
		return FilesystemInfo{}, fmtHostReadError("path volume", err)
	}
	point := syscall.UTF16ToString(buffer)
	mount := FilesystemInfo{MountPoint: point, Source: point}
	root, err := syscall.UTF16PtrFromString(point)
	if err != nil {
		return FilesystemInfo{}, err
	}
	volume := make([]uint16, 1024)
	ok, _, _ = hostGetVolumeName.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&volume[0])), uintptr(len(volume)))
	if ok != 0 {
		mount.Source = syscall.UTF16ToString(volume)
	}
	mount, present, err := windowsMountDetails(ctx, mount)
	if err != nil {
		return FilesystemInfo{}, err
	}
	if !present {
		return FilesystemInfo{}, fmt.Errorf("path volume has no mounted media")
	}
	return mount, nil
}

func hostDirectoryWritable(path string) error {
	// Request directory AddFile rights on an existing directory handle. No
	// file is created; the eventual output open still performs its own check.
	return hostWindowsAccess(path, 2, syscall.FILE_FLAG_BACKUP_SEMANTICS)
}

func hostExecutableAccess(path string) error {
	return hostWindowsAccess(path, 0x20, 0) // FILE_EXECUTE.
}

func hostWindowsAccess(path string, access, flags uint32) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(name, access, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, flags, 0)
	if err != nil {
		return err
	}
	return syscall.CloseHandle(handle)
}
