//go:build darwin || linux || windows

package systemgraph

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const processFixtureSentinel = "ctx-fixture-private-content-742619"

type processFixtureReady struct {
	PID                           int
	File, Mapping, TCP, UDP, Pipe string
}

// This subprocess owns all inspected resources. No installed application or
// personal profile is a test fixture. Control uses stdin/stdout acknowledgements.
func TestProcessFixtureHelper(t *testing.T) {
	if os.Getenv("CTX_GRAPH_PROCESS_FIXTURE") != "1" {
		return
	}
	if err := runProcessFixture(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
func runProcessFixture() error {
	root := os.Getenv("CTX_GRAPH_PROCESS_FIXTURE_DIR")
	file, err := os.OpenFile(filepath.Join(root, "open file.txt"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.WriteString(processFixtureSentinel); err != nil {
		return err
	}
	mapped, err := os.OpenFile(filepath.Join(root, "mapped file.bin"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer mapped.Close()
	if err = mapped.Truncate(4096); err != nil {
		return err
	}
	unmap, err := mapProcessFixture(mapped)
	if err != nil {
		return err
	}
	defer func() {
		if unmap != nil {
			unmap()
		}
	}()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer udp.Close()
	pipeRead, pipeWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pipeRead.Close()
	defer pipeWrite.Close()
	if os.Getenv("CTX_GRAPH_PROCESS_FIXTURE_DENY") == "1" {
		if err := restrictProcessFixture(); err != nil {
			return err
		}
	}
	out := json.NewEncoder(os.Stdout)
	descriptorBase := 10
	if runtime.GOOS == "windows" {
		descriptorBase = 16
	}
	ready := processFixtureReady{Pipe: strconv.FormatUint(uint64(pipeRead.Fd()), descriptorBase), PID: os.Getpid(), File: file.Name(), Mapping: mapped.Name(), TCP: listener.Addr().String(), UDP: udp.LocalAddr().String()}
	if err = out.Encode(ready); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "close":
			if err = errors.Join(unmap(), mapped.Close(), file.Close(), listener.Close(), udp.Close(), pipeRead.Close(), pipeWrite.Close()); err != nil {
				return err
			}
			unmap = nil
			if err = out.Encode("closed"); err != nil {
				return err
			}
		case "exit":
			return nil
		default:
			return errors.New("unexpected fixture command")
		}
	}
	return scanner.Err()
}

type processFixture struct {
	cmd     *exec.Cmd
	input   io.WriteCloser
	decoder *json.Decoder
	ready   processFixtureReady
	done    chan error
}

func startProcessFixture(t *testing.T, extraEnv ...string) *processFixture {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return startProcessFixtureAt(t, executable, extraEnv...)
}
func startProcessFixtureAt(t *testing.T, executable string, extraEnv ...string) *processFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProcessFixtureHelper$", "--", processFixtureSentinel)
	cmd.Env = append(os.Environ(), "CTX_GRAPH_PROCESS_FIXTURE=1", "CTX_GRAPH_PROCESS_FIXTURE_DIR="+t.TempDir(), "CTX_GRAPH_PROCESS_PRIVATE="+processFixtureSentinel)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f := &processFixture{cmd: cmd, input: input, decoder: json.NewDecoder(stdout), done: make(chan error, 1)}
	go func() { f.done <- cmd.Wait() }()
	t.Cleanup(func() {
		input.Close()
		select {
		case <-f.done:
		default:
			cmd.Process.Kill()
			<-f.done
		}
	})
	if err = f.decoder.Decode(&f.ready); err != nil {
		cmd.Process.Kill()
		waitErr := <-f.done
		f.done <- waitErr
		t.Fatalf("fixture startup: %v; exit=%v; stderr=%s", err, waitErr, stderr.String())
	}
	return f
}
func processFixturePathEqual(observed, expected string) bool {
	// Windows PSS reports NT-device paths; the unique temp directory and filename
	// still identify our fixture without depending on a drive-letter alias.
	observed = strings.ReplaceAll(observed, "\\", "/")
	expected = strings.ReplaceAll(expected, "\\", "/")
	if runtime.GOOS == "windows" {
		return strings.HasSuffix(strings.ToLower(observed), strings.ToLower(expected[2:]))
	}
	a, e1 := filepath.EvalSymlinks(observed)
	b, e2 := filepath.EvalSymlinks(expected)
	return e1 == nil && e2 == nil && a == b
}
func requireProcessFixtureResource(t *testing.T, p ProcessInfo, kind, value string) {
	t.Helper()
	for _, r := range p.Resources {
		if r.Kind != kind {
			continue
		}
		if kind == "socket" && r.LocalAddress == value {
			return
		}
		if kind != "socket" && processFixturePathEqual(r.Path, value) {
			return
		}
	}
	t.Fatalf("missing fixture %s %q; coverage=%+v", kind, value, p.Coverage[kind])
}
func TestNativeProcessLifecycle(t *testing.T) {
	if os.Getenv("CTX_GRAPH_PROCESS_NATIVE_TESTS") != "1" {
		t.Skip("set CTX_GRAPH_PROCESS_NATIVE_TESTS=1 to run isolated native process inspection")
	}
	f := startProcessFixture(t)
	ctx := context.Background()
	options := ProcessOptions{Timeout: 20 * time.Second, MaxProcesses: 65536, MaxResources: 4096}
	inspection, err := InspectProcess(ctx, f.ready.PID, options)
	if err != nil {
		t.Fatal(err)
	}
	p := inspection.Processes[0]
	if p.PID != f.ready.PID || p.ParentPID != os.Getpid() || p.StartID == "" || p.Executable == "" || p.Owner == "" {
		t.Fatalf("incomplete identity: %+v", p)
	}
	requireProcessFixtureResource(t, p, "file", f.ready.File)
	requireProcessFixtureResource(t, p, "mapping", f.ready.Mapping)
	requireProcessFixtureResource(t, p, "socket", f.ready.TCP)
	requireProcessFixtureResource(t, p, "socket", f.ready.UDP)
	for _, r := range p.Resources {
		if r.Kind == "socket" && r.LocalAddress == f.ready.TCP && r.State != "LISTEN" {
			t.Fatalf("listener state=%q", r.State)
		}
	}
	if p.Usage == nil || p.Usage.ResidentBytes == nil || *p.Usage.ResidentBytes == 0 || p.Usage.CPUSeconds == nil {
		t.Fatalf("missing usage: %+v (%+v)", p.Usage, p.Coverage["usage"])
	}
	foundIPC := false
	for _, r := range p.Resources {
		if r.Kind == "ipc" && r.Descriptor == f.ready.Pipe {
			foundIPC = true
		}
	}
	if !foundIPC {
		t.Fatalf("missing IPC evidence: %+v", p.Coverage["ipc"])
	}
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(processFixtureSentinel)) {
		t.Fatal("file contents, arguments, or environment leaked into process metadata")
	}
	all, err := DiscoverProcesses(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range all.Processes {
		if entry.PID == p.PID {
			found = true
			if entry.StartID != p.StartID {
				t.Fatal("identity differs between inventory and inspection")
			}
		}
	}
	if !found {
		t.Fatal("fixture absent from process inventory")
	}
	g := processTestGraph(t)
	if err = g.ObserveProcesses(ctx, all); err != nil {
		t.Fatal(err)
	}
	// Refresh inspection after enumeration to exercise monotonic reconciliation.
	inspection, err = InspectProcess(ctx, f.ready.PID, options)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.ObserveProcesses(ctx, inspection); err != nil {
		t.Fatal(err)
	}
	limited, err := InspectProcess(ctx, f.ready.PID, ProcessOptions{Timeout: 20 * time.Second, MaxResources: 1})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, r := range limited.Processes[0].Resources {
		counts[r.Kind]++
		if counts[r.Kind] > 1 {
			t.Fatalf("resource limit exceeded: %s", r.Kind)
		}
	}
	if limited.Processes[0].Coverage["file"].State != "partial" {
		t.Fatal("resource truncation not reported")
	}
	small, err := DiscoverProcesses(ctx, ProcessOptions{MaxProcesses: 1, Timeout: 20 * time.Second})
	if err != nil || len(small.Processes) != 1 || small.Enumeration.State != "partial" {
		t.Fatalf("enumeration limit: %+v %v", small, err)
	}
	if _, err = fmt.Fprintln(f.input, "close"); err != nil {
		t.Fatal(err)
	}
	var ack string
	if err = f.decoder.Decode(&ack); err != nil || ack != "closed" {
		t.Fatalf("close acknowledgement: %q %v", ack, err)
	}
	closed, err := InspectProcess(ctx, f.ready.PID, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range closed.Processes[0].Resources {
		if processFixturePathEqual(r.Path, f.ready.File) || processFixturePathEqual(r.Path, f.ready.Mapping) || r.LocalAddress == f.ready.TCP || r.LocalAddress == f.ready.UDP {
			t.Fatalf("closed resource still present: %+v", r)
		}
	}
	if err = g.ObserveProcesses(ctx, closed); err != nil {
		t.Fatal(err)
	}
	for _, v := range processTestStoreSnapshot(t, g).Vertices {
		if v.Kind == Namespace+"/process-resource" {
			if path, _ := v.Attributes["path"].(string); processFixturePathEqual(path, f.ready.File) || processFixturePathEqual(path, f.ready.Mapping) {
				t.Fatal("closed file remained in graph")
			}
		}
	}
	if _, err = fmt.Fprintln(f.input, "exit"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-f.done:
		if err != nil {
			t.Fatal(err)
		}
		f.done <- err
	case <-time.After(10 * time.Second):
		t.Fatal("fixture did not exit")
	}
	if _, err = InspectProcess(ctx, f.ready.PID, options); err == nil {
		t.Fatal("inspection of exited process succeeded")
	}
	if _, err = g.ScanProcesses(ctx, options); err != nil {
		t.Fatal(err)
	}
	for _, v := range processTestStoreSnapshot(t, g).Vertices {
		if v.ID == processInstanceID(all.HostID, p) {
			t.Fatal("exited process remains in graph")
		}
	}
	t.Logf("validated %s/%s: process/parent, executable, owner, open file, mapping, TCP, UDP, IPC, usage, limits, closure, exit, and reconciliation", runtime.GOOS, runtime.GOARCH)
}
func TestNativeProcessCancellation(t *testing.T) {
	if os.Getenv("CTX_GRAPH_PROCESS_NATIVE_TESTS") != "1" {
		t.Skip("native process tests are opt-in")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DiscoverProcesses(ctx, ProcessOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery cancellation: %v", err)
	}
	if _, err := InspectProcess(ctx, os.Getpid(), ProcessOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("inspection cancellation: %v", err)
	}
	if _, err := DiscoverProcesses(context.Background(), ProcessOptions{Timeout: time.Nanosecond}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("discovery deadline: %v", err)
	}
}
