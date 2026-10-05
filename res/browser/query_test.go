package browser

import (
	"context"
	"errors"
	"testing"

	browsercontract "github.com/webong/ctx/res/browser/contract"
)

type queryBackend struct{ sources []Source }

func (backend queryBackend) Sources(context.Context, Options) ([]Source, []string, error) {
	return backend.sources, nil, nil
}

func (queryBackend) Normalize(context.Context, string, string, []byte) ([]byte, error) {
	return nil, errors.New("not supported")
}

func TestGetUsesInjectedBackend(t *testing.T) {
	backend := queryBackend{sources: []Source{{
		Adapter: "fixture", Profile: "work", Label: "fixture:work", SupportsQuery: true,
		Invoke: func(_ context.Context, operation string, args []string, _ []byte) ([]byte, error) {
			if operation != "share" || len(args) != 2 || args[0] != "cookie" || args[1] != "query" {
				t.Fatalf("unexpected invocation %s %v", operation, args)
			}
			return []byte(`{"cookies":[{"name":"session","value":"value","domain":"example.test","path":"/"}]}`), nil
		},
	}}}
	result, err := Get(context.Background(), Options{URL: "https://example.test", Backend: backend})
	if err != nil || len(result.Cookies) != 1 || result.Cookies[0].Source != "fixture:work" {
		t.Fatalf("query result: %#v, %v", result, err)
	}
}

func TestGetRejectsMissingOrInvalidBackend(t *testing.T) {
	if _, err := Get(context.Background(), Options{URL: "https://example.test"}); err == nil {
		t.Fatal("missing backend accepted")
	}
	if _, err := Get(context.Background(), Options{URL: "https://example.test", Backend: queryBackend{sources: []Source{{Label: "invalid"}}}}); err == nil {
		t.Fatal("invalid backend source accepted")
	}
}

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
