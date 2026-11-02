// Package home resolves Ext's shared per-user package roots. Products choose
// which substore to use; this package only supplies the common location.
package home

import (
	"os"
	"path/filepath"
	"runtime"
)

// Config returns Ext's per-user state root. EXT_HOME overrides it. Without it, an
// installation made before ~/.config/ext existed keeps being used from its old
// directory.
func Config() string {
	if value := os.Getenv("EXT_HOME"); value != "" {
		return value
	}
	shared, legacy := candidates()
	if shared == "" {
		return ".ext"
	}
	if _, err := os.Stat(shared); err == nil {
		return shared
	}
	if legacy != "" {
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return shared
}

// Adapters is the ext adapter substore. EXT_ADAPTER_HOME overrides it.
func Adapters() string {
	if value := os.Getenv("EXT_ADAPTER_HOME"); value != "" {
		return value
	}
	return filepath.Join(Config(), "adapters")
}

// Intelligence is the shared CTN intelligence-provider package substore.
// The former inference override remains a migration fallback.
func Intelligence() string {
	if value := os.Getenv("EXT_INT_PACKAGE_ROOT"); value != "" {
		return value
	}
	if value := os.Getenv("EXT_INFERENCE_PACKAGE_ROOT"); value != "" {
		return value
	}
	return filepath.Join(Config(), "commands")
}

// Inference is retained for callers using the original CTN name.
func Inference() string { return Intelligence() }

func candidates() (shared, legacy string) {
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil {
			return filepath.Join(dir, "ext"), filepath.Join(dir, "ctx")
		}
	}
	if dir, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "windows" {
			return filepath.Join(dir, ".ext"), filepath.Join(dir, ".ctx")
		}
		return filepath.Join(dir, ".config", "ext"), filepath.Join(dir, ".config", "ctx")
	}
	return "", ""
}
