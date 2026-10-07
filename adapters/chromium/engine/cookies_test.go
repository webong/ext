package chromium

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	share "github.com/webong/ext/res/web/contract"
)

func TestChromiumCookieEncryptedImportAndQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows import intentionally unavailable")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 unavailable")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof unavailable")
	}
	root := t.TempDir()
	profile := filepath.Join(root, "Default")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "Preferences"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(profile, "Cookies")
	schema := `CREATE TABLE meta(key TEXT,value INTEGER); INSERT INTO meta VALUES('version',24); CREATE TABLE cookies(host_key TEXT,name TEXT,path TEXT,value TEXT,encrypted_value BLOB,expires_utc INTEGER,is_secure INTEGER,is_httponly INTEGER,samesite INTEGER,has_expires INTEGER,is_persistent INTEGER,top_frame_site_key TEXT,has_cross_site_ancestor INTEGER,UNIQUE(host_key,name,path,top_frame_site_key,has_cross_site_ancestor));`
	if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	password := filepath.Join(root, "password")
	if err := os.WriteFile(password, []byte("fixture-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CTX_BROWSER_FIXTURE_SAFE_STORAGE_PASSWORD_FILE", password)
	config := Config{Name: "fixture"}
	cookie := share.Cookie{Name: "session", Value: "fixture-secret", Domain: ".example.test", Path: "/", Secure: true, HTTPOnly: true, SameSitePolicy: "lax", PartitionKey: "https://top.test", CrossSiteAncestor: true}
	if err := importChromiumCookie(config, profile, cookie, false); err != nil {
		t.Fatal(err)
	}
	site, _ := url.Parse("https://example.test")
	cookies, handle, err := queryChromiumCookies(config, profile, site, "", false)
	if err != nil || len(cookies) != 1 || cookies[0].Expiry != 0 || cookies[0].PartitionKey != cookie.PartitionKey || !cookies[0].CrossSiteAncestor {
		t.Fatalf("scope: cookies=%d err=%v", len(cookies), err)
	}
	value, err := readChromiumCookieValue(config, handle, cookies[0])
	if err != nil || value != cookie.Value {
		t.Fatalf("encrypted read failed: %v", err)
	}
	if err := importChromiumCookie(config, profile, cookie, false); err == nil {
		t.Fatal("existing cookie overwritten")
	}
	cookie.Value = "replacement"
	if err := importChromiumCookie(config, profile, cookie, true); err != nil {
		t.Fatal(err)
	}
	if value, err := readChromiumCookieValue(config, handle, cookies[0]); err != nil || value != "replacement" {
		t.Fatal("replacement unreadable")
	}
	if _, err := runSQLite(db, false, "UPDATE cookies SET host_key='.changed.test'"); err != nil {
		t.Fatal(err)
	}
	changed := cookies[0]
	changed.Domain = ".changed.test"
	if _, err := readChromiumCookieValue(config, handle, changed); err == nil || !strings.Contains(err.Error(), "domain hash") {
		t.Fatal("host binding was ignored")
	}
	out, err := runSQLite(db, true, "SELECT value,hex(encrypted_value) AS encrypted FROM cookies")
	var rows []struct{ Value, Encrypted string }
	if err != nil || json.Unmarshal(out, &rows) != nil || len(rows) != 1 || rows[0].Value != "" || rows[0].Encrypted == "" {
		t.Fatal("import did not encrypt stored value")
	}
	if err := os.WriteFile(filepath.Join(root, "SingletonLock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importChromiumCookie(config, profile, cookie, true); err == nil {
		t.Fatal("locked profile was modified")
	}
}

func TestChromiumCredentialFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Safe Storage password files use macOS/Linux permissions")
	}
	root := t.TempDir()
	path := filepath.Join(root, "key")
	t.Setenv("CTX_BROWSER_FIXTURE_SAFE_STORAGE_PASSWORD_FILE", path)
	config := Config{Name: "fixture"}
	for _, test := range []struct {
		mode  os.FileMode
		data  string
		valid bool
	}{{0o600, "fixture-key\n", true}, {0o400, "fixture-key", true}, {0o644, "fixture-key", false}, {0o600, "", false}, {0o600, "one\ntwo", false}} {
		_ = os.Remove(path)
		if err := os.WriteFile(path, []byte(test.data), test.mode); err != nil {
			t.Fatal(err)
		}
		_, configured, err := configuredSafeStoragePassword(config)
		if !configured || (err == nil) != test.valid {
			t.Fatalf("credential mode=%o valid=%t err=%v", test.mode, test.valid, err)
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "link")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CTX_BROWSER_FIXTURE_SAFE_STORAGE_PASSWORD_FILE", link)
		if _, _, err := configuredSafeStoragePassword(config); err == nil {
			t.Fatal("symlink credential accepted")
		}
	}

}

func TestChromiumWindowsAppBoundUnavailable(t *testing.T) {
	if _, err := decryptChromiumWindowsCookie("unused", []byte("v20encrypted")); err == nil || !strings.Contains(err.Error(), "App-Bound") {
		t.Fatal("v20 protection not reported")
	}
}
