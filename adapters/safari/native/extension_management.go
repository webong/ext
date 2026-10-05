package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/webong/ctx/adapters/browserdiscovery"
	"github.com/webong/ctx/res/browser/extension"
)

type safariExtensionBackend struct{}

type safariExtensionInput struct {
	Source   string `json:"source"`
	Output   string `json:"output"`
	Revision string `json:"revision"`
	Project  string `json:"project"`
	Scheme   string `json:"scheme"`
	TeamID   string `json:"teamId"`
	BundleID string `json:"bundleId"`
	AppName  string `json:"appName"`
}

func (safariExtensionBackend) InspectExtension(ctx context.Context, source string) (extension.Description, error) {
	if strings.EqualFold(filepath.Ext(source), ".app") {
		return InspectSafariApp(ctx, source)
	}
	return extension.Inspect(source)
}

func (safariExtensionBackend) ManageExtension(ctx context.Context, profile, action string, raw json.RawMessage, progress func(extension.InstallResult) error) (any, string, error) {
	var input safariExtensionInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, "", fmt.Errorf("invalid native extension input: %w", err)
		}
	}
	switch action {
	case "targets":
		executables, err := browserdiscovery.FindExecutables(browserdiscovery.ExecutableLocations{Darwin: []string{"Safari.app/Contents/MacOS/Safari"}})
		if err != nil {
			return nil, "", err
		}
		targets := make([]extension.BrowserTarget, 0, len(executables))
		for _, executable := range executables {
			target := extension.NewTarget("safari", executable, "", "", "Safari")
			target.Profile, target.InstallMode = "default", "app-handoff"
			target.Reason = "Safari profile enablement is selected in Safari Settings"
			targets = append(targets, target)
		}
		return targets, "ready", nil
	case "capabilities":
		if runtime.GOOS != "darwin" {
			return extension.InstallCapability{Browser: "safari", Reason: "Safari WebExtension installation requires macOS and a containing app"}, "ready", nil
		}
		return extension.InstallCapability{Browser: "safari", PersistentLocalInstall: true,
			InstallDriver: "signed-containing-app", RequiresBrowserAction: true,
			Requires: []string{"source", "revision"},
			Reason:   "The caller supplies a signed macOS app containing a Safari WebExtension; Safari requires the user to enable it in Settings"}, "ready", nil
	case "convert":
		result, err := PackageSafariProject(ctx, input.Source, input.Output, input.Revision, input.BundleID, input.AppName)
		return result, result.Status, err
	case "sign":
		result, err := BuildSafariApp(ctx, input.Project, input.Scheme, input.Output, input.TeamID)
		return result, "signed", err
	case "install":
		result, err := InstallSafariApp(ctx, input.Source, input.Revision)
		return result, result.Status, err
	default:
		return nil, "", fmt.Errorf("Safari does not implement extension operation %q", action)
	}
}
