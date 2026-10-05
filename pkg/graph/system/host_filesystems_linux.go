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

func platformFilesystems(ctx context.Context) ([]FilesystemInfo, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, fmtHostReadError("mounted filesystems", err)
	}
	defer file.Close()
	result := []FilesystemInfo{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mount, err := parseLinuxMount(scanner.Text())
		if err != nil {
			return nil, err
		}
		var stat syscall.Statfs_t
		if err := syscall.Statfs(mount.MountPoint, &stat); err != nil {
			mount.DetailError = "space unavailable: " + err.Error()
		} else {
			size := stat.Frsize
			if size <= 0 {
				size = stat.Bsize
			}
			if size > 0 {
				mount.Space = filesystemSpace(stat.Blocks, stat.Bfree, stat.Bavail, uint64(size))
			}
		}
		result = append(result, mount)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmtHostReadError("mounted filesystems", err)
	}
	return result, nil
}

func parseLinuxMount(line string) (FilesystemInfo, error) {
	before, after, ok := strings.Cut(line, " - ")
	fields, filesystem := strings.Fields(before), strings.Fields(after)
	if !ok || len(fields) < 6 || len(filesystem) < 3 {
		return FilesystemInfo{}, fmt.Errorf("invalid Linux mountinfo record")
	}
	point, err := unescapeMountField(fields[4])
	if err != nil {
		return FilesystemInfo{}, err
	}
	source, err := unescapeMountField(filesystem[1])
	if err != nil {
		return FilesystemInfo{}, err
	}
	readonly := false
	for _, option := range strings.Split(fields[5]+","+filesystem[2], ",") {
		if option == "ro" {
			readonly = true
		}
	}
	return FilesystemInfo{MountPoint: point, Source: source, Type: filesystem[0], ReadOnly: readonly}, nil
}

func unescapeMountField(value string) (string, error) {
	var decoded strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			decoded.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", fmt.Errorf("invalid Linux mountinfo escape")
		}
		character, err := strconv.ParseUint(value[i+1:i+4], 8, 8)
		if err != nil || character == 0 {
			return "", fmt.Errorf("invalid Linux mountinfo escape")
		}
		decoded.WriteByte(byte(character))
		i += 3
	}
	return decoded.String(), nil
}
