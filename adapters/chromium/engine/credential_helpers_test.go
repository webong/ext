package chromium

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func cookieCredentialHelper(t *testing.T, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock credential helper uses a POSIX shell")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root
}

func TestChromiumKWalletNetworkDiscoveryAndOverride(t *testing.T) {
	root := cookieCredentialHelper(t, "dbus-send", `printf '%s\n' "$*" >> "$EXT_TEST_CREDENTIAL_CALLS"
case "$*" in
  *org.kde.kwalletd6*) printf '%s\n' 'Work Wallet';;
  *) exit 1;;
esac
`)
	calls := filepath.Join(root, "calls")
	t.Setenv("EXT_TEST_CREDENTIAL_CALLS", calls)
	t.Setenv("EXT_KWALLET_NAME", "")
	t.Setenv("KDE_SESSION_VERSION", "6")
	cookieCredentialHelper(t, "kwallet-query", `printf '%s\n' "$*" >> "$EXT_TEST_CREDENTIAL_CALLS"
printf '%s\n' fixture-password
`)
	config := Config{WalletFolder: "Fixture Keys", WalletKey: "Fixture Safe Storage"}
	if secret, err := chromiumKWalletSecret(config); err != nil || secret != "fixture-password" {
		t.Fatalf("discovered wallet failed: %v", err)
	}
	data, err := os.ReadFile(calls)
	if err != nil || !strings.Contains(string(data), "org.kde.KWallet.networkWallet") || !strings.Contains(string(data), "Work Wallet") {
		t.Fatal("network wallet not used")
	}
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_KWALLET_NAME", "Explicit Wallet")
	if _, err := chromiumKWalletSecret(config); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(calls)
	if err != nil || strings.Contains(string(data), "dbus-send") || strings.Contains(string(data), "networkWallet") || !strings.Contains(string(data), "Explicit Wallet") {
		t.Fatal("explicit wallet was not used directly")
	}
	t.Setenv("EXT_KWALLET_NAME", "-invalid")
	if _, err := chromiumKWalletSecret(config); err == nil {
		t.Fatal("option-like wallet accepted")
	}
}

func TestChromiumKWalletDiscoveryFallbackAndFalseSuccess(t *testing.T) {
	cookieCredentialHelper(t, "dbus-send", "exit 1\n")
	t.Setenv("EXT_KWALLET_NAME", "")
	if wallet := chromiumNetworkWallet(); wallet != "kdewallet" {
		t.Fatal("default wallet fallback failed")
	}
	cookieCredentialHelper(t, "kwallet-query", "printf '%s\\n' 'Failed to read entry'\n")
	if _, err := chromiumKWalletSecret(Config{}); err == nil {
		t.Fatal("helper error output became a password")
	}
}

func TestChromiumSecretServiceIdentityFallback(t *testing.T) {
	root := cookieCredentialHelper(t, "ctx", `case "$3" in
  'secret_service:application=fixture') exit 1;;
  'secret_service:service=Fixture+Safe+Storage&account=Fixture') printf '%s\n' fixture-password;;
  *) exit 2;;
esac
`)
	t.Setenv("EXT_EXECUTABLE", filepath.Join(root, "ctx"))
	t.Setenv("EXT_DEPENDENCY_CREDENTIAL", "secret_service")
	config := Config{SecretApplication: "fixture", KeychainService: "Fixture Safe Storage", KeychainAccount: "Fixture"}
	if secret, err := chromiumSecretServiceSecret(config); err != nil || secret != "fixture-password" {
		t.Fatalf("service/account fallback failed: %v", err)
	}
}

func fixtureChromiumCBC(t *testing.T, password string, iterations int, version string) []byte {
	t.Helper()
	plaintext := []byte("synthetic-cookie-value")
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	plaintext = append(plaintext, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher(chromiumPBKDF2Key([]byte(password), iterations))
	if err != nil {
		t.Fatal(err)
	}
	encrypted := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte(" "), aes.BlockSize)).CryptBlocks(encrypted, plaintext)
	return append([]byte(version), encrypted...)
}

func TestChromiumCredentialCacheIsRequestScoped(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Safe Storage uses macOS/Linux")
	}
	path := filepath.Join(t.TempDir(), "password")
	write := func() {
		t.Helper()
		if err := os.WriteFile(path, []byte("fixture-key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	t.Setenv("EXT_BROWSER_FIXTURE_SAFE_STORAGE_PASSWORD_FILE", path)
	config := Config{Name: "fixture"}
	iterations, version := 1003, "v10"
	if runtime.GOOS == "linux" {
		iterations, version = 1, "v11"
	}
	encrypted := fixtureChromiumCBC(t, "fixture-key", iterations, version)
	decryptor := newChromiumCookieDecryptor(config)
	if value, err := decryptor.decrypt("unused", encrypted); err != nil || value != "synthetic-cookie-value" {
		t.Fatal("initial lookup failed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if value, err := decryptor.decrypt("unused", encrypted); err != nil || value != "synthetic-cookie-value" {
		t.Fatal("credential was read twice")
	}
	denied := newChromiumCookieDecryptor(config)
	if _, err := denied.decrypt("unused", encrypted); err == nil {
		t.Fatal("credential crossed requests")
	}
	write()
	if _, err := denied.decrypt("unused", encrypted); err == nil {
		t.Fatal("denied credential was retried in one request")
	}
	if _, err := newChromiumCookieDecryptor(config).decrypt("unused", encrypted); err != nil {
		t.Fatal("fresh request did not retry credentials")
	}
}

func TestChromiumMalformedCiphertextDoesNotAccessCredentials(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Safe Storage uses macOS/Linux")
	}
	path := filepath.Join(t.TempDir(), "password")
	t.Setenv("EXT_BROWSER_FIXTURE_SAFE_STORAGE_PASSWORD_FILE", path)
	decryptor := newChromiumCookieDecryptor(Config{Name: "fixture"})
	version, iterations := "v10", 1003
	if runtime.GOOS == "linux" {
		version, iterations = "v11", 1
	}
	if _, err := decryptor.decrypt("unused", []byte(version+"malformed")); err == nil {
		t.Fatal("malformed ciphertext accepted")
	}
	if len(decryptor.keys) != 0 {
		t.Fatal("malformed ciphertext accessed credentials")
	}
	if err := os.WriteFile(path, []byte("fixture-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := decryptor.decrypt("unused", fixtureChromiumCBC(t, "fixture-key", iterations, version)); err != nil {
		t.Fatal("valid ciphertext was blocked by malformed data")
	}
	if _, err := decryptChromiumWindowsCookie("unused", []byte("v10short")); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Fatal("malformed Windows ciphertext accessed credentials")
	}
}
