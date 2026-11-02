//go:build !darwin && !linux && !windows

package host

import (
	"context"
	"fmt"
	"runtime"
)

// FilesystemAt describes the mounted filesystem that contains path, in the same terms as DiscoverFilesystems. It reports an error where the platform cannot answer.
func FilesystemAt(ctx context.Context, path string) (FilesystemInfo, error) {
	if err := ctx.Err(); err != nil {
		return FilesystemInfo{}, err
	}
	return FilesystemInfo{}, fmt.Errorf("filesystem preparation is unavailable on %s", runtime.GOOS)
}
