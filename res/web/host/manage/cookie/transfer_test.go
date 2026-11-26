package cookie

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/host/contract"
)

func TestCookieTransferVerifiesSelectionBeforeImport(t *testing.T) {
	site, err := contract.ParseSite("https://example.test/path")
	if err != nil {
		t.Fatal(err)
	}
	listedCookie := contract.Cookie{ID: 7, Name: "session", Value: "listed-secret", Domain: "example.test", Path: "/", Secure: true}
	source := func(_ context.Context, operation string, request contract.CookieRequest, response any) error {
		switch operation {
		case "list":
			*response.(*[]contract.Cookie) = []contract.Cookie{listedCookie}
		case "export":
			if request.Cookie.Value != "" || request.Cookie.ID != 7 {
				t.Fatal("export did not use redacted selected metadata")
			}
			*response.(*contract.Cookie) = listedCookie
		default:
			t.Fatalf("unexpected operation %q", operation)
		}
		return nil
	}
	listed, err := ListForSite(context.Background(), source, site)
	if err != nil || len(listed) != 1 || listed[0].Value != "" {
		t.Fatalf("list = %#v, %v", listed, err)
	}
	selected, err := Select(listed, Selector{Name: "session", ID: 7})
	if err != nil {
		t.Fatal(err)
	}
	imported := false
	destination := func(_ context.Context, operation string, request contract.CookieRequest, _ any) error {
		if operation != "import" || request.Bundle == nil || request.Bundle.Cookie.Value != "listed-secret" || !request.Replace {
			t.Fatalf("import request = %#v", request)
		}
		imported = true
		return nil
	}
	bundle, err := Copy(context.Background(), source, destination, "browser:profile", site, selected, true)
	if err != nil || !imported || bundle.Source != "browser:profile" || bundle.Site != "https://example.test/path" {
		t.Fatalf("copy = %#v, imported=%t, %v", bundle, imported, err)
	}
	if _, err := ExportSelected(context.Background(), source, "browser:profile", site, listedCookie); err != nil {
		t.Fatalf("direct export with a valued selection: %v", err)
	}
	listedCookie.ID = 8
	imported = false
	_, err = Copy(context.Background(), source, destination, "browser:profile", site, selected, false)
	if err == nil || imported || strings.Contains(err.Error(), "listed-secret") {
		t.Fatalf("mismatched export reached destination or leaked value: %v", err)
	}
}

func TestSelectRequiresExactlyOneMetadataMatch(t *testing.T) {
	listed := []contract.Cookie{{Name: "session", Domain: "example.test", Path: "/", ID: 1, Value: "secret", Attributes: map[string]string{"browser.store": "first"}}, {Name: "session", Domain: "example.test", Path: "/", ID: 2, Attributes: map[string]string{"browser.store": "second"}}}
	if _, err := Select(listed, Selector{Name: "other"}); !errors.Is(err, ErrNoMatch) {
		t.Fatal(err)
	}
	if _, err := Select(listed, Selector{Name: "session"}); !errors.Is(err, ErrAmbiguous) {
		t.Fatal(err)
	}
	if chosen, err := Select(listed, Selector{Name: "session", ID: 2}); err != nil || chosen.ID != 2 {
		t.Fatalf("select = %#v, %v", chosen, err)
	}
	if chosen, err := Select(listed, Selector{Name: "session", Attributes: map[string]string{"browser.store": "first"}}); err != nil || chosen.ID != 1 || chosen.Value != "" {
		t.Fatalf("attribute selection = %#v, %v", chosen, err)
	}
}
