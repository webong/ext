package guest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/contract"
	"github.com/webong/ext/res/web/extension"
)

type nativeExtensionFunc func(context.Context, string, string, json.RawMessage, func(extension.InstallResult) error) (any, string, error)

func (backend nativeExtensionFunc) ManageExtension(ctx context.Context, profile, action string, input json.RawMessage, progress func(extension.InstallResult) error) (any, string, error) {
	return backend(ctx, profile, action, input, progress)
}

func TestLocalManagementDelegatesNativeOperationsToUnknownAdapter(t *testing.T) {
	statuses := map[string]string{"targets": "ready", "capabilities": "ready", "sign": "signed", "install": "installed", "activate": "activated", "store_install": "requested", "store_remove": "request-removed"}
	for action, status := range statuses {
		t.Run(action, func(t *testing.T) {
			called := false
			native := nativeExtensionFunc(func(_ context.Context, profile, actual string, input json.RawMessage, progress func(extension.InstallResult) error) (any, string, error) {
				called = true
				var nativeInput map[string]string
				if err := json.Unmarshal(input, &nativeInput); err != nil {
					t.Fatal(err)
				}
				if profile != "Work" || actual != action || nativeInput["source"] != "native-package" || nativeInput["revision"] != "reviewed" || nativeInput["customOption"] != "adapter-owned" {
					t.Fatalf("adapter received profile=%q action=%q input=%+v", profile, actual, input)
				}
				result := extension.InstallResult{Status: status, Browser: "custom_provider"}
				return result, status, progress(result)
			})
			request, err := contract.NewRequest("extension", action, map[string]string{"source": "native-package", "revision": "reviewed", "customOption": "adapter-owned"})
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr strings.Builder
			code := RunLocalManagement(context.Background(), "custom_provider", "Work", strings.NewReader(string(data)), &stdout, &stderr, native)
			if code != 0 || !called {
				t.Fatalf("code=%d called=%v stderr=%s", code, called, stderr.String())
			}
			var response contract.Response
			if err := json.Unmarshal([]byte(stdout.String()), &response); err != nil || response.Status != status {
				t.Fatalf("response=%s error=%v", stdout.String(), err)
			}
			if !strings.Contains(stderr.String(), `"browser":"custom_provider"`) {
				t.Fatalf("adapter progress was not forwarded: %s", stderr.String())
			}
		})
	}
}

func TestLocalManagementRejectsNativeOperationWithoutAdapterBackend(t *testing.T) {
	for _, action := range []string{"targets", "capabilities", "sign", "install", "activate", "store_install", "store_remove"} {
		t.Run(action, func(t *testing.T) {
			input := `{"version":"1.0","kind":"extension","action":"` + action + `"}`
			var stdout, stderr strings.Builder
			code := RunLocalManagement(context.Background(), "custom_provider", "Work", strings.NewReader(input), &stdout, &stderr, nil)
			if code == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "no native extension backend") {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

type nativeInspector struct{ nativeExtensionFunc }

func (nativeInspector) InspectExtension(_ context.Context, source string) (extension.Description, error) {
	return extension.Description{Name: source, Revision: "native-revision"}, nil
}

func TestLocalManagementUsesAdapterInspectorForNativePackages(t *testing.T) {
	native := nativeInspector{nativeExtensionFunc: func(context.Context, string, string, json.RawMessage, func(extension.InstallResult) error) (any, string, error) {
		t.Fatal("preparation dispatched as a native operation")
		return nil, "", nil
	}}
	var stdout, stderr strings.Builder
	input := `{"version":"1.0","kind":"extension","action":"prepare","input":{"source":"provider-native-bundle"}}`
	code := RunLocalManagement(context.Background(), "custom_provider", "Work", strings.NewReader(input), &stdout, &stderr, native)
	if code != 0 || !strings.Contains(stdout.String(), `"revision":"native-revision"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
