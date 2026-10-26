//go:build darwin && !cgo

package system

import "context"

func platformSurfaces(ctx context.Context, _, _ int) ([]DisplayInfo, CollectionStatus, []WindowInfo, CollectionStatus, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, nil, CollectionStatus{}, "", err
	}
	displays := surfaceStatus("unsupported", "native display enumeration needs the cgo build or the ExtSystem Apple binding")
	windows := surfaceStatus("unsupported", "native window enumeration needs the cgo build or the ExtSystem Apple binding")
	return []DisplayInfo{}, displays, []WindowInfo{}, windows, "desktop-global-points", nil
}
