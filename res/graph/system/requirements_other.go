//go:build !darwin && !linux && !windows

package systemgraph

import (
	"context"
	"fmt"
	"runtime"
)

func platformFilesystemAt(ctx context.Context, path string) (FilesystemInfo, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemInfo{}, err
	}
	return FilesystemInfo{}, fmt.Errorf("filesystem preparation is unavailable on %s", runtime.GOOS)
}
