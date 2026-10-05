//go:build !windows

package systemgraph

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
)

func platformShellPaths(ctx context.Context) ([]shellPath, error) {
	file, err := os.Open("/etc/shells")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmtHostReadError("registered shells", err)
	}
	defer file.Close()
	paths := []shellPath{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, _, _ := strings.Cut(scanner.Text(), "#")
		path := strings.TrimSpace(line)
		if path != "" {
			paths = append(paths, shellPath{path, "registered"})
		}
	}
	return paths, scanner.Err()
}

func hostExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 && syscall.Access(path, 1) == nil
}
