//go:build windows

package chromium

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func fixtureWindowsProtect(t *testing.T, data []byte) []byte {
	t.Helper()
	const script = `Add-Type -AssemblyName System.Security; $raw=[Convert]::FromBase64String($env:EXT_TEST_DPAPI_DATA); $protected=[Security.Cryptography.ProtectedData]::Protect($raw,$null,[Security.Cryptography.DataProtectionScope]::CurrentUser); [Console]::Out.Write([Convert]::ToBase64String($protected))`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "EXT_TEST_DPAPI_DATA="+base64.StdEncoding.EncodeToString(data))
	output, err := command.Output()
	if err != nil {
		t.Fatalf("protect synthetic Windows credential: %v", err)
	}
	protected, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(output)))
	if err != nil {
		t.Fatal("Windows fixture returned malformed data")
	}
	return protected
}

func TestNativeCookieCredentialsWindowsDPAPI(t *testing.T) {
	if os.Getenv("EXT_COOKIE_NATIVE_CREDENTIAL_TESTS") != "1" {
		t.Skip("set EXT_COOKIE_NATIVE_CREDENTIAL_TESTS=1 for isolated OS credential fixtures")
	}
	plaintext := []byte("ctx-synthetic-cookie-value")
	protected := fixtureWindowsProtect(t, plaintext)
	if value, err := decryptChromiumWindowsCookie("unused", protected); err != nil || value != string(plaintext) {
		t.Fatalf("legacy DPAPI round trip failed: %v", err)
	}
	if _, err := windowsDPAPIUnprotect([]byte("invalid-synthetic-data")); err == nil {
		t.Fatal("invalid DPAPI ciphertext accepted")
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	root := t.TempDir()
	statePath := filepath.Join(root, "Local State")
	state, err := json.Marshal(map[string]any{"os_crypt": map[string]string{
		"encrypted_key": base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), fixtureWindowsProtect(t, key)...)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x23}, gcm.NonceSize()) // one synthetic message
	ciphertext := append([]byte("v10"), nonce...)
	ciphertext = append(ciphertext, gcm.Seal(nil, nonce, plaintext, nil)...)
	database := filepath.Join(root, "Default", "Network", "Cookies")
	decryptor := newChromiumCookieDecryptor(Config{})
	if value, err := decryptor.decrypt(database, ciphertext); err != nil || value != string(plaintext) {
		t.Fatalf("DPAPI-backed AES-GCM round trip failed: %v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if value, err := decryptor.decrypt(database, ciphertext); err != nil || value != string(plaintext) {
		t.Fatal("Windows key was fetched twice in one request")
	}
	if _, err := newChromiumCookieDecryptor(Config{}).decrypt(database, ciphertext); err == nil {
		t.Fatal("Windows key crossed requests")
	}
	if _, err := decryptor.decrypt(database, []byte("v20synthetic")); err == nil {
		t.Fatal("App-Bound protection was bypassed")
	}
}
