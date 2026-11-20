package sandboxcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/res/sandbox"
	"github.com/webong/ext/res/system/service/confine"
)

func runCommand(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run("ext", args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"nope"},
		{"exec"},
		{"exec", "--grant", "g.json"},
		{"exec", "--launch-fd", "3", "--grant", "g.json"},
		{"exec", "--launch-fd", "3", "--", "/bin/true"},
	} {
		if code, _, _ := runCommand(args...); code != ExitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, ExitUsage)
		}
	}
	if code, out, _ := runCommand("help"); code != 0 || !strings.Contains(out, "sandbox exec") {
		t.Errorf("help: %d %q", code, out)
	}
}

func TestGrantFileMustBeAPolicy(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"not json": "{", "unknown field": `{"everything": true}`, "two values": `{} {}`} {
		path := filepath.Join(dir, "grant.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := runCommand("exec", "--grant", path, "--", "/bin/true"); code != ExitUsage {
			t.Errorf("%s: exit %d", name, code)
		}
	}
}

func TestRefusedLaunchExits125(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grant.json")
	// The guest's executable is not in the grant, so this is refused on every platform: with
	// denied where a driver exists, with unsupported where none does.
	if err := os.WriteFile(path, []byte(`{"network":{"mode":"none"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCommand("exec", "--grant", path, "--accept", "lifecycle.fresh", "--", "/bin/true")
	if code != ExitRefused || !strings.Contains(stderr, "sandbox exec:") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestCheckListsTheDrivers(t *testing.T) {
	code, out, _ := runCommand("check", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Drivers []sandbox.Capabilities `json:"drivers"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Drivers) != len(confine.Drivers()) {
		t.Errorf("%d drivers, want %d", len(got.Drivers), len(confine.Drivers()))
	}
	for _, c := range got.Drivers {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", c.Driver, err)
		}
	}
	if code, out, _ := runCommand("check"); code != 0 || out == "" {
		t.Errorf("check: %d %q", code, out)
	}
}
