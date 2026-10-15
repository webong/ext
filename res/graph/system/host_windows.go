//go:build windows

package systemgraph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func platformShellPaths(ctx context.Context) ([]shellPath, error) {
	paths := []shellPath{}
	if root := os.Getenv("SystemRoot"); root != "" {
		paths = append(paths,
			shellPath{filepath.Join(root, "System32", "cmd.exe"), "system"},
			shellPath{filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "system"},
		)
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if !filepath.IsAbs(root) {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(root, "PowerShell"))
		if err != nil {
			continue
		}
		for _, version := range versions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if version.IsDir() {
				paths = append(paths, shellPath{filepath.Join(root, "PowerShell", version.Name(), "pwsh.exe"), "system"})
			}
		}
	}
	return paths, ctx.Err()
}

func hostExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && strings.EqualFold(filepath.Ext(path), ".exe")
}

var (
	hostKernel                 = syscall.NewLazyDLL("kernel32.dll")
	hostGetLogicalDriveStrings = hostKernel.NewProc("GetLogicalDriveStringsW")
	hostGetDriveType           = hostKernel.NewProc("GetDriveTypeW")
	hostFindFirstVolume        = hostKernel.NewProc("FindFirstVolumeW")
	hostFindNextVolume         = hostKernel.NewProc("FindNextVolumeW")
	hostFindVolumeClose        = hostKernel.NewProc("FindVolumeClose")
	hostGetVolumePaths         = hostKernel.NewProc("GetVolumePathNamesForVolumeNameW")
	hostGetVolumeInformation   = hostKernel.NewProc("GetVolumeInformationW")
	hostGetDiskFreeSpace       = hostKernel.NewProc("GetDiskFreeSpaceExW")
)

func platformFilesystems(ctx context.Context) ([]FilesystemInfo, error) {
	// Volume paths include mounts in folders, which drive letters alone miss.
	mounts, err := windowsVolumeMounts(ctx)
	if err != nil {
		return nil, err
	}
	buffer := make([]uint16, 32768)
	count, _, err := hostGetLogicalDriveStrings.Call(uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])))
	if count == 0 || count >= uintptr(len(buffer)) {
		return nil, fmtHostReadError("Windows drive paths", err)
	}
	// Mapped network drives are not returned by volume enumeration.
	for _, path := range windowsStringList(buffer[:count]) {
		if _, exists := mounts[hostPathKey(path)]; !exists {
			mounts[hostPathKey(path)] = FilesystemInfo{MountPoint: path, Source: path}
		}
	}
	result := make([]FilesystemInfo, 0, len(mounts))
	for _, mount := range mounts {
		details, present, err := windowsMountDetails(ctx, mount)
		if err != nil {
			return nil, err
		}
		if present {
			result = append(result, details)
		}
	}
	return result, nil
}

func windowsMountDetails(ctx context.Context, mount FilesystemInfo) (FilesystemInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemInfo{}, false, err
	}
	point, err := syscall.UTF16PtrFromString(mount.MountPoint)
	if err != nil {
		return FilesystemInfo{}, false, fmtHostReadError("Windows mount point", err)
	}
	driveType, _, _ := hostGetDriveType.Call(uintptr(unsafe.Pointer(point)))
	switch driveType {
	case 2:
		mount.DriveKind = "removable"
	case 3:
		mount.DriveKind = "fixed"
	case 4:
		mount.DriveKind = "network"
	case 5:
		mount.DriveKind = "optical"
	case 6:
		mount.DriveKind = "ramdisk"
	default:
		mount.DriveKind = "unknown"
	}
	label, kind := make([]uint16, 261), make([]uint16, 261)
	var flags uint32
	ok, _, err := hostGetVolumeInformation.Call(uintptr(unsafe.Pointer(point)),
		uintptr(unsafe.Pointer(&label[0])), uintptr(len(label)), 0, 0,
		uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&kind[0])), uintptr(len(kind)))
	if ok != 0 {
		mount.Label, mount.Type = syscall.UTF16ToString(label), syscall.UTF16ToString(kind)
		mount.ReadOnly = flags&0x00080000 != 0
	} else {
		// A drive letter with no removable media is not a mounted filesystem.
		if errors.Is(err, syscall.Errno(21)) && (driveType == 2 || driveType == 5) {
			return mount, false, nil
		}
		mount.DetailError = "volume details unavailable: " + err.Error()
	}
	var available, total, free uint64
	ok, _, err = hostGetDiskFreeSpace.Call(uintptr(unsafe.Pointer(point)),
		uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if ok != 0 {
		mount.Space = &FilesystemSpace{TotalBytes: total, FreeBytes: free, AvailableBytes: available}
	} else {
		if mount.DetailError != "" {
			mount.DetailError += "; "
		}
		mount.DetailError += "space unavailable: " + err.Error()
	}
	return mount, true, ctx.Err()
}

func windowsVolumeMounts(ctx context.Context) (map[string]FilesystemInfo, error) {
	buffer := make([]uint16, 1024)
	handle, _, err := hostFindFirstVolume.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if handle == ^uintptr(0) {
		return nil, fmtHostReadError("Windows volumes", err)
	}
	defer hostFindVolumeClose.Call(handle)
	result := map[string]FilesystemInfo{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		volume := syscall.UTF16ToString(buffer)
		paths, err := windowsVolumePaths(volume)
		if err != nil {
			return nil, fmtHostReadError("Windows volume mount points", err)
		}
		for _, path := range paths {
			result[hostPathKey(path)] = FilesystemInfo{MountPoint: path, Source: volume}
		}
		ok, _, err := hostFindNextVolume.Call(handle, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if ok == 0 {
			if errors.Is(err, syscall.Errno(18)) { // ERROR_NO_MORE_FILES
				break
			}
			return nil, fmtHostReadError("Windows volumes", err)
		}
	}
	return result, nil
}

func windowsVolumePaths(volume string) ([]string, error) {
	name, err := syscall.UTF16PtrFromString(volume)
	if err != nil {
		return nil, err
	}
	buffer := make([]uint16, 1024)
	for attempt := 0; attempt < 3; attempt++ {
		var required uint32
		ok, _, err := hostGetVolumePaths.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(len(buffer)), uintptr(unsafe.Pointer(&required)))
		if ok != 0 {
			return windowsStringList(buffer), nil
		}
		if !errors.Is(err, syscall.Errno(234)) || required <= uint32(len(buffer)) || required > 1<<20 {
			return nil, err
		}
		buffer = make([]uint16, required)
	}
	return nil, fmt.Errorf("Windows volume mount points changed during discovery")
}

func windowsStringList(buffer []uint16) []string {
	result := []string{}
	start := 0
	for i, character := range buffer {
		if character != 0 {
			continue
		}
		if i == start {
			break
		}
		result = append(result, syscall.UTF16ToString(buffer[start:i]))
		start = i + 1
	}
	return result
}
