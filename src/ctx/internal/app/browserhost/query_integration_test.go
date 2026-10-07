package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	modpkg "github.com/webong/ext/pkg/plugin/adapter"
	"github.com/webong/ext/res/browser"
	browsercontract "github.com/webong/ext/res/browser/contract"
)

func installQueryFixture(t *testing.T, store *modpkg.Store, name string, priority int, auto bool, query string, legacy bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	operations := "cookie.query"
	if legacy {
		operations = "cookie.list,cookie.export"
	}
	manifest := fmt.Sprintf("api_version = \"2.0\"\nname = %q\nruntime = \"browser\"\nsurfaces = \"web\"\nexecutable = \"adapter\"\ncapabilities = \"list,share,validate,doctor\"\nselector_key = \"browser\"\nshare_spaces = \"browser\"\nbrowser_share = %q\nbrowser_query_priority = \"%d\"\nbrowser_query_auto = \"%t\"\n", name, operations, priority, auto)
	script := "#!/bin/sh\nroot=$(dirname \"$0\")\ncase \"$1\" in\nlist) printf '%s:default\\n' \"$CTX_ADAPTER_NAME\";;\nshare) " + query + ";;\nesac\n"
	for filename, contents := range map[string]string{"adapter.toml": manifest, "adapter": script} {
		mode := os.FileMode(0o600)
		if filename == "adapter" {
			mode = 0o700
		}
		if err := os.WriteFile(filepath.Join(root, filename), []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	a, err := store.Install(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(a); err != nil {
		t.Fatal(err)
	}
}

func cookieResponse(value string) string {
	data, _ := json.Marshal(browsercontract.CookieQueryResult{Cookies: []browsercontract.Cookie{{Name: "session", Value: value, Domain: "example.test", Path: "/"}}, StorePath: "/fixture/Cookies"})
	return "printf '%s' '" + string(data) + "'"
}

func TestQuerySourceOrderFallbackAndTrust(t *testing.T) {
	home := filepath.Join(t.TempDir(), "adapters")
	store := modpkg.NewStore(home)
	installQueryFixture(t, store, "alpha", 20, true, cookieResponse("alpha"), false)
	installQueryFixture(t, store, "beta", 10, true, cookieResponse("beta"), false)
	installQueryFixture(t, store, "optin", 0, false, cookieResponse("optin"), false)
	options := browser.Options{URL: "https://example.test", Backend: Provider{AdapterHome: home}, FallbackInline: browser.InlineCookies{JSON: []byte(`[{"name":"session","value":"fallback","domain":"example.test","path":"/"},{"name":"csrf","value":"missing","domain":"example.test","path":"/"}]`)}}
	result, err := browser.Get(context.Background(), options)
	if err != nil || len(result.Cookies) != 2 || result.Cookies[0].Value != "beta" || !result.Cookies[1].SourceInfo.Fallback || result.Cookies[0].SourceInfo.StorePath != "/fixture/Cookies" {
		t.Fatalf("merge: cookies=%d err=%v", len(result.Cookies), err)
	}
	options.PreferredSource = "alpha:default"
	result, err = browser.Get(context.Background(), options)
	if err != nil || result.Cookies[0].Value != "alpha" {
		t.Fatal("preferred selection was not first")
	}
	options.Sources = []string{"optin:default", "beta:default"}
	result, err = browser.Get(context.Background(), options)
	if err != nil || result.Cookies[0].Value != "optin" {
		t.Fatal("explicit opt-in order lost")
	}
	options.Mode = browser.ModeFirst
	result, err = browser.Get(context.Background(), options)
	if err != nil || len(result.Cookies) != 1 {
		t.Fatal("first mode unexpectedly filled missing scopes")
	}
	if err := store.RemoveTrust("optin"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser.Get(context.Background(), options); err == nil {
		t.Fatal("untrusted explicit source executed")
	}
}

func TestQueryCancellationAndMalformedSources(t *testing.T) {
	home := filepath.Join(t.TempDir(), "adapters")
	store := modpkg.NewStore(home)
	installQueryFixture(t, store, "slow", 10, true, "sleep 5 & wait", false)
	start := time.Now()
	result, err := browser.Get(context.Background(), browser.Options{URL: "https://example.test", Sources: []string{"slow:default"}, Backend: Provider{AdapterHome: home}, Timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || len(result.Cookies) != 0 || time.Since(start) > time.Second {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := browser.Get(ctx, browser.Options{URL: "https://example.test", InlineOnly: true}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	installQueryFixture(t, store, "malformed", 10, true, `printf '%s' '{"cookies":[{"name":"bad","value":"secret","domain":"example.test","path":"/","secure":null},{"name":"good","value":"okay","domain":"example.test","path":"/"}]}'`, false)
	result, err = browser.Get(context.Background(), browser.Options{URL: "https://example.test", Sources: []string{"malformed:default"}, Backend: Provider{AdapterHome: home}})
	if err != nil || len(result.Cookies) != 1 || result.Cookies[0].Name != "good" || len(result.Warnings) != 1 || strings.Contains(result.Warnings[0], "secret") {
		t.Fatal("malformed cookie was not isolated safely")
	}
}

func TestLegacyQueryContinuesAfterIndividualExportFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "adapters")
	store := modpkg.NewStore(home)
	script := `request=$(cat)
if [ "$5" = list ]; then
printf '%s' '[{"id":1,"name":"bad","value":"","domain":"example.test","path":"/"},{"id":2,"name":"good","value":"","domain":"example.test","path":"/"}]'
elif printf '%s' "$request" | grep -q '"name":"bad"'; then
printf '%s' 'unavailable OS key' >&2; exit 1
else
printf '%s' '{"id":2,"name":"good","value":"okay","domain":"example.test","path":"/"}'
fi`
	installQueryFixture(t, store, "legacy", 10, true, script, true)
	result, err := browser.Get(context.Background(), browser.Options{URL: "https://example.test", Sources: []string{"legacy:default"}, Backend: Provider{AdapterHome: home}})
	if err != nil || len(result.Cookies) != 1 || result.Cookies[0].Name != "good" || len(result.Warnings) != 1 {
		t.Fatalf("legacy partial read: cookies=%d warnings=%d err=%v", len(result.Cookies), len(result.Warnings), err)
	}
}
