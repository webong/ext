package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

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
