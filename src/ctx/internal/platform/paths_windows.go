//go:build windows

package platform

import (
	"os"
	"os/exec"
)

func DefaultShell() string {
	if shell := os.Getenv("CTX_SHELL"); shell != "" {
		return shell
	}
	for _, shell := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := exec.LookPath(shell); err == nil {
			return path
		}
	}
	if shell := os.Getenv("COMSPEC"); shell != "" {
		return shell
	}
	return "cmd.exe"
}
