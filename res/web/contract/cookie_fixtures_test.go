package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Language-neutral vectors shared with other implementations; see
// ../testdata/cookie/README.md.
const cookieFixtureDir = "../testdata/cookie/v2"

func loadCookieFixture(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cookieFixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestFixtureSite(t *testing.T) {
	var f struct {
		Doc   string `json:"doc"`
		Cases []struct {
			Name   string `json:"name"`
			Input  string `json:"input"`
			Valid  bool   `json:"valid"`
			Expect string `json:"expect"`
		} `json:"cases"`
	}
	loadCookieFixture(t, "site.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := ParseSite(c.Input)
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v, err=%v", c.Valid, err)
			}
			if c.Valid && got.String() != c.Expect {
				t.Fatalf("got %s want %s", got, c.Expect)
			}
		})
	}
}

func TestFixtureDomainAndPathMatching(t *testing.T) {
	var domains struct {
		Doc   string `json:"doc"`
		Cases []struct {
			Name         string `json:"name"`
			SiteHost     string `json:"siteHost"`
			CookieDomain string `json:"cookieDomain"`
			Match        bool   `json:"match"`
		} `json:"cases"`
	}
	loadCookieFixture(t, "domain-match.json", &domains)
	for _, c := range domains.Cases {
		t.Run("domain "+c.Name, func(t *testing.T) {
			if CookieDomainMatches(c.SiteHost, c.CookieDomain) != c.Match {
				t.Fatalf("CookieDomainMatches(%q, %q) != %v", c.SiteHost, c.CookieDomain, c.Match)
			}
		})
	}
	var paths struct {
		Doc   string `json:"doc"`
		Cases []struct {
			Name        string `json:"name"`
			RequestPath string `json:"requestPath"`
			CookiePath  string `json:"cookiePath"`
			Match       bool   `json:"match"`
		} `json:"cases"`
	}
	loadCookieFixture(t, "path-match.json", &paths)
	if len(domains.Cases) == 0 || len(paths.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range paths.Cases {
		t.Run("path "+c.Name, func(t *testing.T) {
			if CookiePathMatches(c.RequestPath, c.CookiePath) != c.Match {
				t.Fatalf("CookiePathMatches(%q, %q) != %v", c.RequestPath, c.CookiePath, c.Match)
			}
		})
	}
}

func TestFixtureCookie(t *testing.T) {
	var f struct {
		Doc   string `json:"doc"`
		Cases []struct {
			Name   string `json:"name"`
			Cookie Cookie `json:"cookie"`
			Valid  bool   `json:"valid"`
		} `json:"cases"`
	}
	loadCookieFixture(t, "cookie.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			err := ValidateCookie(c.Cookie)
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v, err=%v", c.Valid, err)
			}
			// Errors must not carry the secret value. Use a distinctive one.
			secret := c.Cookie
			secret.Value = "s3cr3t-value-0f7a"
			if err := ValidateCookie(secret); err != nil && bytes.Contains([]byte(err.Error()), []byte(secret.Value)) {
				t.Fatal("error leaks the cookie value")
			}
		})
	}
}

func TestFixtureBundle(t *testing.T) {
	var f struct {
		Doc   string `json:"doc"`
		Cases []struct {
			Name   string       `json:"name"`
			Bundle CookieBundle `json:"bundle"`
			Valid  bool         `json:"valid"`
		} `json:"cases"`
	}
	loadCookieFixture(t, "bundle.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if err := ValidateCookieBundle(c.Bundle); (err == nil) != c.Valid {
				t.Fatalf("valid=%v, err=%v", c.Valid, err)
			}
		})
	}
}
