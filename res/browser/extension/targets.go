package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BrowserTarget describes an installation and profile discovered by an adapter.
type BrowserTarget struct {
	ID               string `json:"id"`
	Browser          string `json:"browser"`
	Name             string `json:"name"`
	Profile          string `json:"profile,omitempty"`
	ExecutablePath   string `json:"executablePath"`
	ProfilePath      string `json:"profilePath,omitempty"`
	ProfileDirectory string `json:"profileDirectory,omitempty"`
	InstallMode      string `json:"installMode"`
	Reason           string `json:"reason,omitempty"`
}

// NewTarget gives adapter-discovered paths a stable identity. The adapter sets
// its profile selector, installation mode, and native requirements.
func NewTarget(browser, executable, profile, directory, name string) BrowserTarget {
	key := strings.Join([]string{browser, executable, profile, directory}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return BrowserTarget{ID: browser + ":" + hex.EncodeToString(sum[:12]), Browser: browser,
		Name: name, ExecutablePath: executable, ProfilePath: profile, ProfileDirectory: directory,
		InstallMode: "native"}
}

// ResolveTarget binds a selection to fresh adapter discovery. An explicit ID
// must still belong to the selected adapter and profile.
func ResolveTarget(targets []BrowserTarget, browser, profile, id string) (BrowserTarget, error) {
	var matches []BrowserTarget
	for _, target := range targets {
		if id != "" && target.ID != id {
			continue
		}
		if target.Browser != browser {
			if id != "" {
				return BrowserTarget{}, fmt.Errorf("target %q belongs to browser %q, selected browser is %q", id, target.Browser, browser)
			}
			continue
		}
		selected := profile != "" && (profile == target.Profile || profile == target.ProfileDirectory || profile == target.Name)
		if profile != "" && !selected {
			if id != "" {
				return BrowserTarget{}, fmt.Errorf("target %q does not match selected profile %q", id, profile)
			}
			continue
		}
		if id != "" {
			return target, nil
		}
		if selected {
			matches = append(matches, target)
		}
	}
	if id != "" {
		return BrowserTarget{}, fmt.Errorf("browser target %q is no longer available; discover targets again", id)
	}
	if len(matches) == 0 {
		return BrowserTarget{}, fmt.Errorf("no %s browser target matches profile %q; run extension targets and select a targetId", browser, profile)
	}
	if len(matches) != 1 {
		return BrowserTarget{}, fmt.Errorf("profile %q matches multiple %s targets; run extension targets and select a targetId", profile, browser)
	}
	return matches[0], nil
}

// ExecutableLocations contains native executable names declared by an adapter.
// Darwin and Windows paths are relative to their standard application roots;
// Linux values are command names looked up on PATH.
type ExecutableLocations struct {
	Darwin  []string
	Linux   []string
	Windows []string
}

// FindExecutables checks declared locations without launching a process.
func FindExecutables(locations ExecutableLocations) ([]string, error) {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications"), "/System/Applications"} {
			for _, suffix := range locations.Darwin {
				candidates = append(candidates, filepath.Join(root, suffix))
			}
		}
	case "linux":
		for _, name := range locations.Linux {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}
	case "windows":
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			for _, suffix := range locations.Windows {
				candidates = append(candidates, filepath.Join(root, filepath.FromSlash(suffix)))
			}
		}
	}
	seen := map[string]bool{}
	found := make([]string, 0)
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) || seen[absolute] {
			continue
		}
		seen[absolute] = true
		found = append(found, absolute)
	}
	return found, nil
}
