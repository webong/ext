package chromium

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/res/web/discovery"
	"github.com/webong/ext/res/web/extension"
)

func exampleConfig() Config {
	return Config{
		Name: "example", MacUserData: "Example/Browser", WindowsUserData: `Example\Browser\User Data`, LinuxUserData: "example-browser",
		Extensions: ExtensionManagementConfig{
			ExtensionPage: "example://extensions/", DebuggingRequiresCustomProfile: true,
			LinuxPolicyPath:      "/etc/example/policies/managed/extensions.json",
			ManagedPolicyDrivers: map[string]string{"windows": "powershell-registry", "linux": "managed-json"},
			Store:                func() *StoreConfig { s := exampleStore(); return &s }(),
		},
	}
}

func TestExtensionCapabilityFollowsConfig(t *testing.T) {
	none := extensionCapability(Config{Name: "example"})
	if none.PersistentLocalInstall || none.SessionLoad || none.ExternalStoreRequest || none.ManagedPolicy || none.Reason == "" {
		t.Fatalf("adapter without extension routes reports one: %+v", none)
	}
	capability := extensionCapability(exampleConfig())
	if !capability.PersistentLocalInstall || capability.InstallDriver != "native-load-unpacked" || !capability.RequiresBrowserAction || !capability.SessionLoad || capability.SessionDriver != "cdp-pipe" {
		t.Fatalf("incorrect session adapter: %+v", capability)
	}
	if want := []string{"primary", "secondary"}; len(capability.SupportedStores) != 2 || capability.SupportedStores[0] != want[0] || capability.SupportedStores[1] != want[1] {
		t.Fatalf("stores must come from the adapter's declared URLs, got %v", capability.SupportedStores)
	}
	if !capability.ExternalStoreRequest || capability.ExternalStoreDriver == "" || !capability.UpdateManifest {
		t.Fatalf("store routes missing: %+v", capability)
	}
	if capability.ManagedPolicy != (runtime.GOOS == "windows" || runtime.GOOS == "linux") {
		t.Fatalf("managed policy follows the declared drivers for %s: %+v", runtime.GOOS, capability)
	}
	if capability.Reason == "" || len(capability.Requires) == 0 {
		t.Fatalf("restriction must be explained: %+v", capability)
	}
}

func TestCapabilityWithoutDebuggingPipeTransport(t *testing.T) {
	// A host without the pipe transport keeps store and policy routes but
	// reports no local install or session route, with the reason stated.
	for _, goos := range []string{"windows", "linux"} {
		capability := extensionCapabilityFor(goos, exampleConfig(), false)
		if capability.PersistentLocalInstall || capability.SessionLoad || capability.RequiresBrowserAction || capability.InstallDriver != "" || capability.SessionDriver != "" || len(capability.Requires) != 0 {
			t.Fatalf("%s reports a local route without the transport: %+v", goos, capability)
		}
		if !strings.Contains(capability.Reason, "debugging-pipe transport") || !strings.Contains(capability.Reason, goos) {
			t.Fatalf("%s reason does not explain the missing transport: %q", goos, capability.Reason)
		}
	}
	windows := extensionCapabilityFor("windows", exampleConfig(), false)
	if !windows.ExternalStoreRequest || windows.ExternalStoreDriver != "powershell-registry" || !windows.ManagedPolicy {
		t.Fatalf("store and policy routes must not depend on the pipe transport: %+v", windows)
	}
	// This engine implements the transport for Windows, so the real result
	// there offers the session routes.
	if !debuggingPipeSupported("windows") || !debuggingPipeSupported(runtime.GOOS) || debuggingPipeSupported("plan9") {
		t.Fatal("pipe transport support is declared incorrectly")
	}
	if capability := extensionCapabilityFor("windows", exampleConfig(), debuggingPipeSupported("windows")); !capability.PersistentLocalInstall || !capability.SessionLoad {
		t.Fatalf("Windows must offer session routes when the transport exists: %+v", capability)
	}
	if capability := extensionCapabilityFor("plan9", exampleConfig(), debuggingPipeSupported("plan9")); capability.SessionLoad {
		t.Fatalf("an unsupported system must not offer sessions: %+v", capability)
	}
	// Adapters that declare no extension page keep their own reason.
	if capability := extensionCapabilityFor("windows", Config{Name: "example"}, false); !strings.Contains(capability.Reason, "no native extension") {
		t.Fatalf("adapter reason lost: %q", capability.Reason)
	}
}

func TestDiscoverExtensionTargetsFromDeclaredLocations(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("discovery locations are resolved through PATH on Linux")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "example-browser"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, name := range []string{"Default", "Profile 2", "System Profile", "Crashpad"} {
		if err := os.MkdirAll(filepath.Join(home, ".config", "example-browser", name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := exampleConfig()
	config.Extensions.Executables = discovery.ExecutableLocations{Linux: []string{"example-browser"}}
	first, err := discoverExtensionTargets(config)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := discoverExtensionTargets(config)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("only Default and numbered profiles are selectable: %+v", first)
	}
	for i, target := range first {
		if target.ID == "" || target.ID != second[i].ID {
			t.Fatalf("unstable target ID: %+v", target)
		}
		if target.InstallMode != "manual-stage" {
			t.Fatalf("a browser that restricts debugging in its default directory must hand off manually: %+v", target)
		}
	}
	config.Extensions.ExtensionPage = ""
	unsupported, err := discoverExtensionTargets(config)
	if err != nil || len(unsupported) != 2 || unsupported[0].InstallMode != "unsupported" || unsupported[0].Reason == "" {
		t.Fatalf("adapters without an install route must say so: %+v, %v", unsupported, err)
	}
}

func TestManageExtensionReportsCapabilities(t *testing.T) {
	backend := extensionBackend{config: exampleConfig()}
	result, status, err := backend.ManageExtension(context.Background(), "Default", "capabilities", nil, nil)
	if err != nil || status != "ready" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	capability, ok := result.(extension.InstallCapability)
	if !ok || capability.Browser != "example" {
		t.Fatalf("unexpected capability result: %#v", result)
	}
	if _, _, err := backend.ManageExtension(context.Background(), "Default", "unknown", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("accepted an undeclared operation")
	}
	noStore := extensionBackend{config: Config{Name: "example"}}
	if _, _, err := noStore.ManageExtension(context.Background(), "Default", "store_install", json.RawMessage(`{"id":"`+exampleExtensionID+`"}`), nil); err == nil {
		t.Fatal("store request without a declared store")
	}
}

// Run with EXT_TEST_BROWSER_BIN set to a local Chromium-family executable. This
// checks pipe-only session loading in an isolated profile.
func TestRunWithDevToolsLive(t *testing.T) {
	executable := os.Getenv("EXT_TEST_BROWSER_BIN")
	if executable == "" {
		t.Skip("set EXT_TEST_BROWSER_BIN for a live browser installation test")
	}
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	ready := make(chan extension.InstallResult, 1)
	done := make(chan error, 1)
	go func() {
		done <- RunWithDevTools(ctx, DevToolsTarget{
			Browser: "example", ExecutablePath: executable,
			ProfilePath: filepath.Join(root, "profile"), Headless: true,
		}, source, filepath.Join(root, "staged"), prepared.Revision, func(result extension.InstallResult) error {
			ready <- result
			return nil
		})
	}()
	var result extension.InstallResult
	select {
	case result = <-ready:
	case err := <-done:
		t.Fatalf("browser session failed before loading extension: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if result.Status != "activated" || result.ID == "" || result.Extension == nil || result.Extension.Revision != prepared.Revision {
		t.Fatalf("unexpected browser session: %+v", result)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
