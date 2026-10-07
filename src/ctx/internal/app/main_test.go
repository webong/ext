package app

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps every test out of the developer's real configuration home.
// Commands under test record observations in the system graph, so a test that
// does not set CTX_HOME itself would otherwise write to ~/.config/ctx.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ctx-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test config home:", err)
		os.Exit(1)
	}
	// EXT_HOME and EXT_ADAPTER_HOME take precedence over the CTX names the tests
	// set, so a developer's own values must not reach them.
	os.Unsetenv("EXT_HOME")
	os.Unsetenv("EXT_ADAPTER_HOME")
	if err := os.Setenv("CTX_HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, "set CTX_HOME:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
