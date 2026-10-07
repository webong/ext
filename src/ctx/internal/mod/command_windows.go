//go:build windows

package mod

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func adapterCommand(path string, args []string) *exec.Cmd {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		shell := os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
		return exec.Command(shell, append([]string{"/d", "/s", "/c", path}, args...)...)
	case ".ps1":
		return exec.Command("powershell.exe", powershellArguments(path, args)...)
	default:
		return exec.Command(path, args...)
	}
}

func adapterCommandContext(ctx context.Context, path string, args []string) *exec.Cmd {
	if ctx == nil {
		return adapterCommand(path, args)
	}
	var command *exec.Cmd
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		shell := os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
		command = exec.CommandContext(ctx, shell, append([]string{"/d", "/s", "/c", path}, args...)...)
	case ".ps1":
		command = exec.CommandContext(ctx, "powershell.exe", powershellArguments(path, args)...)
	default:
		command = exec.CommandContext(ctx, path, args...)
	}
	command.Cancel = func() error {
		// PowerShell adapters invoke native helpers as child processes. Request
		// termination of that tree, then fall back to the ordinary parent kill.
		if root := os.Getenv("SystemRoot"); root != "" {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			killer := filepath.Join(root, "System32", "taskkill.exe")
			if err := exec.CommandContext(cleanup, killer, "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid)).Run(); err == nil {
				return nil
			}
		}
		return command.Process.Kill()
	}
	command.WaitDelay = 2 * time.Second
	return command
}
