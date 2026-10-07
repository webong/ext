package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/webong/ext/res/browser/extension"
)

var safariCDHash = regexp.MustCompile(`(?m)^CDHash=([0-9a-fA-F]+)$`)

// InspectSafariApp reads a signed app's native Safari Web Extension identity.
// The returned CDHash binds later review to the signed app bundle.
func InspectSafariApp(ctx context.Context, appPath string) (extension.Description, error) {
	if runtime.GOOS != "darwin" {
		return extension.Description{}, errors.New("Safari app inspection requires macOS")
	}
	if !filepath.IsAbs(appPath) || !strings.EqualFold(filepath.Ext(appPath), ".app") {
		return extension.Description{}, errors.New("Safari source must be an absolute .app path")
	}
	info, err := os.Lstat(appPath)
	if err != nil {
		return extension.Description{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return extension.Description{}, errors.New("Safari app must be a directory, not a symlink")
	}
	if output, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", appPath).CombinedOutput(); err != nil {
		return extension.Description{}, fmt.Errorf("Safari app signature verification failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	output, err := exec.CommandContext(ctx, "codesign", "-dv", "--verbose=4", appPath).CombinedOutput()
	if err != nil {
		return extension.Description{}, err
	}
	match := safariCDHash.FindSubmatch(output)
	if len(match) != 2 {
		return extension.Description{}, errors.New("Safari app has no code-signing hash")
	}
	plugins := filepath.Join(appPath, "Contents", "PlugIns")
	entries, err := os.ReadDir(plugins)
	if err != nil {
		return extension.Description{}, err
	}
	var extensionID string
	for _, entry := range entries {
		if !entry.IsDir() || filepath.Ext(entry.Name()) != ".appex" {
			continue
		}
		plist := filepath.Join(plugins, entry.Name(), "Contents", "Info.plist")
		point, err := safariPlist(ctx, plist, "NSExtension.NSExtensionPointIdentifier")
		if err != nil || point != "com.apple.Safari.web-extension" {
			continue
		}
		if extensionID != "" {
			return extension.Description{}, errors.New("Safari app contains more than one web extension; select a single-extension app")
		}
		extensionID, err = safariPlist(ctx, plist, "CFBundleIdentifier")
		if err != nil {
			return extension.Description{}, err
		}
	}
	if extensionID == "" {
		return extension.Description{}, errors.New("Safari app does not contain a Safari web extension")
	}
	name, err := safariPlist(ctx, filepath.Join(appPath, "Contents", "Info.plist"), "CFBundleName")
	if err != nil {
		name = filepath.Base(appPath)
	}
	return extension.Description{Name: name, ID: extensionID, Revision: "cdhash:" + strings.ToLower(string(match[1]))}, nil
}

func safariPlist(ctx context.Context, path, key string) (string, error) {
	output, err := exec.CommandContext(ctx, "plutil", "-extract", key, "raw", "-o", "-", path).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// InstallSafariApp opens the caller's signed containing app. Safari's
// extension enablement and per-profile permissions remain under user control.
func InstallSafariApp(ctx context.Context, appPath, expectedRevision string) (extension.InstallResult, error) {
	if expectedRevision == "" {
		return extension.InstallResult{}, errors.New("Safari installation requires a prepared app revision")
	}
	description, err := InspectSafariApp(ctx, appPath)
	if err != nil {
		return extension.InstallResult{}, err
	}
	if description.Revision != expectedRevision {
		return extension.InstallResult{}, errors.New("Safari app changed since inspection")
	}
	if output, err := exec.CommandContext(ctx, "open", appPath).CombinedOutput(); err != nil {
		return extension.InstallResult{}, fmt.Errorf("launch Safari extension app: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return extension.InstallResult{Status: "awaiting-browser-action", Browser: "safari", ID: description.ID,
		Source: appPath, Extension: &description,
		NextAction: "Open Safari > Settings > Extensions, enable this extension, then allow it for the intended Safari profile and websites. Safari controls those permissions."}, nil
}
