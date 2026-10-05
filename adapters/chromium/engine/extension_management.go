package chromium

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	management "github.com/webong/ctx/res/browser/contract"
	"github.com/webong/ctx/res/browser/extension"
	kit "github.com/webong/ctx/res/browser/guest"
	"github.com/webong/ctx/res/browser/userscript"
)

// ExtensionManagementConfig contains product conventions supplied by an adapter.
// An empty ExtensionPage leaves native installation and activation unavailable.
type ExtensionManagementConfig struct {
	Executables                    extension.ExecutableLocations
	ExtensionPage                  string
	DebuggingRequiresCustomProfile bool
	LinuxConfigHomeEnv             string
	Store                          *StoreConfig
	ManagedPolicyDrivers           map[string]string
	LinuxPolicyPath                string
}

type extensionBackend struct{ config Config }

// RunManagement serves portable workflows and this adapter's native operations.
func RunManagement(config Config, profile string, input io.Reader, stdout, stderr io.Writer) int {
	return kit.RunLocalManagement(nil, config.Name, profile, input, stdout, stderr, extensionBackend{config: config})
}

type extensionInput struct {
	Source            string `json:"source"`
	Output            string `json:"output"`
	Dest              string `json:"destination"`
	Revision          string `json:"revision"`
	Executable        string `json:"executable"`
	KeyPath           string `json:"keyPath"`
	TargetID          string `json:"targetId"`
	ID                string `json:"id"`
	Store             string `json:"store"`
	UpdateURL         string `json:"updateURL"`
	CodebaseURL       string `json:"codebaseURL"`
	Version           string `json:"version"`
	ExternalDirectory string `json:"externalDirectory"`
	PolicyAction      string `json:"policyAction"`
	ProfilePath       string `json:"profilePath"`
	ProfileDirectory  string `json:"profileDirectory"`
}

func (backend extensionBackend) ManageExtension(ctx context.Context, profile, action string, raw json.RawMessage, progress func(extension.InstallResult) error) (any, string, error) {
	var input extensionInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, "", fmt.Errorf("invalid native extension input: %w", err)
		}
	}
	switch action {
	case "targets":
		if input.Executable != "" || input.ProfilePath != "" || filepath.IsAbs(profile) {
			target, err := backend.resolveTargetInput(profile, input)
			if err != nil {
				return nil, "", err
			}
			result := extension.NewTarget(backend.config.Name, target.ExecutablePath, target.ProfilePath, target.ProfileDirectory, backend.config.Name+" — "+profile)
			result.Profile = profile
			capability := extensionCapability(backend.config)
			if !capability.PersistentLocalInstall {
				result.InstallMode, result.Reason = "unsupported", capability.Reason
			} else if target.RestrictedProfilePath != "" && sameExtensionPath(target.ProfilePath, target.RestrictedProfilePath) {
				result.InstallMode, result.Reason = "manual-stage", "The selected browser requires manual installation in its default user data directory"
			}
			return []extension.BrowserTarget{result}, "ready", nil
		}
		targets, err := discoverExtensionTargets(backend.config)
		return targets, "ready", err
	case "capabilities":
		return extensionCapability(backend.config), "ready", nil
	case "sign":
		result, err := SignChromiumCRX(ctx, backend.config.Name, input.Executable, input.Source, input.Output, input.Revision, input.KeyPath)
		return result, "signed", err
	case "update_manifest":
		result, err := CRXUpdateManifest(input.Source, input.Revision, input.CodebaseURL, input.Output)
		return result, result.Status, err
	case "policy":
		result, err := ManageExtensionPolicy(ctx, backend.config, input.Store, input.ID, input.PolicyAction)
		return result, result.Status, err
	case "store_install", "store_remove":
		if backend.config.Extensions.Store == nil {
			return nil, "", fmt.Errorf("adapter %s has no external store request route", backend.config.Name)
		}
		result, err := changeDistributionInstall(runtime.GOOS, backend.config.Name, *backend.config.Extensions.Store, input, action == "store_remove")
		return result, result.Status, err
	case "install", "activate":
		if strings.EqualFold(filepath.Ext(input.Source), ".crx") {
			return nil, "", fmt.Errorf("CRX registration uses store_install on supported platforms; install and activate require source directories or ZIPs for Load unpacked")
		}
		capability := extensionCapability(backend.config)
		if (action == "install" && !capability.PersistentLocalInstall) || (action == "activate" && !capability.SessionLoad) {
			return nil, "", fmt.Errorf("%s", capability.Reason)
		}
		target, err := backend.resolveTargetInput(profile, input)
		if err != nil {
			return nil, "", err
		}
		var latest extension.InstallResult
		report := func(result extension.InstallResult) error {
			latest = result
			return progress(result)
		}
		if action == "install" {
			err = InstallWithNativeUI(ctx, target, input.Source, input.Dest, input.Revision, report)
		} else {
			err = RunWithDevTools(ctx, target, input.Source, input.Dest, input.Revision, report)
		}
		return latest, latest.Status, err
	default:
		return nil, "", fmt.Errorf("adapter %s does not implement extension operation %q", backend.config.Name, action)
	}
}

