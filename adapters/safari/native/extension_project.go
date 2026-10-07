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

	"github.com/webong/ext/res/web/extension"
)

var safariBundleID = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)

// PackageSafariProject converts caller-owned WebExtension files to a caller-owned
// Xcode project. The caller remains responsible for building and signing its app.
func PackageSafariProject(ctx context.Context, source, projectLocation, expectedRevision, bundleID, appName string) (extension.InstallResult, error) {
	if runtime.GOOS != "darwin" {
		return extension.InstallResult{}, errors.New("Safari packaging requires macOS and Xcode")
	}
	if !filepath.IsAbs(projectLocation) || !safariBundleID.MatchString(bundleID) || strings.TrimSpace(appName) == "" || filepath.Base(appName) != appName || appName == "." || appName == ".." {
		return extension.InstallResult{}, errors.New("Safari packaging requires an absolute project location, bundle ID, and app name")
	}
	if sourcePath, err := filepath.Abs(source); err == nil && (projectLocation == sourcePath || strings.HasPrefix(projectLocation, sourcePath+string(filepath.Separator))) {
		return extension.InstallResult{}, errors.New("Safari project location must be outside the extension source")
	}
	if expectedRevision == "" {
		return extension.InstallResult{}, errors.New("Safari packaging requires a prepared revision")
	}
	if _, err := os.Stat(projectLocation); err == nil {
		return extension.InstallResult{}, errors.New("Safari project location already exists")
	} else if !os.IsNotExist(err) {
		return extension.InstallResult{}, err
	}
	description, err := extension.Inspect(source)
	if err != nil {
		return extension.InstallResult{}, err
	}
	if description.Revision != expectedRevision {
		return extension.InstallResult{}, errors.New("Safari extension changed since inspection")
	}
	staged, err := os.MkdirTemp("", "ctx-safari-extension-")
	if err != nil {
		return extension.InstallResult{}, err
	}
	defer os.RemoveAll(staged)
	if _, err := extension.StageWithRevision(source, filepath.Join(staged, "extension"), expectedRevision); err != nil {
		return extension.InstallResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(projectLocation), 0755); err != nil {
		return extension.InstallResult{}, err
	}
	tool := "safari-web-extension-packager"
	if _, err := exec.CommandContext(ctx, "xcrun", "-f", tool).Output(); err != nil {
		tool = "safari-web-extension-converter"
	}
	command := exec.CommandContext(ctx, "xcrun", tool,
		"--project-location", projectLocation, "--app-name", appName,
		"--bundle-identifier", bundleID, "--macos-only", "--copy-resources",
		"--no-open", "--no-prompt", filepath.Join(staged, "extension"))
	if output, err := command.CombinedOutput(); err != nil {
		return extension.InstallResult{}, fmt.Errorf("Safari project conversion failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if current, err := extension.Inspect(source); err != nil || current.Revision != expectedRevision {
		return extension.InstallResult{}, errors.New("Safari extension changed during conversion")
	}
	return extension.InstallResult{Status: "packaged", Browser: "safari", Source: projectLocation,
		Extension: &description, NextAction: "Build and sign the generated macOS app with your Apple developer identity, then use extension prepare and extension install with the built .app."}, nil
}
