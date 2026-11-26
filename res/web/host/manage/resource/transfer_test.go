package resource

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/webong/ext/res/web/host/contract"
)

func TestResourceTransferValidatesBeforeImport(t *testing.T) {
	called := false
	source := func(_ context.Context, kind, operation string, request contract.ResourceRequest, result any) error {
		if kind != "local_storage" || operation != "export" || request.Version != contract.Version || len(request.Args) != 1 {
			t.Fatalf("export request = %#v", request)
		}
		*result.(*contract.ResourceBundle) = contract.ResourceBundle{
			Version: contract.Version, Resource: kind, Payload: json.RawMessage(`{"key":"value"}`),
		}
		return nil
	}
	destination := func(_ context.Context, kind, operation string, request contract.ResourceRequest, _ any) error {
		if kind != "local_storage" || operation != "import" || request.Bundle == nil || request.Bundle.Source != "browser:profile" || !request.Replace {
			t.Fatalf("import request = %#v", request)
		}
		called = true
		return nil
	}
	bundle, err := Copy(context.Background(), source, destination, "local_storage", "browser:profile", []string{"site"}, true)
	if err != nil || !called || bundle.Source != "browser:profile" {
		t.Fatalf("copy = %#v, called=%t, %v", bundle, called, err)
	}
	called = false
	bad := func(_ context.Context, kind, operation string, request contract.ResourceRequest, result any) error {
		*result.(*contract.ResourceBundle) = contract.ResourceBundle{Version: contract.Version, Resource: "different", Payload: json.RawMessage(`{}`)}
		return nil
	}
	if _, err := Copy(context.Background(), bad, destination, "local_storage", "browser:profile", nil, false); err == nil || called {
		t.Fatalf("invalid export reached destination: %v", err)
	}
}
