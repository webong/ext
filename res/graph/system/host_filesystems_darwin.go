//go:build darwin

package systemgraph

import (
	"context"
	"errors"
	"syscall"
)

func platformFilesystems(ctx context.Context) ([]FilesystemInfo, error) {
	const mountNoWait = 2
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, err := syscall.Getfsstat(nil, mountNoWait)
		if err != nil {
			return nil, fmtHostReadError("mounted filesystems", err)
		}
		// Leave room for mounts appearing between the count and data calls.
		buffer := make([]syscall.Statfs_t, count+16)
		count, err = syscall.Getfsstat(buffer, mountNoWait)
		if err != nil {
			return nil, fmtHostReadError("mounted filesystems", err)
		}
		if count >= len(buffer) {
			continue
		}
		result := make([]FilesystemInfo, 0, count)
		for _, mount := range buffer[:count] {
			result = append(result, FilesystemInfo{
				MountPoint: mountString(mount.Mntonname[:]), Source: mountString(mount.Mntfromname[:]),
				Type: mountString(mount.Fstypename[:]), ReadOnly: mount.Flags&1 != 0,
				Space: filesystemSpace(mount.Blocks, mount.Bfree, mount.Bavail, uint64(mount.Bsize)),
			})
		}
		return result, ctx.Err()
	}
	return nil, errors.New("mounted filesystems changed during discovery")
}

func mountString(value []int8) string {
	buffer := make([]byte, 0, len(value))
	for _, character := range value {
		if character == 0 {
			break
		}
		buffer = append(buffer, byte(character))
	}
	return string(buffer)
}
