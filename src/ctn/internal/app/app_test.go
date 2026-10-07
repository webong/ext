package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"no arguments print usage", nil, 0, "Usage:", ""},
		{"version", []string{"version"}, 0, "ctn " + Version, ""},
		{"unknown command", []string{"bogus"}, 2, "", `unknown command "bogus"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(test.args, &stdout, &stderr); code != test.code {
				t.Fatalf("exit code = %d, want %d", code, test.code)
			}
			if !strings.Contains(stdout.String(), test.stdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), test.stdout)
			}
			if !strings.Contains(stderr.String(), test.stderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), test.stderr)
			}
		})
	}
}
