package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ctx/res/browser"
)

func TestFirefoxCookieSharingDestinations(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	root := t.TempDir()
	adapterSource := filepath.Join(root, "firefox-adapter")
	if err := os.Mkdir(adapterSource, 0o700); err != nil {
		t.Fatal(err)
	}
	maintained := filepath.Join("..", "..", "..", "..", "adapters", "firefox")
	entries, err := os.ReadDir(maintained)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := os.ReadFile(filepath.Join(maintained, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(adapterSource, entry.Name()), contents, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(adapterSource, "ctx-firefox-share")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	command := exec.Command("go", "build", "-o", helper, "../../../../adapters/firefox/native")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build browser adapter helper: %v %s", err, output)
	}
	// Build with the caller's Go environment before isolating browser state.
	// Module downloads under a fake HOME are read-only and break TempDir cleanup.
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	t.Setenv("CTX_ADAPTER_HOME", filepath.Join(root, "state", "adapters"))
	t.Setenv("APPDATA", filepath.Join(root, "home", "AppData", "Roaming"))
	profilesRoot := filepath.Join(root, "home", ".mozilla", "firefox")
	if runtime.GOOS == "darwin" {
		profilesRoot = filepath.Join(root, "home", "Library", "Application Support", "Firefox")
	} else if runtime.GOOS == "windows" {
		profilesRoot = filepath.Join(root, "home", "AppData", "Roaming", "Mozilla", "Firefox")
	}
	for _, name := range []string{"source", "target"} {
		if err := os.MkdirAll(filepath.Join(profilesRoot, "Profiles", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ini := "[Profile0]\nName=source\nIsRelative=1\nPath=Profiles/source\n[Profile1]\nName=target\nIsRelative=1\nPath=Profiles/target\n"
	if err := os.WriteFile(filepath.Join(profilesRoot, "profiles.ini"), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}
	const schema = `CREATE TABLE moz_cookies (id INTEGER PRIMARY KEY, originAttributes TEXT NOT NULL DEFAULT '', name TEXT, value TEXT, host TEXT, path TEXT, expiry INTEGER, lastAccessed INTEGER, creationTime INTEGER, isSecure INTEGER, isHttpOnly INTEGER, sameSite INTEGER, UNIQUE(name,host,path,originAttributes));`
	sourceDB := filepath.Join(profilesRoot, "Profiles", "source", "cookies.sqlite")
	targetDB := filepath.Join(profilesRoot, "Profiles", "target", "cookies.sqlite")
	for _, database := range []string{sourceDB, targetDB} {
		if output, err := exec.Command("sqlite3", database, schema).CombinedOutput(); err != nil {
			t.Fatalf("create fixture: %v %s", err, output)
		}
	}
	insert := `INSERT INTO moz_cookies (originAttributes,name,value,host,path,expiry,lastAccessed,creationTime,isSecure,isHttpOnly,sameSite) VALUES ('','session','fixture-secret','.example.test','/',9999999999,1,1,1,1,1);`
	if output, err := exec.Command("sqlite3", sourceDB, insert).CombinedOutput(); err != nil {
		t.Fatalf("insert fixture: %v %s", err, output)
	}
	store := adapterStore()
	installed, err := store.Install(adapterSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(installed); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run(args, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	// Normalization is independent of disk profile discovery and credentials.
	// Run the real adapter against a synthetic, explicitly bound API export.
	export := filepath.Join(root, "native-export.json")
	native := `{"selection":{"browser":"firefox","profile":"export-only","storeId":"firefox-container-7"},"sites":["https://example.test"],"names":["session"],"partition":"unpartitioned","firstPartyDomain":"example.test","cookies":[{"name":"session","value":"synthetic-export-secret","domain":".example.test","path":"/","secure":true,"httpOnly":true,"hostOnly":false,"session":true,"sameSite":"lax","storeId":"firefox-container-7","firstPartyDomain":"example.test"}]}`
	if err := os.WriteFile(export, []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}
	normalized := filepath.Join(root, "normalized.json")
	normalizeArgs := []string{"share:browser", "cookie", "normalize", "--from", "firefox:export-only", "--store-id", "firefox-container-7", "--from-file", export, "--to-file", normalized}
	if code, output, diagnostics := run(normalizeArgs...); code != 0 || strings.Contains(output+diagnostics, "synthetic-export-secret") {
		t.Fatalf("native normalization failed: code=%d diagnostics=%s", code, diagnostics)
	}
	data, err := os.ReadFile(normalized)
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := browser.ParseCookies(data)
	if err != nil || len(cookies) != 1 || cookies[0].SourceInfo.StoreID != "firefox-container-7" || cookies[0].Attributes["firefox.webextension_first_party_domain"] != "example.test" {
		t.Fatal("native normalization lost scope")
	}
	if info, err := os.Stat(normalized); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("normalized output is not private")
	}
	if code, _, _ := run(normalizeArgs...); code == 0 {
		t.Fatal("normalization overwrote an existing file")
	}
	invalidOutput := filepath.Join(root, "invalid-normalized.json")
	invalidArgs := append([]string{}, normalizeArgs...)
	invalidArgs[4] = "firefox:wrong-profile"
	invalidArgs[len(invalidArgs)-1] = invalidOutput
	if code, _, diagnostics := run(invalidArgs...); code == 0 || strings.Contains(diagnostics, "synthetic-export-secret") {
		t.Fatal("mismatched profile accepted or secret disclosed")
	}
	if _, err := os.Stat(invalidOutput); !os.IsNotExist(err) {
		t.Fatal("failed normalization created output")
	}
	base := []string{"share:browser", "cookie", "copy", "--from", "firefox:source", "--site", "https://example.test", "--name", "session"}
	if code, output, diagnostics := run("share:browser", "cookie", "list", "--from", "firefox:source", "--site", "https://example.test"); code != 0 || !strings.Contains(output, "session\t.example.test") || strings.Contains(output, "fixture-secret") {
		t.Fatalf("list: exit=%d output=%q diagnostics=%q", code, output, diagnostics)
	}
	t.Setenv("CTX_BROWSER", "firefox:source")
	if code, output, diagnostics := run("share:browser", "cookie", "list", "--site", "https://example.test"); code != 0 || !strings.Contains(output, "session\t.example.test") {
		t.Fatalf("selected profile: exit=%d output=%q diagnostics=%q", code, output, diagnostics)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var pipeDiagnostics bytes.Buffer
	pipeCode := Run(append(append([]string{}, base...), "--stdout"), writer, &pipeDiagnostics)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	pipeOutput, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || pipeCode != 0 {
		t.Fatalf("pipe: exit=%d read=%v diagnostics=%q", pipeCode, err, pipeDiagnostics.String())
	}
	var bundle browserCookieBundle
	if err := json.Unmarshal(pipeOutput, &bundle); err != nil || bundle.Cookie.Value != "fixture-secret" {
		t.Fatalf("invalid pipe bundle: %v", err)
	}
	redirect, err := os.Create(filepath.Join(root, "redirect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	if code := Run(append(append([]string{}, base...), "--stdout"), redirect, &diagnostics); code != 2 || !strings.Contains(diagnostics.String(), "requires a pipe") {
		t.Fatalf("redirected stdout: exit=%d diagnostics=%q", code, diagnostics.String())
	}
	if err := redirect.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(redirect.Name()); err != nil || info.Size() != 0 {
		t.Fatalf("redirected file received cookie data: info=%v err=%v", info, err)
	}
	file := filepath.Join(root, "cookie.json")
	if code, _, diagnostics := run(append(append([]string{}, base...), "--to-file", file)...); code != 0 {
		t.Fatalf("file: exit=%d diagnostics=%q", code, diagnostics)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle permissions: info=%v err=%v", info, err)
	}
	if _, err := exec.LookPath("lsof"); err == nil {
		copyArgs := append(append([]string{}, base...), "--to-profile", "firefox:target")
		if code, _, diagnostics := run(copyArgs...); code != 0 {
			t.Fatalf("profile copy: exit=%d diagnostics=%q", code, diagnostics)
		}
		if output, err := exec.Command("sqlite3", targetDB, `SELECT value FROM moz_cookies WHERE name='session'`).Output(); err != nil || strings.TrimSpace(string(output)) != "fixture-secret" {
			t.Fatalf("target cookie missing: %v", err)
		}
		if code, _, diagnostics := run(copyArgs...); code != 1 || !strings.Contains(diagnostics, "already has this cookie") {
			t.Fatalf("duplicate copy: exit=%d diagnostics=%q", code, diagnostics)
		}
		opened, err := os.Open(targetDB)
		if err != nil {
			t.Fatal(err)
		}
		if code, _, diagnostics := run(append(copyArgs, "--replace")...); code != 1 || !strings.Contains(diagnostics, "appears to be open") {
			t.Errorf("open profile: exit=%d diagnostics=%q", code, diagnostics)
		}
		_ = opened.Close()
	}
}
