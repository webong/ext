//go:build !windows

package platform

import (
	"os"
)

func DefaultShell() string {
	if shell := os.Getenv("CTX_SHELL"); shell != "" {
		return shell
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}
