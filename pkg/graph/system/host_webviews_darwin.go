//go:build darwin

package systemgraph

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func platformWebviews(ctx context.Context) ([]WebviewInfo, error) {
	const framework = "/System/Library/Frameworks/WebKit.framework"
	info, err := os.Stat(framework)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmtHostReadError("system WebKit framework", err)
	}
	if !info.IsDir() {
		return nil, nil
	}
	item := WebviewInfo{Name: "WebKit", Engine: "webkit", API: "WKWebView", Location: framework, Scope: "system", Source: "system-framework"}
	resolved, err := filepath.EvalSymlinks(framework)
	if err != nil {
		item.DetailError = "framework path unavailable: " + err.Error()
	} else {
		item.ResolvedPath = resolved
	}
	// macOS system frameworks may live in the dyld shared cache. Requiring a
	// standalone WebKit binary would incorrectly hide the native runtime.
	// The system plist utility handles both XML and binary framework metadata.
	metadataContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := exec.CommandContext(metadataContext, "/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(framework, "Resources", "Info.plist")).Output()
	if err == nil {
		item.Version = strings.TrimSpace(string(version))
	} else {
		if item.DetailError != "" {
			item.DetailError += "; "
		}
		item.DetailError += "framework version unavailable: " + err.Error()
	}
	return []WebviewInfo{item}, ctx.Err()
}
