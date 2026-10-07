package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// dockerConfigOverlay gives one invocation its own plugin lookup directory while
// retaining the user's contexts, credentials, and other Docker configuration.
// Only plugin commands may use it: config.json is a private read snapshot, so
// ordinary commands such as docker login must keep the user's real config.
func dockerNeedsPluginOverlay(operation string, args []string) bool {
	return dockerInvokedPlugin(operation, args) != ""
}

func dockerInvokedPlugin(operation string, args []string) string {
	if operation == "build" {
		return "buildx"
	}
	if operation != "run" {
		return ""
	}
	for _, arg := range args {
		if arg == "--config" || strings.HasPrefix(arg, "--config=") {
			// An explicit Docker config takes precedence over DOCKER_CONFIG.
			return ""
		}
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--context", "--host", "-H", "--log-level":
			index++
			continue
		case "--":
			if index+1 < len(args) {
				return dockerPluginCommand(args[index+1])
			}
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return dockerPluginCommand(arg)
	}
	return ""
}

func dockerPluginCommand(name string) string {
	if name == "buildx" || name == "compose" {
		return name
	}
	return ""
}

func dockerPluginFilename(name string) string {
	filename := "docker-" + name
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	return filename
}

func dockerConfigOverlay(pluginDir string, plugins map[string]string) (string, func(), error) {
	base := os.Getenv("DOCKER_CONFIG")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, err
		}
		base = filepath.Join(home, ".docker")
	}
	overlay, err := os.MkdirTemp("", "ctx-docker-config-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(overlay) }
	if err := copyDockerConfigFile(filepath.Join(base, "config.json"), filepath.Join(overlay, "config.json")); err != nil {
		cleanup()
		return "", nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil && !os.IsNotExist(err) {
		cleanup()
		return "", nil, err
	}
	for _, entry := range entries {
		if entry.Name() == "config.json" || entry.Name() == "cli-plugins" {
			continue
		}
		if err := linkDockerEntry(filepath.Join(base, entry.Name()), filepath.Join(overlay, entry.Name())); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("link Docker config entry %s: %w", entry.Name(), err)
		}
	}
	pluginHome := filepath.Join(overlay, "cli-plugins")
	if err := os.Mkdir(pluginHome, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	chosen := map[string]string{}
	if pluginDir != "" {
		if err := collectDockerPlugins(pluginDir, chosen); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	for name, path := range plugins {
		chosen[dockerPluginFilename(name)] = path
	}
	// Preserve unrelated plugins without inheriting globally contested Buildx or
	// Compose links when this manager supplied its own version.
	global := map[string]string{}
	if err := collectDockerPlugins(filepath.Join(base, "cli-plugins"), global); err != nil && !os.IsNotExist(err) {
		cleanup()
		return "", nil, err
	}
	for name, path := range global {
		if pluginDir != "" && (name == dockerPluginFilename("buildx") || name == dockerPluginFilename("compose")) {
			continue
		}
		if chosen[name] == "" {
			chosen[name] = path
		}
	}
	for name, path := range chosen {
		if err := linkDockerEntry(path, filepath.Join(pluginHome, name)); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("link Docker plugin %s: %w", name, err)
		}
	}
	return overlay, cleanup, nil
}

// Windows can disallow symlinks. Hard links, then copies, preserve an isolated
// Docker config without requiring Developer Mode or administrator rights.
func linkDockerEntry(source, target string) error {
	if err := os.Symlink(source, target); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cannot mirror Docker config symlink %s without Windows symlink permission", source)
	}
	if info.IsDir() {
		if err := os.Mkdir(target, 0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := linkDockerEntry(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported Docker config entry %s", source)
	}
	if err := os.Link(source, target); err == nil {
		return nil
	}
	return copyDockerConfigFile(source, target)
}

func collectDockerPlugins(directory string, result map[string]string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "docker-") || entry.IsDir() {
			continue
		}
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			result[name] = path
		}
	}
	return nil
}

func copyDockerConfigFile(source, target string) error {
	input, err := os.Open(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
