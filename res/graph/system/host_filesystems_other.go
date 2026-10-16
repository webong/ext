//go:build !darwin && !linux && !windows

package systemgraph

import (
	"context"
	"fmt"
	"runtime"
)

func platformFilesystems(ctx context.Context) ([]FilesystemInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("mounted filesystem discovery is unavailable on %s", runtime.GOOS)
}
