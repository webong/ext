//go:build !darwin && !windows

package system

import "context"

func platformSurfaces(ctx context.Context, _, _ int) ([]DisplayInfo, CollectionStatus, []WindowInfo, CollectionStatus, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, nil, CollectionStatus{}, "", err
	}
	displays := surfaceStatus("unsupported", "native display enumeration is unavailable on this platform")
	windows := surfaceStatus("unsupported", "native window enumeration is unavailable on this platform")
	return []DisplayInfo{}, displays, []WindowInfo{}, windows, "desktop-global", nil
}
