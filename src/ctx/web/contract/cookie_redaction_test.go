package contract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const cookieSecret = "s3cr3t-cookie-value-9d41"

func secretCookie() Cookie {
	return Cookie{ID: 7, Ref: "ref-1", Name: "sid", Value: cookieSecret, Domain: ".example.com", Path: "/", Secure: true, HTTPOnly: true, Expiry: 4102444800, PartitionKey: "https://top.example"}
}

func TestCookieFormattingNeverPrintsValues(t *testing.T) {
	cookie := secretCookie()
	bundle := CookieBundle{Version: Version, Source: "example", Site: "https://example.com/", Cookie: cookie}
	result := CookieQueryResult{Cookies: []Cookie{cookie, cookie}, Warnings: []string{"one row failed"}, StorePath: "/tmp/store"}
	request := CookieRequest{Version: Version, Site: "https://example.com/", Names: []string{"sid"}, Cookie: cookie, Bundle: &bundle, NativeExport: json.RawMessage(`{"value":"` + cookieSecret + `"}`)}
	values := map[string]any{
		"cookie": cookie, "cookie pointer": &cookie, "bundle": bundle, "bundle pointer": &bundle,
		"result": result, "result pointer": &result, "request": request, "request pointer": &request,
		"slice": []Cookie{cookie}, "pointer slice": []*Cookie{&cookie}, "map": map[string]Cookie{"k": cookie},
		"nested map": map[string][]Cookie{"k": {cookie}}, "struct holder": struct{ C Cookie }{cookie},
		"array": [1]Cookie{cookie}, "interface slice": []any{cookie, &bundle},
	}
	for name, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			rendered := fmt.Sprintf(format, value)
			if strings.Contains(rendered, cookieSecret) {
				t.Errorf("%s with %s leaks the value: %s", name, format, rendered)
			}
		}
		if rendered := fmt.Sprint(value); strings.Contains(rendered, cookieSecret) {
			t.Errorf("%s with Sprint leaks the value: %s", name, rendered)
		}
	}
	// Identity stays visible so logs remain useful.
	if rendered := cookie.String(); !strings.Contains(rendered, `name="sid"`) || !strings.Contains(rendered, `domain=".example.com"`) || !strings.Contains(rendered, "value=<redacted>") {
		t.Fatalf("redacted form lost the identity: %s", rendered)
	}
	if rendered := (Cookie{Name: "sid"}).String(); strings.Contains(rendered, "redacted") {
		t.Fatalf("an empty value must not be reported as redacted: %s", rendered)
	}
}

func TestCookieWireFormKeepsTheValue(t *testing.T) {
	encoded, err := json.Marshal(secretCookie())
	if err != nil || !strings.Contains(string(encoded), cookieSecret) {
		t.Fatalf("the protocol needs the real value: %s %v", encoded, err)
	}
	var back Cookie
	if err := json.Unmarshal(encoded, &back); err != nil || back.Value != cookieSecret {
		t.Fatalf("round trip lost the value: %+v %v", back, err)
	}
}

func TestCookieValidationErrorsNeverCarryTheValue(t *testing.T) {
	cases := map[string]Cookie{
		"control character": {Name: "sid", Value: cookieSecret + "\n", Domain: "example.com", Path: "/"},
		"bad name":          {Name: "bad name", Value: cookieSecret, Domain: "example.com", Path: "/"},
		"bad domain":        {Name: "sid", Value: cookieSecret, Domain: "a/b", Path: "/"},
		"secure prefix":     {Name: "__Secure-sid", Value: cookieSecret, Domain: "example.com", Path: "/"},
		"host prefix":       {Name: "__Host-sid", Value: cookieSecret, Domain: ".example.com", Path: "/", Secure: true},
		"samesite none":     {Name: "sid", Value: cookieSecret, Domain: "example.com", Path: "/", SameSitePolicy: "none"},
		"expiry":            {Name: "sid", Value: cookieSecret, Domain: "example.com", Path: "/", Expiry: 1 << 40},
		"partition":         {Name: "sid", Value: cookieSecret, Domain: "example.com", Path: "/", CrossSiteAncestor: true},
		"attribute":         {Name: "sid", Value: cookieSecret, Domain: "example.com", Path: "/", Attributes: map[string]string{"bad": cookieSecret}},
		"path":              {Name: "sid", Value: cookieSecret, Domain: "example.com", Path: "x"},
	}
	for name, cookie := range cases {
		err := ValidateCookie(cookie)
		if err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), cookieSecret) {
			t.Errorf("%s: error carries the value: %v", name, err)
		}
		bundle := CookieBundle{Version: Version, Source: "x", Site: "https://example.com/", Cookie: cookie}
		if err := ValidateCookieBundle(bundle); err != nil && strings.Contains(err.Error(), cookieSecret) {
			t.Errorf("%s: bundle error carries the value: %v", name, err)
		}
	}
	if err := ValidateCookieBundle(CookieBundle{Version: 1, Cookie: secretCookie()}); err == nil || strings.Contains(err.Error(), cookieSecret) {
		t.Errorf("bundle version error: %v", err)
	}
}
