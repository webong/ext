package adapter

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPowerShellAdapterLiteralArguments(t *testing.T) {
	shell := testPowerShell(t)
	path := filepath.Join(t.TempDir(), "adapter's script.ps1")
	script := `[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)
Write-Progress -Activity 'fixture initialization' -Status 'loading'
ConvertTo-Json -Compress -InputObject (@($Operation, $Selection) + @($Arguments))
`
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"configure", "client's profile", "--", "--namespace", "payments", "--option=value", "", "space and \"quote\"", "$(throw 'injected')", "`literal`", "line\nbreak", "unicode 🍪"}
	output, err := exec.Command(shell, powershellArguments(path, args)...).CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell invocation failed: %v %s", err, output)
	}
	var got []string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("parse adapter output: %v %s", err, output)
	}
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("literal arguments changed: got=%q want=%q", got, args)
	}
}

func TestPowerShellAdapterExitStatus(t *testing.T) {
	shell := testPowerShell(t)
	for _, fixture := range []struct {
		name   string
		script string
		code   int
	}{
		{"success", "exit 0", 0},
		{"failure", "exit 7", 7},
		{"exception", "throw 'fixture failure'", 1},
		{"terminating-error", "Write-Error 'fixture failure' -ErrorAction Stop", 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "adapter.ps1")
			if err := os.WriteFile(path, []byte(fixture.script), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(shell, powershellArguments(path, nil)...).CombinedOutput()
			if strings.Contains(string(output), "#< CLIXML") {
				t.Fatal("PowerShell serialized diagnostics instead of returning text")
			}
			code := 0
			if err != nil {
				var failure *exec.ExitError
				if !errors.As(err, &failure) {
					t.Fatal(err)
				}
				code = failure.ExitCode()
			}
			if code != fixture.code {
				t.Fatalf("exit=%d want=%d", code, fixture.code)
			}
		})
	}
}

func testPowerShell(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"powershell.exe", "pwsh"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("PowerShell unavailable")
	return ""
}
