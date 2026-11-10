//go:build darwin && !cgo

package surface

import (
	"context"
	proc "github.com/webong/ext/res/system/service/process"
)

func platformAccessibility(ctx context.Context, _ AccessibilityOptions) ([]AccessibilityElement, proc.CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, proc.CollectionStatus{}, err
	}
	return []AccessibilityElement{}, surfaceUnsupported("macOS Accessibility observation requires cgo or the ExtSystem Swift library"), nil
}

func platformPermissions(ctx context.Context) (PermissionInfo, PermissionInfo, error) {
	if err := ctx.Err(); err != nil {
		return PermissionInfo{}, PermissionInfo{}, err
	}
	unavailable := PermissionInfo{State: "unavailable", Detail: "native permission inspection requires cgo or the ExtSystem Swift library"}
	return unavailable, unavailable, nil
}
