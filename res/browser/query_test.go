package browser

import (
	"testing"

	browsercontract "github.com/webong/ctx/res/browser/contract"
)

func TestCookieMergePreservesOpaqueAdapterScopes(t *testing.T) {
	result := Result{}
	seen := map[string]bool{}
	add := func(attributes map[string]string) {
		appendCookie(&result, seen, Cookie{Cookie: browsercontract.Cookie{
			Name: "session", Domain: "example.test", Path: "/", Attributes: attributes,
		}})
	}
	add(map[string]string{"custom.scope": "work", "custom.partition": "one"})
	add(map[string]string{"custom.partition": "one", "custom.scope": "work"})
	add(map[string]string{"custom.scope": "personal", "custom.partition": "one"})
	add(nil)
	add(map[string]string{})
	if len(result.Cookies) != 3 {
		t.Fatalf("got %d cookies; expected distinct native scopes and one unscoped cookie", len(result.Cookies))
	}
}
