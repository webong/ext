//go:build linux

package systemgraph

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func platformFilesystemAt(ctx context.Context, path string) (FilesystemInfo, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemInfo{}, err
	}
	// Match the open path's actual mount ID, including bind mounts and mounts
	// stacked at one location. String-prefix matching is insufficient.
	// O_PATH obtains metadata without opening a FIFO/device for I/O or
	// requiring read access to the destination's contents.
	const openPath = 0x200000
	fd, err := syscall.Open(path, openPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return FilesystemInfo{}, err
	}
	pathFile := os.NewFile(uintptr(fd), path)
	if pathFile == nil {
		_ = syscall.Close(fd) // Best-effort cleanup of a handle rejected by os.NewFile.
		return FilesystemInfo{}, fmt.Errorf("could not inspect path handle")
	}
	defer pathFile.Close()
	fdinfo, err := os.ReadFile("/proc/self/fdinfo/" + strconv.FormatUint(uint64(pathFile.Fd()), 10))
	if err != nil {
		return FilesystemInfo{}, err
	}
	mountID := ""
	for _, line := range strings.Split(string(fdinfo), "\n") {
		key, value, _ := strings.Cut(line, ":")
		if key == "mnt_id" {
			mountID = strings.TrimSpace(value)
		}
	}
	if mountID == "" {
		return FilesystemInfo{}, fmt.Errorf("Linux path has no visible mount ID")
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return FilesystemInfo{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return FilesystemInfo{}, err
		}
		id, _, _ := strings.Cut(scanner.Text(), " ")
		if id != mountID {
			continue
		}
		mount, err := parseLinuxMount(scanner.Text())
		if err != nil {
			return FilesystemInfo{}, err
		}
		var stat syscall.Statfs_t
		if err := syscall.Fstatfs(int(pathFile.Fd()), &stat); err != nil {
			return FilesystemInfo{}, err
		}
		size := stat.Frsize
		if size <= 0 {
			size = stat.Bsize
		}
		if size > 0 {
			mount.Space = filesystemSpace(stat.Blocks, stat.Bfree, stat.Bavail, uint64(size))
		}
		return mount, ctx.Err()
	}
	if err := scanner.Err(); err != nil {
		return FilesystemInfo{}, err
	}
	return FilesystemInfo{}, fmt.Errorf("path mount is no longer visible")
}
