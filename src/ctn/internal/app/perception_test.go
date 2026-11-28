package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	domain "github.com/webong/ext/src/ctn/contract/perception"
	bridge "github.com/webong/ext/src/ctn/perception"
)

func TestPerceptionProviderPackageTrustAndCall(t *testing.T) {
	config := t.TempDir()
	t.Setenv("EXT_HOME", config)
	t.Setenv("EXT_INT_PACKAGE_ROOT", filepath.Join(config, "providers"))
	binary := filepath.Join(t.TempDir(), "perceptionguest")
	build := exec.Command("go", "build", "-o", binary, "./testdata/perceptionguest")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, output)
	}
	packageDir := filepath.Join(t.TempDir(), "package")
	var output, diagnostics bytes.Buffer
	command := func(args ...string) int {
		output.Reset()
		diagnostics.Reset()
		return perceptionCommand(args, &output, &diagnostics)
	}
	if code := command("package", "perception-fixture", "1.0.0", binary, packageDir); code != 0 {
		t.Fatalf("package = %d: %s", code, diagnostics.String())
	}
	if code := command("install", packageDir); code != 0 {
		t.Fatalf("install = %d: %s", code, diagnostics.String())
	}
	if code := command("providers"); code != 0 || !strings.Contains(output.String(), "untrusted") {
		t.Fatalf("untrusted providers = %d, %q, %q", code, output.String(), diagnostics.String())
	}
	request := filepath.Join(t.TempDir(), "interpret.json")
	if err := os.WriteFile(request, []byte(`{"requestId":"r1","parts":[{"kind":"text","text":"hello"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	call := []string{"interpret", "--provider", "perception-fixture", "--model", "model-1", "--input", request}
	if code := command(call...); code == 0 {
		t.Fatal("untrusted provider was invoked")
	}
	if code := command("trust", "perception-fixture"); code != 0 {
		t.Fatalf("trust = %d: %s", code, diagnostics.String())
	}
	if code := command(call...); code != 0 {
		t.Fatalf("interpret = %d: %s", code, diagnostics.String())
	}
	var response domain.InterpretResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || len(response.Findings) != 1 || response.Findings[0].Text != "hello" {
		t.Fatalf("interpretation = %+v, %v", response, err)
	}
	observeFile := filepath.Join(t.TempDir(), "observe.json")
	if err := os.WriteFile(observeFile, []byte(`{"requestId":"r2","mediaType":"image/png","image":"AQ=="}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := command("observe", "--provider", "perception-fixture", "--model", "model-1", "--input", observeFile); code != 0 {
		t.Fatalf("observe = %d: %s", code, diagnostics.String())
	}
	var observation domain.ObserveResponse
	if err := json.Unmarshal(output.Bytes(), &observation); err != nil || len(observation.Observations) != 1 || observation.Observations[0].Label != "fixture" {
		t.Fatalf("observation = %+v, %v", observation, err)
	}

	t.Run("cancel-owned-session", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "guest.pid")
		t.Setenv("EXT_TEST_PERCEPTION_PID", pidFile)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := withPerceptionClient(ctx, "perception-fixture", func(client *bridge.Client) error {
			if _, err := client.Info(ctx); err != nil {
				return err
			}
			if code := command("untrust", "perception-fixture"); code != 1 || !strings.Contains(diagnostics.String(), "writer unavailable") {
				t.Fatalf("live session allowed mutation: %d %s", code, diagnostics.String())
			}
			cancel()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
		if runtime.GOOS != "windows" {
			data, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil || pid <= 0 {
				t.Fatalf("invalid guest pid: %q", data)
			}
			process, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			err = process.Signal(syscall.Signal(0))
			_ = process.Release()
			if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("canceled guest still running: %d: %v", pid, err)
			}
		}

		if _, err := os.Stat(filepath.Join(config, ".package-write.lock")); !os.IsNotExist(err) {
			t.Fatalf("session retained writer: %v", err)
		}
	})
	if code := command("untrust", "perception-fixture"); code != 0 {
		t.Fatalf("untrust = %d: %s", code, diagnostics.String())
	}
	if code := command(call...); code == 0 {
		t.Fatal("revoked provider was invoked")
	}
}

func TestPerceptionRequestRejectsOversizedTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"parts":[{"kind":"text","text":"hello"}]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(strings.Repeat(" ", 16<<20)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var request domain.InterpretRequest
	if err := readPerceptionRequest(path, &request); err == nil {
		t.Fatal("oversized request accepted")
	}
}
