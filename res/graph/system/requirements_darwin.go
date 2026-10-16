//go:build darwin

package systemgraph

import (
	"context"
	"syscall"
)

func platformFilesystemAt(ctx context.Context, path string) (FilesystemInfo, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemInfo{}, err
	}
	var mount syscall.Statfs_t
	if err := syscall.Statfs(path, &mount); err != nil {
		return FilesystemInfo{}, err
	}
	return FilesystemInfo{
		MountPoint: mountString(mount.Mntonname[:]), Source: mountString(mount.Mntfromname[:]),
		Type: mountString(mount.Fstypename[:]), ReadOnly: mount.Flags&1 != 0,
		Space: filesystemSpace(mount.Blocks, mount.Bfree, mount.Bavail, uint64(mount.Bsize)),
	}, ctx.Err()
}
