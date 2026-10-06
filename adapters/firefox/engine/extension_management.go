package firefox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	management "github.com/webong/ctx/res/browser/contract"
	"github.com/webong/ctx/res/browser/discovery"
	"github.com/webong/ctx/res/browser/extension"
)

type extensionBackend struct{ config Config }

type extensionInput struct {
	Source      string `json:"source"`
	Output      string `json:"output"`
	Revision    string `json:"revision"`
	TargetID    string `json:"targetId"`
	Executable  string `json:"executable"`
	ProfilePath string `json:"profilePath"`
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
			target, err := backend.resolveExtensionTarget(profile, input)
			if err != nil {
				return nil, "", err
			}
			if !backend.config.NativeExtensions {
				target.InstallMode, target.Reason = "unsupported", extensionCapability(backend.config).Reason
			}
			return []extension.BrowserTarget{target}, "ready", err
		}
		targets, err := discoverExtensionTargets(backend.config)
		return targets, "ready", err
	case "capabilities":
		return extensionCapability(backend.config), "ready", nil
	case "sign":
		result, err := SignFirefoxXPI(ctx, input.Source, input.Output, input.Revision)
		result.Browser = backend.config.Name
		return result, "signed", err
	case "install", "activate":
		if !backend.config.NativeExtensions {
			return nil, "", fmt.Errorf("adapter %s has no native extension installation or activation route", backend.config.Name)
		}
		target, err := backend.resolveExtensionTarget(profile, input)
		if err != nil {
			return nil, "", err
		}
		var latest extension.InstallResult
		report := func(result extension.InstallResult) error {
			latest = result
			return progress(result)
		}
		if action == "install" {
			err = InstallExtension(ctx, target, input.Source, input.Revision, report)
		} else {
			err = ActivateExtension(ctx, target, input.Source, input.Revision, report)
		}
		return latest, latest.Status, err
	default:
		return nil, "", fmt.Errorf("adapter %s does not implement extension operation %q", backend.config.Name, action)
	}
}

func (backend extensionBackend) resolveExtensionTarget(profile string, input extensionInput) (extension.BrowserTarget, error) {
	if input.TargetID != "" && (input.Executable != "" || input.ProfilePath != "") {
		return extension.BrowserTarget{}, fmt.Errorf("targetId cannot be combined with explicit paths")
	}
	if input.Executable == "" && input.ProfilePath == "" && !filepath.IsAbs(profile) {
		targets, err := discoverExtensionTargets(backend.config)
		if err != nil {
			return extension.BrowserTarget{}, err
		}
		return extension.ResolveTarget(targets, backend.config.Name, profile, input.TargetID)
	}
	if input.TargetID != "" || !filepath.IsAbs(profile) {
		return extension.BrowserTarget{}, fmt.Errorf("custom targeting requires selecting %s:<absolute profile directory>", backend.config.Name)
	}
	if input.ProfilePath != "" {
		a, b := filepath.Clean(profile), filepath.Clean(input.ProfilePath)
		if resolved, err := filepath.EvalSymlinks(a); err == nil {
			a = resolved
		}
		if resolved, err := filepath.EvalSymlinks(b); err == nil {
			b = resolved
		}
		if !filepath.IsAbs(input.ProfilePath) || a != b {
			return extension.BrowserTarget{}, fmt.Errorf("profilePath must match the selected profile")
		}
	}
	executable := input.Executable
	if executable == "" {
		found, err := discovery.FindExecutables(backend.config.ExtensionExecutables)
		if err != nil {
			return extension.BrowserTarget{}, err
		}
		if len(found) != 1 {
			return extension.BrowserTarget{}, fmt.Errorf("custom targeting requires an explicit executable when discovery finds %d executables", len(found))
		}
		executable = found[0]
	}
	if !filepath.IsAbs(executable) {
		return extension.BrowserTarget{}, fmt.Errorf("executable must be an absolute path")
	}
	info, err := os.Stat(executable)
	if err != nil {
		return extension.BrowserTarget{}, err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return extension.BrowserTarget{}, fmt.Errorf("browser executable is not executable")
	}
	target := extension.NewTarget(backend.config.Name, executable, filepath.Clean(profile), "", backend.config.Name+" — "+profile)
	target.Profile = profile
	return target, nil
}

func extensionCapability(config Config) extension.InstallCapability {
	if !config.NativeExtensions {
		return extension.InstallCapability{Browser: config.Name, Reason: "this adapter has no native extension installation or activation route"}
	}
	return extension.InstallCapability{Browser: config.Name, PersistentLocalInstall: true,
		InstallDriver: "webdriver-bidi-signed-xpi", SessionLoad: true,
		SessionDriver: "webdriver-bidi-temporary-xpi",
		Requires:      []string{"executable", "profilePath", "source", "revision"},
		Reason:        "Firefox accepts a permanently installed XPI only when its signature is valid; temporary activation uses WebDriver BiDi and does not persist"}
}

func discoverExtensionTargets(config Config) ([]extension.BrowserTarget, error) {
	executables, err := discovery.FindExecutables(config.ExtensionExecutables)
	if err != nil {
		return nil, err
	}
	targets := make([]extension.BrowserTarget, 0)
	if len(executables) == 0 {
		return targets, nil
	}
	ini, err := firefoxProfilesINI(config)
	if err != nil {
		return nil, err
	}
	profiles, err := extensionProfiles(filepath.Dir(ini))
	if err != nil {
		return nil, err
	}
	for _, executable := range executables {
		for _, profile := range profiles {
			target := extension.NewTarget(config.Name, executable, profile.path, "", config.Name+" — "+profile.name)
			target.Profile = profile.name
			if !config.NativeExtensions {
				target.InstallMode, target.Reason = "unsupported", extensionCapability(config).Reason
			}
			targets = append(targets, target)
		}
	}
	return targets, nil
}

type extensionProfile struct{ name, path string }

func extensionProfiles(root string) ([]extensionProfile, error) {
	profiles := make([]extensionProfile, 0)
	seen := map[string]bool{}
	add := func(path, name string) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.IsDir() || seen[absolute] {
			return
		}
		if name == "" {
			name = filepath.Base(absolute)
		}
		seen[absolute] = true
		profiles = append(profiles, extensionProfile{name: name, path: absolute})
	}
	file, err := os.Open(filepath.Join(root, "profiles.ini"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		defer file.Close()
		section := ""
		values := map[string]string{}
		flush := func() {
			if !strings.HasPrefix(section, "Profile") || values["Path"] == "" {
				return
			}
			path := filepath.FromSlash(values["Path"])
			if values["IsRelative"] != "0" && !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			add(path, values["Name"])
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				flush()
				section = strings.Trim(line, "[]")
				values = map[string]string{}
			} else if key, value, ok := strings.Cut(line, "="); ok {
				values[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		flush()
	}
	entries, err := os.ReadDir(filepath.Join(root, "Profiles"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			add(filepath.Join(root, "Profiles", entry.Name()), entry.Name())
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].path < profiles[j].path })
	return profiles, nil
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
	if !backend.config.NativeExtensions {
		return nil, fmt.Errorf("this adapter has no BiDi page route")
	}
	return NewPageSessionRuntime(backend.config, endpoint), nil
}
