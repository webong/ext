package chromium

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/res/web/extension"
)

func TestInstallWithNativeUIVerifiesRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake browser launcher is a shell script")
	}
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	staged := filepath.Join(root, "staged")
	launchCount := filepath.Join(root, "launch-count")
	if err := os.WriteFile(launchCount, []byte("0"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "browser")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "'"
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec "+quoted+" -test.run=^TestNativeBrowserHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_FAKE_BROWSER", "1")
	t.Setenv("EXT_FAKE_BROWSER_COUNT", launchCount)
	t.Setenv("EXT_FAKE_BROWSER_EXTENSION", staged)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var statuses []string
	err = InstallWithNativeUI(ctx, DevToolsTarget{
		Browser: "example", ExecutablePath: launcher, ExtensionPage: "example://extensions/",
		ProfilePath: filepath.Join(root, "user-data"), ProfileDirectory: "Profile 2",
	}, source, staged, prepared.Revision, func(result extension.InstallResult) error {
		statuses = append(statuses, result.Status)
		if result.Status == "installed" {
			if result.ID != exampleExtensionID || result.Profile != filepath.Join(root, "user-data", "Profile 2") {
				t.Errorf("incorrect installed result: %+v", result)
			}
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || statuses[0] != "awaiting-browser-action" || statuses[1] != "installed" {
		t.Fatalf("incorrect install statuses: %v", statuses)
	}
	count, err := os.ReadFile(launchCount)
	if err != nil || string(count) != "2" {
		t.Fatalf("browser was not restarted: count=%s error=%v", count, err)
	}
}

// This process stands in for the browser's pipe protocol in the restart test.
func TestNativeBrowserHelper(t *testing.T) {
	if os.Getenv("EXT_FAKE_BROWSER") != "1" {
		t.Skip("helper process")
	}
	countPath := os.Getenv("EXT_FAKE_BROWSER_COUNT")
	count, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	launch := 0
	if _, err := fmt.Sscanf(string(count), "%d", &launch); err != nil {
		t.Fatal(err)
	}
	launch++
	if err := os.WriteFile(countPath, []byte(fmt.Sprint(launch)), 0600); err != nil {
		t.Fatal(err)
	}
	request := os.NewFile(3, "request")
	response := os.NewFile(4, "response")
	reader := bufio.NewReader(request)
	queries := 0
	for {
		message, err := reader.ReadBytes(0)
		if err != nil {
			return
		}
		var call struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(bytes.TrimSuffix(message, []byte{0}), &call); err != nil {
			t.Fatal(err)
		}
		if call.Method == "Browser.close" {
			return
		}
		queries++
		items := []nativeExtension{}
		if launch > 1 || queries > 1 {
			items = append(items, nativeExtension{ID: exampleExtensionID, Path: os.Getenv("EXT_FAKE_BROWSER_EXTENSION"), Enabled: true})
		}
		encoded, err := json.Marshal(map[string]any{"id": call.ID, "result": map[string]any{"extensions": items}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := response.Write(append(encoded, 0)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRestrictedProfileStagesForManualInstall(t *testing.T) {
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := DevToolsTarget{
		Browser: "example", ExecutablePath: filepath.Join(root, "unused"), ExtensionPage: "example://extensions/",
		ProfilePath: filepath.Join(root, "default-data"), RestrictedProfilePath: filepath.Join(root, "default-data"),
	}
	var results []extension.InstallResult
	staged := filepath.Join(root, "staged")
	err = InstallWithNativeUI(context.Background(), target, source, staged, prepared.Revision, func(result extension.InstallResult) error {
		results = append(results, result)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "awaiting-browser-action" || !strings.Contains(results[0].NextAction, "manual installation") {
		t.Fatalf("unexpected manual handoff: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(staged, "manifest.json")); err != nil {
		t.Fatalf("extension was not staged: %v", err)
	}
}

func TestNativeInstallRejectsUnsafeTargets(t *testing.T) {
	source := extensionFixture(t)
	prepared, _ := extension.Inspect(source)
	root := t.TempDir()
	report := func(extension.InstallResult) error { return nil }
	good := DevToolsTarget{Browser: "example", ExecutablePath: filepath.Join(root, "browser"), ExtensionPage: "example://extensions/", ProfilePath: filepath.Join(root, "data")}
	cases := map[string]func() error{
		"no extension page": func() error {
			target := good
			target.ExtensionPage = ""
			return InstallWithNativeUI(context.Background(), target, source, filepath.Join(root, "s1"), prepared.Revision, report)
		},
		"headless": func() error {
			target := good
			target.Headless = true
			return InstallWithNativeUI(context.Background(), target, source, filepath.Join(root, "s2"), prepared.Revision, report)
		},
		"missing revision": func() error {
			return InstallWithNativeUI(context.Background(), good, source, filepath.Join(root, "s3"), "", report)
		},
		"relative destination": func() error {
			return InstallWithNativeUI(context.Background(), good, source, "staged", prepared.Revision, report)
		},
		"profile directory path": func() error {
			target := good
			target.ProfileDirectory = "../escape"
			return InstallWithNativeUI(context.Background(), target, source, filepath.Join(root, "s4"), prepared.Revision, report)
		},
		"missing callback": func() error {
			return InstallWithNativeUI(context.Background(), good, source, filepath.Join(root, "s5"), prepared.Revision, nil)
		},
	}
	for name, run := range cases {
		if err := run(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestSameExtensionPathRejectsUnrelatedDirectory(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "staged")
	other := filepath.Join(root, "other")
	for _, dir := range []string{staged, other} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if !sameExtensionPath(staged, staged) || sameExtensionPath(other, staged) || sameExtensionPath("relative", staged) {
		t.Fatal("incorrect extension path match")
	}
}