func (backend extensionBackend) ActivateUserscript(ctx context.Context, profile string, record userscript.Record, progress func(extension.InstallResult) error) (any, error) {
	return backend.ActivateUserscriptTarget(ctx, profile, record, nil, progress)
}

func (backend extensionBackend) ActivateUserscriptTarget(ctx context.Context, profile string, record userscript.Record, raw json.RawMessage, progress func(extension.InstallResult) error) (any, error) {
	var input extensionInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
	}
	if capability := extensionCapability(backend.config); !capability.SessionLoad {
		return nil, fmt.Errorf("userscript session activation is unavailable: %s", capability.Reason)
	}
	target, err := backend.resolveTargetInput(profile, input)
	if err != nil {
		return nil, err
	}
	return activateUserscript(ctx, target, record, progress)
}

func (backend extensionBackend) resolveTarget(profile, id string) (DevToolsTarget, error) {
	targets, err := discoverExtensionTargets(backend.config)
	if err != nil {
		return DevToolsTarget{}, err
	}
	target, err := extension.ResolveTarget(targets, backend.config.Name, profile, id)
	if err != nil {
		return DevToolsTarget{}, err
	}
	resolved := DevToolsTarget{Browser: target.Browser, ExecutablePath: target.ExecutablePath,
		ProfilePath: target.ProfilePath, ProfileDirectory: target.ProfileDirectory,
		ExtensionPage: backend.config.Extensions.ExtensionPage}
	if backend.config.Extensions.DebuggingRequiresCustomProfile {
		resolved.RestrictedProfilePath, err = extensionProfileRoot(backend.config)
	}
	return resolved, err
}

// resolveTargetInput binds custom roots to the explicit profile selector.
func (backend extensionBackend) resolveTargetInput(profile string, input extensionInput) (DevToolsTarget, error) {
	if input.TargetID != "" && (input.Executable != "" || input.ProfilePath != "" || input.ProfileDirectory != "") {
		return DevToolsTarget{}, fmt.Errorf("targetId cannot be combined with explicit executable or profile fields")
	}
	if input.Executable == "" && input.ProfilePath == "" && !filepath.IsAbs(profile) {
		if input.ProfileDirectory != "" && input.ProfileDirectory != profile {
			return DevToolsTarget{}, fmt.Errorf("profileDirectory must match the selected profile")
		}
		return backend.resolveTarget(profile, input.TargetID)
	}
	if input.TargetID != "" {
		return DevToolsTarget{}, fmt.Errorf("custom profiles require explicit paths, not a discovered targetId")
	}
	if !filepath.IsAbs(profile) {
		return DevToolsTarget{}, fmt.Errorf("custom targeting requires selecting %s:<absolute user data directory>", backend.config.Name)
	}
	if input.ProfilePath != "" && (!filepath.IsAbs(input.ProfilePath) || !sameExtensionPath(profile, input.ProfilePath)) {
		return DevToolsTarget{}, fmt.Errorf("profilePath must match the selected user data directory")
	}
	executable := input.Executable
	if executable == "" {
		found, err := extension.FindExecutables(backend.config.Extensions.Executables)
		if err != nil {
			return DevToolsTarget{}, err
		}
		if len(found) != 1 {
			return DevToolsTarget{}, fmt.Errorf("custom targeting requires an explicit executable when discovery finds %d executables", len(found))
		}
		executable = found[0]
	}
	if !filepath.IsAbs(executable) {
		return DevToolsTarget{}, fmt.Errorf("executable must be an absolute path")
	}
	info, err := os.Stat(executable)
	if err != nil {
		return DevToolsTarget{}, err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return DevToolsTarget{}, fmt.Errorf("browser executable is not executable")
	}
	directory := input.ProfileDirectory
	if directory == "" {
		directory = "Default"
	}
	if directory == "." || directory == ".." || filepath.Base(directory) != directory || strings.ContainsAny(directory, "/\\") {
		return DevToolsTarget{}, fmt.Errorf("profileDirectory must be a single directory name")
	}
	target := DevToolsTarget{Browser: backend.config.Name, ExecutablePath: executable, ProfilePath: filepath.Clean(profile), ProfileDirectory: directory, ExtensionPage: backend.config.Extensions.ExtensionPage}
	if backend.config.Extensions.DebuggingRequiresCustomProfile {
		target.RestrictedProfilePath, err = extensionProfileRoot(backend.config)
	}
	return target, err
}

