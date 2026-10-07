package web

import (
	"reflect"
	"strings"
	"testing"
	"time"

	browsercontract "github.com/webong/ext/res/web/contract"
)

func outputCookie(name, value, domain, path string) Cookie {
	return Cookie{Cookie: browsercontract.Cookie{Name: name, Value: value, Domain: domain, Path: path}}
}

func TestCookieHeaderRequestScopeAndOrder(t *testing.T) {
	base := outputCookie("session", "root", ".example.test", "/")
	account := outputCookie("session", "account", "example.test", "/account")
	secure := outputCookie("secure", "https", "example.test", "/")
	secure.Secure = true
	expired := outputCookie("expired", "old", "example.test", "/")
	expired.Expiry = time.Now().Unix() - 1
	cookies := []Cookie{base, expired, outputCookie("other", "no", "other.test", "/"), account, secure,
		outputCookie("wrongpath", "no", "example.test", "/accounts"), outputCookie("empty", "", "example.test", "/")}
	for _, test := range []struct{ site, want string }{
		{"https://example.test/account/details", "session=account; session=root; secure=https; empty="},
		{"http://example.test/account", "session=account; session=root; empty="},
		{"https://sub.example.test/", "session=root"},
		{"https://unrelated.test/", ""},
	} {
		header, err := CookieHeader(cookies, test.site)
		if err != nil || header != test.want {
			t.Fatalf("site=%s header=%q err=%v", test.site, header, err)
		}
	}
}

func TestCookieOutputRejectsScopeLossAndHeaderInjection(t *testing.T) {
	for _, mutate := range []func(*Cookie){
		func(c *Cookie) { c.PartitionKey = "https://top.test" },
		func(c *Cookie) { c.Attributes = map[string]string{"custom.container": "work"} },
	} {
		cookie := outputCookie("session", "secret-value", "example.test", "/")
		mutate(&cookie)
		if _, err := CookieHeader([]Cookie{cookie}, "https://example.test"); err == nil || strings.Contains(err.Error(), cookie.Value) {
			t.Fatal("header lost isolation or revealed the value")
		}
		if _, err := NetscapeCookies([]Cookie{cookie}); err == nil || strings.Contains(err.Error(), cookie.Value) {
			t.Fatal("jar lost isolation or revealed the value")
		}
	}
	for _, value := range []string{"secret; other=1", "secret\r\nInjected: 1", "secret with spaces", "secret,other", "secret\"", "secret\\", "secreté"} {
		cookie := outputCookie("session", value, "example.test", "/")
		if _, err := CookieHeader([]Cookie{cookie}, "https://example.test"); err == nil || strings.Contains(err.Error(), value) {
			t.Fatal("invalid header value accepted or exposed")
		}
	}
	if _, err := CookieHeader(nil, "example.test"); err == nil {
		t.Fatal("header accepted an unspecified scheme")
	}
}

func TestNetscapeOutputRoundTrip(t *testing.T) {
	persistent := outputCookie("session", "jar-value", ".example.test", "/account")
	persistent.Secure, persistent.HTTPOnly, persistent.Expiry = true, true, 2000000000
	session := outputCookie("empty", "", "example.test", "/")
	expired := outputCookie("expired", "old", "example.test", "/")
	expired.Expiry = -1
	jar, err := NetscapeCookies([]Cookie{persistent, session, expired})
	if err != nil || !strings.Contains(jar, "#HttpOnly_.example.test\tTRUE\t/account\tTRUE") {
		t.Fatal("jar fields not preserved")
	}
	parsed, err := ParseCookies([]byte(jar))
	if err != nil || len(parsed) != 3 || !reflect.DeepEqual(parsed[0].Cookie, persistent.Cookie) || parsed[1].Value != "" || parsed[1].Expiry != 0 || parsed[2].Expiry != 1 {
		t.Fatalf("jar round trip failed: %v", err)
	}
}
