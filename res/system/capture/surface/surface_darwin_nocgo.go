//go:build darwin && !cgo

package surface

import (
	"context"
	proc "github.com/webong/ext/res/system/service/process"
)

func platformSurfaces(ctx context.Context, _, _ int) ([]DisplayInfo, proc.CollectionStatus, []WindowInfo, proc.CollectionStatus, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, proc.CollectionStatus{}, nil, proc.CollectionStatus{}, "", err
	}
	displays := surfaceStatus("unsupported", "native display enumeration needs the cgo build or the ExtSystem Apple binding")
	windows := surfaceStatus("unsupported", "native window enumeration needs the cgo build or the ExtSystem Apple binding")
	return []DisplayInfo{}, displays, []WindowInfo{}, windows, "desktop-global-points", nil
}
