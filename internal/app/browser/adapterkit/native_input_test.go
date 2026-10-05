package adapterkit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webong/ctx/res/browser/extension"
)

func TestNativeInputRemainsOpaqueUntilAdapterReadsIt(t *testing.T) {
	called := false
	native := nativeExtensionFunc(func(_ context.Context, _, _ string, raw json.RawMessage, _ func(extension.InstallResult) error) (any, string, error) {
		called = true
		var input struct {
			Source struct {
				Bundle string `json:"bundle"`
			} `json:"source"`
			CustomOption struct {
				Schema int `json:"schema"`
			} `json:"customOption"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatal(err)
		}
		if input.Source.Bundle != "native-format" || input.CustomOption.Schema != 2 {
			t.Fatalf("native input=%s", raw)
		}
		return map[string]string{"artifact": input.Source.Bundle}, "signed", nil
	})
	input := `{"version":"1.0","kind":"extension","action":"sign","input":{"source":{"bundle":"native-format"},"customOption":{"schema":2}}}`
	var stdout, stderr strings.Builder
	code := RunLocalManagement(context.Background(), "custom_provider", "Work", strings.NewReader(input), &stdout, &stderr, native)
	if code != 0 || !called {
		t.Fatalf("code=%d called=%v stderr=%s", code, called, stderr.String())
	}
}
