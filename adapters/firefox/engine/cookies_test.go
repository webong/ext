package firefox

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	share "github.com/webong/ctx/res/browser/contract"
)

func TestFirefoxCookieSchemaExpiryAndNativeScope(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 unavailable")
	}
	const expiry = int64(2000000000)
	for _, version := range []int{15, 16, 17} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "cookies.sqlite")
			scale := int64(1)
			if version >= 16 {
				scale = 1000
			}
			schema := fmt.Sprintf(`PRAGMA user_version=%d; CREATE TABLE moz_cookies (id INTEGER PRIMARY KEY,originAttributes TEXT DEFAULT '',name TEXT,value TEXT,host TEXT,path TEXT,expiry INTEGER,isSecure INTEGER,isHttpOnly INTEGER,sameSite INTEGER,isPartitionedAttributeSet INTEGER DEFAULT 0,creationTime INTEGER,lastAccessed INTEGER,updateTime INTEGER,UNIQUE(name,host,path,originAttributes)); INSERT INTO moz_cookies (name,value,host,path,expiry,isSecure,isHttpOnly,sameSite,originAttributes,isPartitionedAttributeSet) VALUES ('session','fixture-secret','.example.test','/',%d,1,1,1,'^userContextId=1',1);`, version, expiry*scale)
			if out, err := exec.Command("sqlite3", db, schema).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v %s", err, out)
			}
			site, _ := url.Parse("https://example.test")
			cookies, handle, err := queryFirefoxCookies(Config{Name: "fixture"}, db, site, "", false)
			if err != nil || len(cookies) != 1 || cookies[0].Expiry != expiry || cookies[0].Attributes["firefox.partitioned_attribute"] != "true" || cookies[0].Attributes["firefox.origin_attributes"] != "^userContextId=1" {
				t.Fatalf("schema read: cookies=%d err=%v", len(cookies), err)
			}
			value, err := readFirefoxCookieValue(handle, cookies[0])
			if err != nil || value != "fixture-secret" {
				t.Fatal("scoped value not read")
			}
			// A container cookie cannot silently become an ordinary cookie.
			if err := importFirefoxCookie(Config{Name: "fixture"}, db, cookies[0], false); err == nil {
				t.Fatal("native scope was dropped")
			}
			if _, err := exec.LookPath("lsof"); err != nil {
				t.Skip("lsof unavailable")
			}
			cookie := share.Cookie{Name: "imported", Value: "new-secret", Domain: "example.test", Path: "/", Expiry: expiry, SameSitePolicy: "lax"}
			if err := importFirefoxCookie(Config{Name: "fixture"}, db, cookie, false); err != nil {
				t.Fatal(err)
			}
			out, err := runSQLite(db, true, "SELECT expiry FROM moz_cookies WHERE name='imported'")
			var rows []struct {
				Expiry int64 `json:"expiry"`
			}
			if err != nil || json.Unmarshal(out, &rows) != nil || len(rows) != 1 || rows[0].Expiry != expiry*scale {
				t.Fatal("imported expiry uses wrong unit")
			}
			if err := importFirefoxCookie(Config{Name: "fixture"}, db, cookie, false); err == nil {
				t.Fatal("existing cookie overwritten")
			}
			cookie.Value = "replacement"
			if err := importFirefoxCookie(Config{Name: "fixture"}, db, cookie, true); err != nil {
				t.Fatal(err)
			}
			cookie.Name = "session-only"
			cookie.Expiry = 0
			if err := importFirefoxCookie(Config{Name: "fixture"}, db, cookie, false); err == nil || !strings.Contains(err.Error(), "session cookies") {
				t.Fatal("session lifetime was silently changed")
			}
		})
	}
}
