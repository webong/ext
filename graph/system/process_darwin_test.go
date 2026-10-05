//go:build darwin

package systemgraph

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessDarwinResourceFields(t *testing.T) {
	p := newProcessInfo(123)
	data := "p123\x00\nf7\x00tREG\x00n/tmp/file with\nnewline\x00\nfmem\x00tREG\x00n/tmp/mapped file\x00\nf8\x00tIPv4\x00PTCP\x00n127.0.0.1:42->127.0.0.1:43\x00TST=ESTABLISHED\x00\nf9\x00tPIPE\x00nopaque-pipe\x00\n"
	parseDarwinProcessFiles(&p, []byte(data), 10)
	if len(p.Resources) != 4 {
		t.Fatalf("records: %+v", p.Resources)
	}
	if p.Resources[0].Path != "/tmp/file with\nnewline" || p.Resources[1].Kind != "mapping" {
		t.Fatalf("paths: %+v", p.Resources)
	}
	socket := p.Resources[2]
	if socket.Kind != "socket" || socket.LocalAddress != "127.0.0.1:42" || socket.RemoteAddress != "127.0.0.1:43" || socket.State != "ESTABLISHED" {
		t.Fatal(socket)
	}
	parseDarwinProcessFiles(&p, []byte("p123\x00\nfNOFD\x00naccess denied\x00"), 10)
	if p.Coverage["file"].State != "partial" {
		t.Fatal("NOFD was interpreted as successful empty inspection")
	}
}
func TestProcessDarwinOutputBound(t *testing.T) {
	output := &processOutput{limit: 8}
	// LimitReader hides strings.Reader.WriteTo so io.Copy can select ReaderFrom.
	n, err := io.Copy(output, io.LimitReader(strings.NewReader(strings.Repeat("a", 32)), 32))
	if err == nil || n > 8 || output.Len() > 8 {
		t.Fatalf("output limit bypassed: n=%d length=%d err=%v", n, output.Len(), err)
	}
}
func TestProcessDarwinCPUTime(t *testing.T) {
	for value, want := range map[string]float64{"00:01.25": 1.25, "02:03:04": 7384, "1-02:03:04": 93784} {
		got, err := parseProcessCPUTime(value)
		if err != nil || got != want {
			t.Fatalf("%s = %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"oops", "-1:03", "1:2:3:4"} {
		if _, err := parseProcessCPUTime(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
func restrictProcessFixture() error { return nil }

func TestNativeProcessApplicationBundle(t *testing.T) {
	if os.Getenv("CTX_GRAPH_PROCESS_NATIVE_TESTS") != "1" {
		t.Skip("native process tests are opt-in")
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "Graph Fixture.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "fixture")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	// Copy the signed executable bytes unchanged into a generic temporary bundle.
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	f := startProcessFixtureAt(t, executable)
	snapshot, err := InspectProcess(context.Background(), f.ready.PID, ProcessOptions{Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	app := snapshot.Processes[0].Application
	if app == nil || app.Name != "Graph Fixture" || !processFixturePathEqual(app.Path, bundle) {
		t.Fatalf("generic bundle attribution: %+v", app)
	}
}
