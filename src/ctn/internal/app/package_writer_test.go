package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ml "github.com/webong/ext/res/int/model"
	"github.com/webong/ext/res/system/service/host/dirlock"
	inferencebridge "github.com/webong/ext/src/ctn/inference"
	perceptionbridge "github.com/webong/ext/src/ctn/perception"
)

func TestProviderMutationsCoordinateWithPackageWriter(t *testing.T) {
	for _, override := range []string{"", "EXT_INT_PACKAGE_ROOT", "EXT_INFERENCE_PACKAGE_ROOT"} {
		name := override
		if name == "" {
			name = "shared-store"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("EXT_HOME", home)
			t.Setenv("EXT_INT_PACKAGE_ROOT", "")
			t.Setenv("EXT_INFERENCE_PACKAGE_ROOT", "")
			if override != "" {
				t.Setenv(override, filepath.Join(t.TempDir(), "packages"))
			}
			path := filepath.Join(home, ".package-write.lock")
			release, err := dirlock.Acquire(path)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			commands := []struct {
				name string
				run  func([]string, io.Writer, io.Writer) int
			}{
				{"inference", inferenceCommand}, {"perception", perceptionCommand}, {"model", modelProviderCommand},
			}
			for _, command := range commands {
				for _, action := range []string{"install", "trust", "untrust"} {
					t.Run(command.name+"-"+action, func(t *testing.T) {
						var output, diagnostics bytes.Buffer
						// Deliberately absent source/ID: contention must win before any store
						// access, and even untrust must not create or replace the shared ledger.
						code := command.run([]string{action, "missing"}, &output, &diagnostics)
						if code != 1 || !strings.Contains(diagnostics.String(), "writer unavailable") {
							t.Fatalf("exit %d: %s", code, diagnostics.String())
						}
						if output.Len() != 0 {
							t.Fatalf("mutation reported success: %s", output.String())
						}
						if _, err := os.Stat(filepath.Join(home, "packages")); !os.IsNotExist(err) {
							t.Fatalf("contended mutation touched store: %v", err)
						}
					})
				}
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			for _, command := range commands {
				var output, diagnostics bytes.Buffer
				if code := command.run([]string{"install", "missing"}, &output, &diagnostics); code != 1 {
					t.Fatalf("invalid install returned %d", code)
				}
				// The failed mutation must relinquish its ownership, allowing both a
				// competing writer and the next command to proceed.
				unlock, err := dirlock.Acquire(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := unlock(); err != nil {
					t.Fatal(err)
				}
				output.Reset()
				diagnostics.Reset()
				if code := command.run([]string{"untrust", "missing"}, &output, &diagnostics); code != 0 {
					t.Fatalf("untrust after failure: %d %s", code, diagnostics.String())
				}
			}
		})
	}
}

func TestProviderSessionsCoordinateWithPackageWriter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXT_HOME", home)
	calls := map[string]func(context.Context) error{
		"inference": func(ctx context.Context) error {
			return withProviderClient(ctx, "missing", func(*inferencebridge.Client, *inferencebridge.Session) error {
				t.Error("unexpected client")
				return nil
			})
		},
		"perception": func(ctx context.Context) error {
			return withPerceptionClient(ctx, "missing", func(*perceptionbridge.Client) error { t.Error("unexpected client"); return nil })
		},
		"model": func(ctx context.Context) error {
			_, err := runTensorProvider(ctx, "missing", "", ml.Reference{}, nil)
			return err
		},
	}
	path := filepath.Join(home, ".package-write.lock")
	release, err := dirlock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(context.Background()); err == nil || !strings.Contains(err.Error(), "writer unavailable") {
				t.Fatalf("contention: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled admission: %v", err)
			}
		})
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	for name, call := range calls {
		t.Run(name+"-failed-admission", func(t *testing.T) {
			if err := call(context.Background()); err == nil {
				t.Fatal("missing package accepted")
			}
			unlock, err := dirlock.Acquire(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
