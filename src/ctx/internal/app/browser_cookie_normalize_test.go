package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	browser "github.com/webong/ext/res/web"
)

func TestCookieNormalizeCLIContractAndPipe(t *testing.T) {
	root := cookieCLIAdapter(t, "cookie.normalize", `test "$1" = share && test "$2" = default && test "$4" = cookie && test "$5" = normalize || exit 2
cat > "$CTX_NORMALIZE_REQUEST"
printf '%s' '{"store_id":"0","cookies":[{"name":"session","value":"synthetic-secret","domain":"example.test","path":"/","attributes":{"fixture.webextension_store_id":"0"}}]}'`)
	capture := filepath.Join(root, "request")
	t.Setenv("CTX_NORMALIZE_REQUEST", capture)
	input := filepath.Join(root, "input.json")
	raw := `{"selection":{"browser":"fixture","profile":"default","storeId":"0"},"cookies":[]}`
	if err := os.WriteFile(input, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	base := []string{"share:browser", "cookie", "normalize", "--from", "fixture:default", "--store-id", "0", "--from-file", input}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	code := Run(append(base, "--stdout"), writer, &diagnostics)
	writer.Close()
	data, err := io.ReadAll(reader)
	reader.Close()
	if code != 0 || err != nil || strings.Contains(diagnostics.String(), "synthetic-secret") {
		t.Fatalf("normalize pipe: %d %v %s", code, err, diagnostics.String())
	}
	cookies, err := browser.ParseCookies(data)
	if err != nil || len(cookies) != 1 || cookies[0].SourceInfo.StoreID != "0" {
		t.Fatal("invalid pipe result")
	}
	request, _ := os.ReadFile(capture)
	var received struct {
		Export json.RawMessage `json:"native_export"`
		Store  string          `json:"store_id"`
	}
	if json.Unmarshal(request, &received) != nil || received.Store != "0" || string(received.Export) != raw {
		t.Fatal("native request not delivered")
	}
	var out bytes.Buffer
	redirect, err := os.Create(filepath.Join(root, "redirect.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer redirect.Close()
	if code := Run(append(base, "--stdout"), redirect, &diagnostics); code == 0 {
		t.Fatal("non-pipe output accepted")
	}
	if info, err := redirect.Stat(); err != nil || info.Size() != 0 {
		t.Fatal("failed output received cookie data")
	}
	for _, args := range [][]string{
		{"share:browser", "cookie", "normalize"},
		append(append([]string{}, base...), "--stdin", "--to-file", filepath.Join(root, "bad1")),
		append(append([]string{}, base...), "--stdout", "--to-file", filepath.Join(root, "bad2")),
	} {
		if code := Run(args, &out, &diagnostics); code != 2 {
			t.Fatal("ambiguous normalization arguments accepted")
		}
	}
}