func extensionCapability(config Config) extension.InstallCapability {
	result := extension.InstallCapability{Browser: config.Name, UpdateManifest: true}
	if store := config.Extensions.Store; store != nil {
		result.ExternalStoreRequest = runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows"
		result.ExternalLocalPackage = runtime.GOOS == "linux" && store.LinuxLocalCRX
		result.ExternalUpdateURL = runtime.GOOS == "linux" && store.LinuxUpdateURL
		result.ExternalInstallScope = "machine"
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" && store.LinuxDirectoryInHome {
			result.ExternalInstallScope = "browser-user"
		}
		for name := range store.UpdateURLs {
			result.SupportedStores = append(result.SupportedStores, name)
		}
		sort.Strings(result.SupportedStores)
		if runtime.GOOS == "windows" {
			result.ExternalStoreDriver = "powershell-registry"
		} else if result.ExternalStoreRequest {
			result.ExternalStoreDriver = "external-preferences-json"
		}
	}
	result.ManagedPolicyDriver = config.Extensions.ManagedPolicyDrivers[runtime.GOOS]
	result.ManagedPolicy = result.ManagedPolicyDriver != ""
	if config.Extensions.ExtensionPage == "" {
		result.Reason = "this adapter has no native extension installation or activation route"
		return result
	}
	result.PersistentLocalInstall = true
	result.InstallDriver = "native-load-unpacked"
	result.RequiresBrowserAction = true
	result.SessionLoad = true
	result.SessionDriver = "cdp-pipe"
	result.Requires = []string{"executable", "profilePath", "revision", "source", "destination"}
	result.Reason = "Persistent installation requires the browser's Load unpacked action; DevTools loading lasts for the browser session"
	if config.Extensions.DebuggingRequiresCustomProfile {
		result.Reason += "; the selected browser requires a custom user data directory for debugging-pipe verification"
	}
	return result
}

func extensionProfileRoot(config Config) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", config.MacUserData), nil
	case "windows":
		environment := "LOCALAPPDATA"
		if config.WindowsRoaming {
			environment = "APPDATA"
		}
		base := os.Getenv(environment)
		if base == "" {
			return "", fmt.Errorf("%s is not set", environment)
		}
		return filepath.Join(base, filepath.FromSlash(config.WindowsUserData)), nil
	default:
		var base string
		if config.Extensions.LinuxConfigHomeEnv != "" {
			base = os.Getenv(config.Extensions.LinuxConfigHomeEnv)
		}
		if base == "" {
			base = os.Getenv("XDG_CONFIG_HOME")
		}
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, config.LinuxUserData), nil
	}
}

func discoverExtensionTargets(config Config) ([]extension.BrowserTarget, error) {
	executables, err := extension.FindExecutables(config.Extensions.Executables)
	if err != nil {
		return nil, err
	}
	targets := make([]extension.BrowserTarget, 0)
	if len(executables) == 0 {
		return targets, nil
	}
	root, err := extensionProfileRoot(config)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return targets, nil
	}
	if err != nil {
		return nil, err
	}
	capability := extensionCapability(config)
	for _, executable := range executables {
		for _, entry := range entries {
			if !entry.IsDir() || (entry.Name() != "Default" && !strings.HasPrefix(entry.Name(), "Profile ")) {
				continue
			}
			target := extension.NewTarget(config.Name, executable, root, entry.Name(), config.Name+" — "+entry.Name())
			target.Profile = entry.Name()
			if !capability.PersistentLocalInstall {
				target.InstallMode, target.Reason = "unsupported", capability.Reason
			} else if config.Extensions.DebuggingRequiresCustomProfile {
				target.InstallMode = "manual-stage"
				target.Reason = "The selected browser does not allow debugging-pipe verification in its default user data directory"
			}
			targets = append(targets, target)
		}
	}
	return targets, nil
}

func (backend extensionBackend) PageRuntime(ctx context.Context, profile string, raw json.RawMessage) (management.PageSessionRuntime, error) {
	var input struct {
		Endpoint      string                   `json:"endpoint"`
		Target        management.SessionTarget `json:"target"`
		SessionTarget management.SessionTarget `json:"sessionTarget"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
	}
	endpoint := input.Endpoint
	if endpoint == "" {
		endpoint = input.Target.Endpoint
	}
	if endpoint == "" {
		endpoint = input.SessionTarget.Endpoint
	}
	if backend.config.Extensions.ExtensionPage == "" {
		return nil, fmt.Errorf("this adapter has no CDP page route")
	}
	return NewPageSessionRuntime(backend.config, endpoint), nil
}
