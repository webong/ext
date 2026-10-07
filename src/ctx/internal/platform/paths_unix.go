//go:build !windows

package platform

import (
	"os"
	"path/filepath"
)

func DefaultConfigHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ctx"
	}
	return filepath.Join(home, ".config", "ctx")
}

func DefaultShell() string {
	if shell := os.Getenv("CTX_SHELL"); shell != "" {
		return shell
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}
