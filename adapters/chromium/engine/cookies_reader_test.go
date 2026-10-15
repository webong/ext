package chromium

import (
	"crypto/sha256"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// These tests read a committed synthetic Cookies database (made by
// res/web/sqlite/testdata/make-fixtures.sh) and run with no sqlite3 binary on
// the PATH, because the cookie readers use the built-in SQLite reader.

func noShell(t *testing.T) string {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	path, err := filepath.Abs(filepath.Join("testdata", "Cookies"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func siteURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	site, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func TestChromiumCookiesAreReadWithoutTheShell(t *testing.T) {
	database := noShell(t)
	config := Config{Name: "fixture"}
	cookies, handle, err := queryChromiumCookies(config, database, siteURL(t, "https://example.com/app"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if handle != database {
		t.Fatalf("handle %q", handle)
	}
	if len(cookies) != 2 || cookies[0].Name != "sid" || cookies[1].Name != "pref" {
		t.Fatalf("host and parent-domain matching: %+v", cookies)
	}
	sid := cookies[0]
	wantExpiry := (int64(14500000000000000) - chromiumEpochOffsetMicros) / 1_000_000
	if sid.Domain != "example.com" || sid.Path != "/" || !sid.Secure || !sid.HTTPOnly || sid.Expiry != wantExpiry || sid.SameSitePolicy != "unspecified" || sid.ID != 1 {
		t.Fatalf("sid: %+v", sid)
	}
	if pref := cookies[1]; pref.Domain != ".example.com" || pref.Path != "/app" || pref.Expiry != -1 && pref.Expiry != 0 {
		t.Fatalf("pref: %+v", pref)
	}
	// A name filter and a different host.
	named, _, err := queryChromiumCookies(config, database, siteURL(t, "https://example.com/app"), "pref", false)
	if err != nil || len(named) != 1 || named[0].Name != "pref" {
		t.Fatalf("name filter: %+v %v", named, err)
	}
	partitioned, _, err := queryChromiumCookies(config, database, siteURL(t, "https://x.sub.example.com/"), "", false)
	if err != nil || len(partitioned) != 1 || partitioned[0].PartitionKey != "https://top.example" || !partitioned[0].CrossSiteAncestor {
		t.Fatalf("partitioned: %+v %v", partitioned, err)
	}
	// The whole store when no site is given.
	all, _, err := queryChromiumCookies(config, database, nil, "", true)
	if err != nil || len(all) != 6 {
		t.Fatalf("all cookies: %d %v", len(all), err)
	}
	// Names and values never appear in errors for a database that is not one.
	if _, _, err := queryChromiumCookies(config, filepath.Join("testdata", "missing"), nil, "", true); err == nil {
		t.Fatal("missing database accepted")
	}
}

func TestChromiumCookieValuesWithoutTheShell(t *testing.T) {
	database := noShell(t)
	config := Config{Name: "fixture"}
	cookies, handle, err := queryChromiumCookies(config, database, siteURL(t, "https://example.com/app"), "", false)
	if err != nil || len(cookies) != 2 {
		t.Fatalf("%v %d", err, len(cookies))
	}
	fail := func(string, []byte) (string, error) { t.Fatal("a plaintext value needs no decryptor"); return "", nil }
	if value, err := readChromiumCookieValueWithDecryptor(handle, cookies[0], fail); err != nil || value != "plain-value" {
		t.Fatalf("plaintext: %q %v", value, err)
	}
	// Database version 24 binds an encrypted value to its host key.
	pref := cookies[1]
	hash := sha256.Sum256([]byte(pref.Domain))
	good := func(string, []byte) (string, error) { return string(hash[:]) + "decrypted-value", nil }
	if value, err := readChromiumCookieValueWithDecryptor(handle, pref, good); err != nil || value != "decrypted-value" {
		t.Fatalf("encrypted: %q %v", value, err)
	}
	other := sha256.Sum256([]byte("other.example"))
	bad := func(string, []byte) (string, error) { return string(other[:]) + "decrypted-value", nil }
	if _, err := readChromiumCookieValueWithDecryptor(handle, pref, bad); err == nil || !strings.Contains(err.Error(), "domain hash") {
		t.Fatalf("host binding: %v", err)
	}
	// A listing that no longer matches the database is refused.
	moved := pref
	moved.Path = "/elsewhere"
	if _, err := readChromiumCookieValueWithDecryptor(handle, moved, good); err == nil || !strings.Contains(err.Error(), "changed since listing") {
		t.Fatalf("stale listing: %v", err)
	}
	gone := pref
	gone.ID = 99
	if _, err := readChromiumCookieValueWithDecryptor(handle, gone, good); err == nil || !strings.Contains(err.Error(), "changed since listing") {
		t.Fatalf("missing row: %v", err)
	}
	partition := pref
	partition.PartitionKey = "https://not-the-same"
	if _, err := readChromiumCookieValueWithDecryptor(handle, partition, good); err == nil {
		t.Fatal("partition mismatch accepted")
	}
	// The value of the decryptor's failure is not shown to the caller as a cookie.
	failing := func(string, []byte) (string, error) { return "", errors.New("key unavailable") }
	if _, err := readChromiumCookieValueWithDecryptor(handle, pref, failing); err == nil {
		t.Fatal("decryptor failure accepted")
	}
}
