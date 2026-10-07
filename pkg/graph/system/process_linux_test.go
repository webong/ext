//go:build linux

package systemgraph

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessLinuxSocketAddressesAndPaths(t *testing.T) {
	for input, want := range map[string]string{"0100007F:1F90": "127.0.0.1:8080", "00000000000000000000000001000000:0050": "[::1]:80"} {
		got, err := linuxSocketAddress(input)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"bad", "1234:0", "GGGGGGGG:ZZ"} {
		if _, err := linuxSocketAddress(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	if got := processFieldsRemainder("000-fff r--p 000 00:00 12 /tmp/file  with spaces", 5); got != "/tmp/file  with spaces" {
		t.Fatal(got)
	}
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProcessFile(path, 4); err == nil {
		t.Fatal("unbounded proc read")
	}
}
func restrictProcessFixture() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }
func TestNativeProcessPermissionDenied(t *testing.T) {
	if os.Getenv("EXT_GRAPH_PROCESS_NATIVE_TESTS") != "1" {
		t.Skip("native process tests are opt-in")
	}
	if os.Geteuid() == 0 {
		t.Skip("requires a non-root runner without ptrace bypass privileges")
	}
	f := startProcessFixture(t, "EXT_GRAPH_PROCESS_FIXTURE_DENY=1")
	s, err := InspectProcess(context.Background(), f.ready.PID, ProcessOptions{Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"file", "mapping"} {
		if got := s.Processes[0].Coverage[kind].State; got != "permission-denied" {
			t.Fatalf("%s coverage=%s; need unprivileged runner", kind, got)
		}
	}
}
