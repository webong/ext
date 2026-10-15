package guest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/contract"
	"github.com/webong/ext/res/web/extension"
)

const endpointSecret = "0f3c2b1a-9d4e-4a77-8c1d-5e6f7a8b9c0d"

type endpointBackend struct {
	result contract.EndpointResult
	err    error
	raw    json.RawMessage
}

func (b *endpointBackend) ManageExtension(context.Context, string, string, json.RawMessage, func(extension.InstallResult) error) (any, string, error) {
	return nil, "", errors.New("unused")
}

func (b *endpointBackend) Endpoint(_ context.Context, profile string, raw json.RawMessage) (contract.EndpointResult, error) {
	b.raw = raw
	if profile != "Default" {
		return contract.EndpointResult{}, errors.New("unknown profile")
	}
	return b.result, b.err
}

type plainBackend struct{}

func (plainBackend) ManageExtension(context.Context, string, string, json.RawMessage, func(extension.InstallResult) error) (any, string, error) {
	return nil, "", errors.New("unused")
}

func runEndpoint(t *testing.T, native NativeExtensionBackend, profile string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := RunLocalManagement(context.Background(), "example", profile, strings.NewReader(`{"version":"1.0","kind":"session","action":"endpoint"}`), &stdout, &stderr, native)
	return code, stdout.String(), stderr.String()
}

func TestSessionEndpointAnswersThroughTheBackend(t *testing.T) {
	backend := &endpointBackend{result: contract.EndpointResult{Protocol: "cdp", Endpoint: "ws://127.0.0.1:9222/devtools/browser/" + endpointSecret}}
	code, stdout, stderr := runEndpoint(t, backend, "Default")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var response contract.Response
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatal(err)
	}
	request := contract.Request{Version: contract.ManagementVersion, Kind: "session", Action: "endpoint"}
	if err := contract.ValidateResponse(response, request); err != nil || response.Status != contract.StatusReady {
		t.Fatalf("%+v %v", response, err)
	}
	var result contract.EndpointResult
	if err := json.Unmarshal(response.Result, &result); err != nil || result != backend.result {
		t.Fatalf("%+v %v", result, err)
	}
	// The secret reaches stdout only, never stderr.
	if strings.Contains(stderr, endpointSecret) {
		t.Fatalf("stderr leaks the endpoint: %s", stderr)
	}
}

func TestSessionEndpointFailuresNeverLeakTheEndpoint(t *testing.T) {
	// A backend that answers with a non-loopback endpoint is refused.
	bad := &endpointBackend{result: contract.EndpointResult{Protocol: "cdp", Endpoint: "ws://203.0.113.9:9222/devtools/browser/" + endpointSecret}}
	code, stdout, stderr := runEndpoint(t, bad, "Default")
	if code == 0 || stdout != "" || strings.Contains(stderr, endpointSecret) || strings.Contains(stderr, "203.0.113.9") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// A backend error is reported as is, and an adapter without the interface says so.
	code, _, stderr = runEndpoint(t, &endpointBackend{err: errors.New("no active debugging endpoint")}, "Default")
	if code == 0 || !strings.Contains(stderr, "no active debugging endpoint") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = runEndpoint(t, plainBackend{}, "Default")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "no endpoint route") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// Without any native backend there is no route either.
	code, _, stderr = runEndpoint(t, nil, "Default")
	if code == 0 || !strings.Contains(stderr, "no endpoint route") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
