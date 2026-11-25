package nativeplugin

import (
	"context"
	"errors"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestGraphSnapshotCopyFailureCleansPrivateStore(t *testing.T) {
	if os.Getenv("EXT_PLUGIN_LIBRARY") == "" {
		t.Skip("requires the canonical C engine")
	}
	lib, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	installed, scratch := t.TempDir(), t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, scratch)
	}
	s, err := lib.Open("store", map[string]any{"root": installed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ref := plugin.ContractRef{Name: "example.graph", Version: "v1"}
	contract := plugin.Contract{ContractRef: ref, Operations: []plugin.Operation{{Name: "read"}}}
	for _, id := range []string{"leaf", "root"} {
		source := t.TempDir()
		executable := filepath.Join(source, "guest")
		// No process should start in this case. These bytes are a valid artifact,
		// deliberately not a runnable guest.
		if err := os.WriteFile(executable, []byte("not launched"), 0700); err != nil {
			t.Fatal(err)
		}
		manifest, err := packagekit.ExecutableManifest(id, "1.0.0", []plugin.Contract{contract}, executable)
		if err != nil {
			t.Fatal(err)
		}
		if id == "root" {
			manifest.Descriptor.Reverse = []plugin.Contract{contract}
			manifest.Requires = []packagekit.Dependency{{Contract: ref, Operation: "read", Provider: "leaf"}}
		}
		if err := store.WriteManifest(source, manifest); err != nil {
			t.Fatal(err)
		}
		if err := s.Call("install", map[string]any{"source": source}, nil); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := OpenGraph(context.Background(), lib, s, GraphOptions{
		RootID: "root", Contract: ref, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Review: func(m packagekit.Manifest) (string, error) {
			var digest string
			err := lib.Library().Call("store_digest", map[string]any{"root": installed, "id": m.Name()}, &digest)
			if err == nil && m.Name() == "leaf" {
				// Root was reviewed and its entrypoint checked first. Model an
				// uncoordinated writer changing it before the graph copy finishes.
				err = os.WriteFile(filepath.Join(installed, "root", "guest"), []byte("changed"), 0700)
			}
			return digest, err
		},
	})
	if graph != nil {
		graph.Close()
		t.Fatal("changed graph was admitted")
	}
	var code *library.CodeError
	if !errors.As(err, &code) || (code.Name != "mismatch" && code.Name != "denied") {
		t.Fatalf("expected root copy rejection after leaf snapshot: %v", err)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if matched, _ := filepath.Match("ext-graph-snapshot-*", entry.Name()); matched {
			t.Fatalf("failed graph left private store %s", entry.Name())
		}
	}
}

func TestCGraphCloseDuringStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	if os.Getenv("EXT_PLUGIN_LIBRARY") == "" {
		t.Skip("requires canonical C engine")
	}
	lib, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	source, installed, scratch := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", scratch)
	marker := filepath.Join(t.TempDir(), "pid")
	executable := filepath.Join(source, "guest")
	// This guest deliberately never completes the plugin handshake.
	script := "#!/bin/sh\nprintf '%s' \"$$\" > '" + strings.ReplaceAll(marker, "'", "'\\''") + "'\nexec sleep 30\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ref := plugin.ContractRef{Name: "example.graph", Version: "v1"}
	manifest, err := packagekit.ExecutableManifest("slow", "1.0.0", []plugin.Contract{{ContractRef: ref, Operations: []plugin.Operation{{Name: "read"}}}}, executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteManifest(source, manifest); err != nil {
		t.Fatal(err)
	}
	s, err := lib.Open("store", map[string]any{"root": installed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Call("install", map[string]any{"source": source}, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		graph, err := OpenGraph(ctx, lib, s, GraphOptions{RootID: "slow", Contract: ref, OS: runtime.GOOS, Arch: runtime.GOARCH, Review: func(m packagekit.Manifest) (string, error) {
			var digest string
			err := lib.Library().Call("store_digest", map[string]any{"root": installed, "id": m.Name()}, &digest)
			return digest, err
		}})
		if graph != nil {
			graph.Close()
		}
		done <- err
	}()
	finished := false
	defer func() {
		cancel()
		if !finished {
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("startup did not stop")
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			finished = true
			t.Fatalf("startup returned before guest: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("guest did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("C graph did not join startup")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("startup left private copies: %v", entries)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Release()
	err = process.Signal(syscall.Signal(0))
	if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("startup guest survived: %v", err)
	}
}
