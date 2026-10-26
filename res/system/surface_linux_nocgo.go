//go:build linux && !cgo

package system

import "context"

func platformSurfaces(ctx context.Context, _, _ int) ([]DisplayInfo, CollectionStatus, []WindowInfo, CollectionStatus, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, nil, CollectionStatus{}, "", err
	}
	displays := surfaceStatus("unsupported", "Linux display enumeration requires cgo and libX11")
	windows := surfaceStatus("unsupported", "Linux window enumeration requires cgo and libX11")
	return []DisplayInfo{}, displays, []WindowInfo{}, windows, "desktop-global-pixels", nil
}
