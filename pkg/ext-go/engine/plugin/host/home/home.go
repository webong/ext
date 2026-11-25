// Package home resolves Ext's shared per-user package roots. Products choose
// which substore to use; this package only supplies the common location.
package home

import (
	"os"
	"path/filepath"
	"runtime"
)

// Config returns Ext's per-user state root. EXT_HOME overrides it.
func Config() string {
	if value := os.Getenv("EXT_HOME"); value != "" {
		return value
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil {
			return filepath.Join(dir, "ext")
		}
	}
	if dir, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "windows" {
			return filepath.Join(dir, ".ext")
		}
		return filepath.Join(dir, ".config", "ext")
	}
	return ".ext"
}

// Packages is the shared installed package root.
func Packages() string { return filepath.Join(Config(), "packages") }

// Adapters is the shared package root unless EXT_ADAPTER_HOME overrides it.
func Adapters() string {
	if value := os.Getenv("EXT_ADAPTER_HOME"); value != "" {
		return value
	}
	return Packages()
}

// Intelligence is the shared CTN intelligence-provider package substore.
func Intelligence() string {
	if value := os.Getenv("EXT_INT_PACKAGE_ROOT"); value != "" {
		return value
	}
	return Packages()
}
