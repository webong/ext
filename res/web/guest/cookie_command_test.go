package guest

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/contract"
)

func TestCookieProtocolFiltersAndIsolatesReadFailures(t *testing.T) {
	cookies := []contract.Cookie{
		{ID: 1, Name: "bad", Value: "must-not-leak", Domain: "example.test", Path: "/"},
		{ID: 2, Name: "good", Value: "must-not-leak", Domain: "example.test", Path: "/"},
		{ID: 3, Name: "unrelated", Value: "must-not-leak", Domain: "other.test", Path: "/"},
	}
	backend := CookieBackend{
		List:  func(string, *url.URL, string) ([]contract.Cookie, string, error) { return cookies, "handle", nil },
		Query: func(string, *url.URL, bool) ([]contract.Cookie, string, error) { return cookies, "handle", nil },
		ReadValue: func(_ string, c contract.Cookie) (string, error) {
			if c.Name == "bad" {
				return "", errors.New("unavailable key")
			}
			return "fixture-secret", nil
		},
	}
	var out, diagnostics bytes.Buffer
	request := `{"version":2,"site":"https://example.test"}`
	if code := RunCookie("default", "list", strings.NewReader(request), &out, &diagnostics, backend); code != 0 || strings.Contains(out.String(), "must-not-leak") || strings.Contains(out.String(), "unrelated") {
		t.Fatalf("list leaked a value or unrelated scope: %d", code)
	}
	out.Reset()
	if code := RunCookie("default", "query", strings.NewReader(request), &out, &diagnostics, backend); code != 0 {
		t.Fatalf("query: %d %s", code, diagnostics.String())
	}
	var result contract.CookieQueryResult
	if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Cookies) != 1 || result.Cookies[0].Name != "good" || len(result.Warnings) != 1 {
		t.Fatal("partial read was not preserved")
	}
	for _, input := range []string{request + " {}", request + strings.Repeat(" ", 8<<20)} {
		out.Reset()
		if code := RunCookie("default", "query", strings.NewReader(input), &out, &diagnostics, backend); code != 2 || out.Len() != 0 {
			t.Fatal("malformed request reached backend")
		}
	}
}
