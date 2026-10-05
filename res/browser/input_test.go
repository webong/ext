package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	browsercontract "github.com/webong/ctx/res/browser/contract"
)

func TestParseCookieExports(t *testing.T) {
	tests := []struct {
		name, input string
		check       func(*testing.T, Cookie)
	}{
		{"query", `{"cookies":[{"name":"session","value":"secret","domain":"example.test","path":"/","source":"custom:work","attributes":{"custom.scope":"work"}}]}`, func(t *testing.T, c Cookie) {
			if c.Source != "custom:work" || c.Attributes["custom.scope"] != "work" {
				t.Fatal("source or native scope lost")
			}
		}},
		{"bundle", `{"version":2,"source":"custom:work","site":"https://example.test","cookie":{"name":"session","value":"secret","domain":"example.test","path":"/"}}`, func(t *testing.T, c Cookie) {
			if c.Source != "custom:work" {
				t.Fatal("bundle source lost")
			}
		}},
		{"JSON aliases", `[{"name":"session","value":"secret","domain":"example.test","path":"/","hostOnly":false,"secure":true,"httpOnly":true,"expirationDate":2000000000.75,"sameSite":"no_restriction","session":false}]`, func(t *testing.T, c Cookie) {
			if c.Domain != ".example.test" || !c.HTTPOnly || c.Expiry != 2000000000 || c.SameSitePolicy != "none" {
				t.Fatalf("aliases not normalized: %+v", c.Cookie)
			}
		}},
		{"Netscape", "# Netscape HTTP Cookie File\n#HttpOnly_example.test\tTRUE\t/\tTRUE\t2000000000\tsession\tsecret\n", func(t *testing.T, c Cookie) {
			if c.Domain != ".example.test" || !c.HTTPOnly || !c.Secure || c.Expiry != 2000000000 {
				t.Fatal("Netscape scope lost")
			}
		}},
		{"empty value", "example.test\tFALSE\t/\tFALSE\t0\tsession\t\n", func(t *testing.T, c Cookie) {
			if c.Value != "" || c.Expiry != 0 {
				t.Fatal("empty session value changed")
			}
		}},
		{"expired subsecond", `[{"name":"session","value":"secret","domain":"example.test","path":"/","expirationDate":0.5}]`, func(t *testing.T, c Cookie) {
			if c.Expiry != -1 {
				t.Fatal("expired timestamp became a session cookie")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cookies, err := ParseCookies([]byte(test.input))
			if err != nil || len(cookies) != 1 {
				t.Fatalf("cookies=%d err=%v", len(cookies), err)
			}
			test.check(t, cookies[0])
		})
	}
}

func TestCookieInputRejectsLossyAndMalformedExports(t *testing.T) {
	inputs := []string{
		`[{"name":"session","domain":"example.test","path":"/"}]`,
		`[{"name":"session","value":"secret","domain":"example.test","path":"/","partitionKey":{"topLevelSite":"https://other.test"}}]`,
		`[{"name":"session","value":"secret","domain":"example.test","path":"/","storeId":"custom-store"}]`,
		`[{"name":"session","value":"secret","domain":"example.test","path":"/","customScope":"work"}]`,
		`[{"name":"session","value":"secret","domain":"example.test","path":"/","expiry":2000000000,"expires":2000000000}]`,
		`[{"name":"session","value":"secret","domain":"example.test","path":"/","secure":null}]`,
		`[{"name":"session","value":"secret\n","domain":"example.test","path":"/"}]`,
		`{"cookies":null}`,
		`{"version":1,"cookie":{}}`,
		`[] {"value":"secret"}`,
		"example.test\tTRUE\t/\tTRUE\tinvalid\tsession\tsecret\n",
	}
	for i, input := range inputs {
		_, err := ParseCookies([]byte(input))
		if err == nil {
			t.Fatalf("input %d was accepted", i)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("input %d leaked its value", i)
		}
	}
	if _, err := ParseCookies([]byte(strings.Repeat(" ", MaxCookieInputBytes+1))); err == nil {
		t.Fatal("oversized input accepted")
	}
}

func TestBundleCookieValidatesSiteAndPreservesScope(t *testing.T) {
	cookie := Cookie{Cookie: browsercontract.Cookie{Name: "session", Value: "secret", Domain: ".example.test", Path: "/account", Secure: true, Attributes: map[string]string{"custom.scope": "work"}}, Source: "custom:work"}
	bundle, err := BundleCookie(cookie, "https://example.test/account?token=sensitive#fragment")
	if err != nil || bundle.Site != "https://example.test/account" || bundle.Source != "custom:work" || bundle.Cookie.Attributes["custom.scope"] != "work" {
		t.Fatalf("unexpected bundle: %v", err)
	}
	if _, err := BundleCookie(cookie, "https://unrelated.test/account"); err == nil {
		t.Fatal("unrelated site accepted")
	}
	if _, err := BundleCookie(cookie, "http://example.test/account"); err == nil {
		t.Fatal("secure cookie accepted over HTTP")
	}
	cookie.Expiry = 1
	if _, err := BundleCookie(cookie, "https://example.test/account"); err == nil {
		t.Fatal("expired cookie accepted")
	}
	result, err := Get(context.Background(), Options{URL: "https://example.test", InlineOnly: true, Inline: InlineCookies{JSON: []byte("[]")}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if string(data) != `{"cookies":[]}` {
		t.Fatalf("empty result is not an array: %s", data)
	}
}
