package chromium

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	browser "github.com/webong/ctx/res/browser/contract"
)

func TestNativeNormalizeNeedsNoDiskProfileOrCredentials(t *testing.T) {
	for _, name := range []string{"chrome", "chromium", "edge", "brave", "arc", "atlas", "comet", "dia", "helium", "opera", "vivaldi", "whale"} {
		t.Run(name, func(t *testing.T) {
			payload := map[string]any{"selection": map[string]string{"browser": name, "profile": "export-only", "storeId": "0"}, "sites": []string{"https://example.test"}, "names": []string{"session"}, "partition": "unpartitioned", "cookies": []any{}}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			request, err := json.Marshal(browser.CookieRequest{Version: 2, StoreID: "0", NativeExport: encoded})
			if err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			if code := Run(Config{Name: name}, []string{"share", "export-only", "--", "cookie", "normalize"}, bytes.NewReader(request), &out, &diagnostics); code != 0 {
				t.Fatalf("native normalization: %d %s", code, diagnostics.String())
			}
			var response browser.CookieQueryResult
			if json.Unmarshal(out.Bytes(), &response) != nil || response.StoreID != "0" || response.Cookies == nil || len(response.Cookies) != 0 {
				t.Fatal("empty normalized result lost binding")
			}
			if Probe(Config{Name: name}, "export-only")["cookie.normalize"] != "ready" {
				t.Fatal("normalization incorrectly depends on disk profile")
			}
			out.Reset()
			diagnostics.Reset()
			if code := Run(Config{Name: "different"}, []string{"share", "export-only", "--", "cookie", "normalize"}, strings.NewReader(string(request)), &out, &diagnostics); code == 0 || out.Len() != 0 {
				t.Fatal("cross-browser binding accepted")
			}
		})
	}
}
