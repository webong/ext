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

	"github.com/webong/ctx/internal/mod"
	"github.com/webong/ctx/res/browser"
)

func normalizeFixture(t *testing.T, operation, response string) (string, *mod.Store) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	home := filepath.Join(root, "installed")
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("api_version = \"2.0\"\nname = \"fixture\"\nruntime = \"browser\"\nsurfaces = \"web\"\nexecutable = \"adapter\"\ncapabilities = \"share,validate,doctor\"\nshare_spaces = \"browser\"\nbrowser_share = %q\n", operation)
	script := "#!/bin/sh\n" + response + "\n"
	for name, data := range map[string]string{"adapter.toml": manifest, "adapter": script} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	store := mod.NewStore(home)
	adapter, err := store.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(adapter); err != nil {
		t.Fatal(err)
	}
	return home, store
}

func TestNormalizeDispatchAndPortableRoundTrip(t *testing.T) {
	root := t.TempDir()
	captured := filepath.Join(root, "request")
	t.Setenv("CTX_NORMALIZE_REQUEST", captured)
	home, store := normalizeFixture(t, "cookie.normalize", `test "$1" = share && test "$2" = Default && test "$4" = cookie && test "$5" = normalize || exit 2
cat > "$CTX_NORMALIZE_REQUEST"
printf '%s' '{"store_id":"0","cookies":[{"name":"session","value":"synthetic-secret","domain":"example.test","path":"/","attributes":{"fixture.webextension_store_id":"0"}}]}'`)
	options := browser.NormalizeOptions{Source: "fixture:Default", StoreID: "0", Export: json.RawMessage(`{"selection":{"storeId":"0"},"cookies":[]}`), Backend: Provider{AdapterHome: home}}
	result, err := browser.Normalize(context.Background(), options)
	if err != nil || len(result.Cookies) != 1 {
		t.Fatalf("normalize: %v", err)
	}
	cookie := result.Cookies[0]
	if cookie.Source != "fixture:Default" || cookie.SourceInfo.Adapter != "fixture" || cookie.SourceInfo.Profile != "Default" || cookie.SourceInfo.StoreID != "0" {
		t.Fatal("source binding missing")
	}
	request, _ := os.ReadFile(captured)
	var envelope struct {
		Version int
		StoreID string          `json:"store_id"`
		Export  json.RawMessage `json:"native_export"`
	}
	if json.Unmarshal(request, &envelope) != nil || envelope.Version != 2 || envelope.StoreID != "0" || string(envelope.Export) != string(options.Export) {
		t.Fatal("request contract changed")
	}
	data, _ := json.Marshal(result)
	parsed, err := browser.ParseCookies(data)
	if err != nil || len(parsed) != 1 || parsed[0].SourceInfo.StoreID != "0" || parsed[0].Attributes["fixture.webextension_store_id"] != "0" {
		t.Fatal("JSON round trip lost scope")
	}
	inline, err := browser.Get(context.Background(), browser.Options{URL: "https://example.test", InlineOnly: true, Inline: browser.InlineCookies{JSON: data}})
	if err != nil || len(inline.Cookies) != 1 || inline.Cookies[0].Attributes["fixture.webextension_store_id"] != "0" {
		t.Fatal("normalized inline input failed")
	}
	if _, err := browser.CookieHeader(parsed, "https://example.test"); err == nil {
		t.Fatal("opaque store lost in HTTP output")
	}
	if err := store.RemoveTrust("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := browser.Normalize(context.Background(), options); err == nil {
		t.Fatal("untrusted normalizer executed")
	}
}

func TestNormalizeRejectsInvalidOptionsResponsesAndTimeout(t *testing.T) {
	for _, options := range []browser.NormalizeOptions{{}, {Source: "fixture", StoreID: "0", Export: []byte(`{}`)}, {Source: "fixture:Default", Export: []byte(`{}`)}, {Source: "fixture:Default", StoreID: "0", Export: []byte(`{`)}, {Source: "fixture:Default", StoreID: "0", Export: []byte(`{}`), Timeout: -1}} {
		if _, err := browser.Normalize(context.Background(), options); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	if _, err := browser.Normalize(nil, browser.NormalizeOptions{}); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := browser.Normalize(ctx, browser.NormalizeOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	for _, response := range []string{`{"cookies":[]}`, `{"store_id":"wrong","cookies":[]}`, `{"store_id":"0","cookies":null}`, `{"store_id":"0","cookies":[{"name":"session","value":"synthetic-secret","domain":"example.test","path":"/","storeId":"0"}]}`} {
		home, _ := normalizeFixture(t, "cookie.normalize", "printf '%s' '"+response+"'")
		if _, err := browser.Normalize(context.Background(), browser.NormalizeOptions{Source: "fixture:Default", StoreID: "0", Export: []byte(`{}`), Backend: Provider{AdapterHome: home}}); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("invalid response accepted or secret disclosed")
		}
	}
	home, _ := normalizeFixture(t, "cookie.query", "exit 0")
	options := browser.NormalizeOptions{Source: "fixture:Default", StoreID: "0", Export: []byte(`{}`), Backend: Provider{AdapterHome: home}}
	if _, err := browser.Normalize(context.Background(), options); err == nil {
		t.Fatal("undeclared normalization invoked")
	}
	home, _ = normalizeFixture(t, "cookie.normalize", "sleep 5 & wait")
	options.Backend = Provider{AdapterHome: home}
	options.Timeout = 30 * time.Millisecond
	start := time.Now()
	if _, err := browser.Normalize(context.Background(), options); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("timeout not enforced: %v", err)
	}
}
