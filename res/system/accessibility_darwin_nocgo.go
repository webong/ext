//go:build darwin && !cgo

package system

import "context"

func platformAccessibility(ctx context.Context, _ AccessibilityOptions) ([]AccessibilityElement, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, err
	}
	return []AccessibilityElement{}, surfaceStatus("unsupported", "macOS Accessibility observation requires cgo or the ExtSystem Swift library"), nil
}

func platformPermissions(ctx context.Context) (PermissionInfo, PermissionInfo, error) {
	if err := ctx.Err(); err != nil {
		return PermissionInfo{}, PermissionInfo{}, err
	}
	unavailable := PermissionInfo{State: "unavailable", Detail: "native permission inspection requires cgo or the ExtSystem Swift library"}
	return unavailable, unavailable, nil
}
