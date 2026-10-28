package app

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestServiceCommandRendersPlatformDefinitions(t *testing.T) {
	cases := map[string][]string{
		"darwin": {
			"render", "--platform", "darwin", "--name", "com.example.agent",
			"--executable", "/usr/local/bin/agent", "--arg=--serve", "--env", "MODE=strict",
			"--workdir", "/var/lib/agent", "--restart", "on-failure",
		},
		"linux": {
			"render", "--platform=linux", "--name", "example-agent",
			"--executable", "/usr/local/bin/agent", "--arg", "serve",
		},
		"windows": {
			"render", "--platform", "windows", "--name", "Example Agent",
			"--executable", `C:\Program Files\agent.exe`, "--workdir", `C:\state`,
			"--user", `DOMAIN\user`, "--restart", "never",
		},
	}
	fragments := map[string]string{
		"darwin":  "<key>Label</key><string>com.example.agent</string>",
		"linux":   "ExecStart=\"/usr/local/bin/agent\" \"serve\"",
		"windows": "<Task version=\"1.3\"",
	}
	for platform, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := serviceCommand(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%s render = %d: %s", platform, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), fragments[platform]) {
			t.Errorf("%s render is missing %q:\n%s", platform, fragments[platform], stdout.String())
		}
	}
}

func TestServiceCommandRejectsMalformedInput(t *testing.T) {
	cases := map[string][]string{
		"noAction":        {},
		"unknownAction":   {"install"},
		"unknownFlag":     {"render", "--nope"},
		"missingName":     {"render", "--executable", "/bin/true"},
		"relativePath":    {"render", "--name", "agent", "--executable", "agent"},
		"jsonWithValue":   {"render", "--json=yes"},
		"badPlatform":     {"render", "--platform", "plan9", "--name", "agent", "--executable", "/bin/true"},
		"badRestart":      {"render", "--name", "agent", "--executable", "/bin/true", "--restart", "sometimes"},
		"badRestartValue": {"render", "--name", "agent", "--executable", "/bin/true", "--restart-seconds", "soon"},
		"badEnvironment":  {"render", "--name", "agent", "--executable", "/bin/true", "--env", "NOEQUALS"},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := serviceCommand(args, &stdout, &stderr); code == 0 {
			t.Errorf("%s was accepted: %s", name, stdout.String())
		}
	}
}

func TestServiceCommandLifecycleNeedsDirectory(t *testing.T) {
	executable := "/bin/true"
	if runtime.GOOS == "windows" {
		executable = `C:\Windows\System32\cmd.exe`
	}
	var stdout, stderr bytes.Buffer
	code := serviceCommand([]string{"status", "--name", "agent", "--executable", executable}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--dir") {
		t.Fatalf("status without directory = %d: %s", code, stderr.String())
	}
}

func TestServiceCommandRejectsCrossPlatformLifecycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := serviceCommand([]string{
		"status", "--platform", "plan9", "--name", "agent", "--executable", "/bin/true", "--dir", t.TempDir(),
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("cross-platform lifecycle was accepted: %s", stdout.String())
	}
}
