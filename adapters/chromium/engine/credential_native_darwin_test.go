//go:build darwin

package chromium

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This opt-in test creates its own keychain and dummy item. It does not read
// browser credentials. Run it on a disposable CI host or explicitly opt in.
func TestNativeCookieCredentialsMacOSKeychain(t *testing.T) {
	if os.Getenv("CTX_COOKIE_NATIVE_CREDENTIAL_TESTS") != "1" {
		t.Skip("set CTX_COOKIE_NATIVE_CREDENTIAL_TESTS=1 for isolated OS credential fixtures")
	}
	keychain := filepath.Join(t.TempDir(), "ctx-fixture.keychain-db")
	run := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, "/usr/bin/security", args...).CombinedOutput(); err != nil {
			t.Fatalf("synthetic keychain operation %s failed: %v: %s", args[0], err, output)
		}
	}
	run("create-keychain", "-p", "ctx-synthetic-keychain-password", keychain)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "/usr/bin/security", "delete-keychain", keychain).Run(); err != nil {
			t.Errorf("remove synthetic keychain: %v", err)
		}
	})
	run("unlock-keychain", "-p", "ctx-synthetic-keychain-password", keychain)
	// The workspace root: this package is adapters/chromium/engine.
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	build := func(target, packagePath string) {
		t.Helper()
		command := exec.Command("go", "build", "-o", target, packagePath)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", packagePath, err, output)
		}
	}
	fixture := t.TempDir()
	ctxBinary := filepath.Join(fixture, "ctx")
	build(ctxBinary, "./src/ctx/cmd/ctx")
	// Stage the keychain adapter from source and let the real ctx CLI install
	// and trust it, mirroring the documented fixture flow.
	staging := filepath.Join(fixture, "keychain-src")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	build(filepath.Join(staging, "ctx-keychain"), "./adapters/keychain/native")
	manifestBytes, err := os.ReadFile(filepath.Join(root, "adapters", "keychain", "adapter.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "adapter.toml"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	adapterHome := filepath.Join(fixture, "state", "adapters")
	ctxCtl := func(args ...string) {
		t.Helper()
		command := exec.Command(ctxBinary, args...)
		command.Dir = root
		command.Env = append(os.Environ(),
			"CTX_HOME="+filepath.Join(fixture, "state"),
			"CTX_ADAPTER_HOME="+adapterHome,
			"CTX_EXECUTABLE="+ctxBinary,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("ctx %v: %v: %s", args, err, output)
		}
	}
	ctxCtl("adapter", "install", staging)
	ctxCtl("adapter", "trust", "keychain")
	// The manifest names the executable at the adapter's root; older layouts nested it
	// under native/. Use whichever exists, because `security -T` needs a real path.
	installed := filepath.Join(adapterHome, "keychain")
	executable := ""
	for _, candidate := range []string{filepath.Join(installed, "ctx-keychain"), filepath.Join(installed, "native", "ctx-keychain")} {
		if _, err := os.Stat(candidate); err == nil {
			executable = candidate
			break
		}
	}
	if executable == "" {
		t.Fatalf("installed keychain adapter has no ctx-keychain executable under %s", installed)
	}
	run("add-generic-password", "-s", "CTX Synthetic Safe Storage", "-a", "CTX Synthetic",
		"-w", "ctx-synthetic-safe-storage-password", "-T", "/usr/bin/security", "-T", executable, keychain)
	t.Setenv("CTX_HOME", filepath.Join(fixture, "state"))
	t.Setenv("CTX_ADAPTER_HOME", adapterHome)
	t.Setenv("CTX_EXECUTABLE", ctxBinary)
	t.Setenv("CTX_DEPENDENCY_CREDENTIAL", "keychain")
	config := Config{Name: "fixture", KeychainService: "CTX Synthetic Safe Storage", KeychainAccount: "CTX Synthetic", KeychainPath: keychain}
	t.Setenv("CTX_BROWSER_FIXTURE_SAFE_STORAGE_PASSWORD_FILE", "")
	password, err := chromiumMacKeychainPassword(config)
	if err != nil || password != "ctx-synthetic-safe-storage-password" {
		t.Fatalf("isolated Keychain lookup failed: %v", err)
	}
	encrypted := fixtureChromiumCBC(t, password, 1003, "v10")
	decryptor := newChromiumCookieDecryptor(config)
	for i := 0; i < 2; i++ {
		if value, err := decryptor.decrypt("unused", encrypted); err != nil || value != "synthetic-cookie-value" {
			t.Fatalf("Keychain-backed cookie decryption failed: %v", err)
		}
	}
}
