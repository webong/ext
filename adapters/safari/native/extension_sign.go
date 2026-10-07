package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/webong/ext/res/web/extension"
)

// BuildSafariApp builds and signs a caller-owned Xcode project for local
// Safari use. App Store distribution and Developer ID notarization are separate.
func BuildSafariApp(ctx context.Context, project, scheme, output, teamID string) (extension.BuildResult, error) {
	if runtime.GOOS != "darwin" {
		return extension.BuildResult{}, errors.New("Safari app building requires macOS and Xcode")
	}
	if !filepath.IsAbs(project) || !strings.EqualFold(filepath.Ext(project), ".xcodeproj") ||
		strings.TrimSpace(scheme) == "" || teamID == "" || strings.ContainsAny(teamID, " \t\n=") {
		return extension.BuildResult{}, errors.New("Safari building requires an absolute .xcodeproj path, scheme, and developer team ID")
	}
	if info, err := os.Stat(project); err != nil || !info.IsDir() {
		return extension.BuildResult{}, errors.New("Safari Xcode project is unavailable")
	}
	if path := filepath.Clean(output); path == filepath.Clean(project) || strings.HasPrefix(path, filepath.Clean(project)+string(filepath.Separator)) {
		return extension.BuildResult{}, errors.New("signed app output must be outside the Xcode project")
	}
	if err := extension.UnusedArtifact(output, ".app"); err != nil {
		return extension.BuildResult{}, err
	}
	work, err := os.MkdirTemp("", "ctx-safari-build-")
	if err != nil {
		return extension.BuildResult{}, err
	}
	defer os.RemoveAll(work)
	derived := filepath.Join(work, "derived")
	command := exec.CommandContext(ctx, "xcodebuild", "-project", project, "-scheme", scheme,
		"-configuration", "Release", "-destination", "generic/platform=macOS", "-derivedDataPath", derived,
		"DEVELOPMENT_TEAM="+teamID, "CODE_SIGN_STYLE=Automatic", "CODE_SIGNING_ALLOWED=YES", "build")
	if outputText, err := command.CombinedOutput(); err != nil {
		return extension.BuildResult{}, fmt.Errorf("Safari Xcode build failed: %w: %s", err, strings.TrimSpace(string(outputText)))
	}
	products := filepath.Join(derived, "Build", "Products", "Release")
	entries, err := os.ReadDir(products)
	if err != nil {
		return extension.BuildResult{}, err
	}
	var app string
	for _, entry := range entries {
		if entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".app") {
			if app != "" {
				return extension.BuildResult{}, errors.New("Xcode produced multiple apps; select a single-app scheme")
			}
			app = filepath.Join(products, entry.Name())
		}
	}
	if app == "" {
		return extension.BuildResult{}, errors.New("Xcode did not produce a macOS app")
	}
	if description, err := InspectSafariApp(ctx, app); err != nil || description.Revision == "" {
		return extension.BuildResult{}, fmt.Errorf("built Safari app is not signed with a Safari WebExtension: %w", err)
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return extension.BuildResult{}, fmt.Errorf("reserve signed Safari app output: %w", err)
	}
	if outputText, err := exec.CommandContext(ctx, "ditto", app, output).CombinedOutput(); err != nil {
		_ = os.RemoveAll(output)
		return extension.BuildResult{}, fmt.Errorf("copy signed Safari app: %w: %s", err, strings.TrimSpace(string(outputText)))
	}
	if info, err := os.Stat(app); err == nil {
		if err := os.Chmod(output, info.Mode().Perm()); err != nil {
			_ = os.RemoveAll(output)
			return extension.BuildResult{}, err
		}
	}
	description, err := InspectSafariApp(ctx, output)
	if err != nil {
		_ = os.RemoveAll(output)
		return extension.BuildResult{}, err
	}
	return extension.BuildResult{Status: "signed", Browser: "safari", Source: output, ArtifactRevision: description.Revision,
		NextAction: "Use the signed app and artifactRevision with extension install, then enable it in Safari Settings. Distribution may require Developer ID signing and notarization."}, nil
}
