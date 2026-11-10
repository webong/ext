//go:build linux && !cgo

package surface

import (
	"context"
	proc "github.com/webong/ext/res/system/service/process"
)

func platformSurfaces(ctx context.Context, _, _ int) ([]DisplayInfo, proc.CollectionStatus, []WindowInfo, proc.CollectionStatus, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, proc.CollectionStatus{}, nil, proc.CollectionStatus{}, "", err
	}
	displays := surfaceUnsupported("Linux display enumeration requires cgo and libX11")
	windows := surfaceUnsupported("Linux window enumeration requires cgo and libX11")
	return []DisplayInfo{}, displays, []WindowInfo{}, windows, "desktop-global-pixels", nil
}
