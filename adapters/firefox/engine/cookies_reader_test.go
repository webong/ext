package firefox

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// These tests read a committed synthetic cookies.sqlite (made by
// res/web/sqlite/testdata/make-fixtures.sh) with no sqlite3 binary on the PATH.

func TestFirefoxCookiesAreReadWithoutTheShell(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	database, err := filepath.Abs(filepath.Join("testdata", "cookies.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Name: "fixture"}
	site, _ := url.Parse("https://example.com/")
	cookies, handle, err := queryFirefoxCookies(config, database, site, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if handle != database || len(cookies) != 2 {
		t.Fatalf("handle %q cookies %+v", handle, cookies)
	}
	sid, container := cookies[0], cookies[1]
	// The fixture is schema 16, whose expiry is in milliseconds.
	if sid.ID != 1 || sid.Name != "sid" || sid.Domain != "example.com" || !sid.Secure || !sid.HTTPOnly || sid.SameSitePolicy != "lax" || sid.Expiry != 1893456000 {
		t.Fatalf("sid: %+v", sid)
	}
	if container.ID != 4 || container.Attributes["firefox.container_id"] != "2" || container.Attributes["firefox.origin_attributes"] != "^userContextId=2" {
		t.Fatalf("container cookie: %+v", container)
	}
	named, _, err := queryFirefoxCookies(config, database, site, "sid", false)
	if err != nil || len(named) != 1 {
		t.Fatalf("name filter: %+v %v", named, err)
	}
	other, _ := url.Parse("https://www.example.net/x/y")
	partitioned, _, err := queryFirefoxCookies(config, database, other, "", true)
	if err != nil || len(partitioned) != 1 || partitioned[0].Attributes["firefox.partitioned_attribute"] != "true" || partitioned[0].SameSitePolicy != "strict" || partitioned[0].Expiry != -1 {
		t.Fatalf("partitioned: %+v %v", partitioned, err)
	}

	value, err := readFirefoxCookieValue(handle, sid)
	if err != nil || value != "firefox-value" {
		t.Fatalf("value: %q %v", value, err)
	}
	// The scope and listing must still match the stored row.
	changed := sid
	changed.Path = "/other"
	if _, err := readFirefoxCookieValue(handle, changed); err == nil || !strings.Contains(err.Error(), "changed since listing") {
		t.Fatalf("path mismatch: %v", err)
	}
	wrongScope := container
	wrongScope.Attributes = map[string]string{"firefox.origin_attributes": "^userContextId=3"}
	if _, err := readFirefoxCookieValue(handle, wrongScope); err == nil {
		t.Fatal("a cookie from another container was read")
	}
	missing := sid
	missing.ID = 77
	if _, err := readFirefoxCookieValue(handle, missing); err == nil {
		t.Fatal("missing row accepted")
	}
	if _, _, err := queryFirefoxCookies(config, filepath.Join("testdata", "missing.sqlite"), site, "", false); err == nil {
		t.Fatal("missing database accepted")
	}
}
