//go:build !darwin && !windows

package system

import (
	"context"
	"os"
	"strings"
)

func platformAccessibility(ctx context.Context, _ AccessibilityOptions) ([]AccessibilityElement, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, err
	}
	return []AccessibilityElement{}, surfaceStatus("unsupported", "native accessibility tree observation is unavailable on this platform"), nil
}

func platformPermissions(ctx context.Context) (PermissionInfo, PermissionInfo, error) {
	if err := ctx.Err(); err != nil {
		return PermissionInfo{}, PermissionInfo{}, err
	}
	accessibility := PermissionInfo{State: "unsupported", Scope: "linux.at-spi", Detail: "AT-SPI observation is not available in this build"}
	recording := PermissionInfo{State: "unavailable", Detail: "no local graphical session is visible"}
	if strings.TrimSpace(os.Getenv("DISPLAY")) != "" {
		recording = PermissionInfo{State: "not-required", Scope: "linux.x11-capture"}
	} else if strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" {
		recording = PermissionInfo{State: "not-determined", Scope: "linux.pipewire-portal", Detail: "portal consent is determined when capture is requested"}
	}
	return accessibility, recording, nil
}
