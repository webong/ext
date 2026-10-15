//go:build !darwin && !linux && !windows

package systemgraph

import (
	"context"
	"fmt"
	"runtime"
)

func platformWebviews(ctx context.Context) ([]WebviewInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("shared webview discovery is unavailable on %s", runtime.GOOS)
}
