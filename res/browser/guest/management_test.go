package guest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webong/ext/res/browser/contract"
)

type fakeManagement struct{ called bool }

func (fake *fakeManagement) ManageBrowser(_ context.Context, profile string, request contract.Request) (contract.Response, error) {
	fake.called = true
	if profile != "Profile 1" {
		return contract.Response{}, context.Canceled
	}
	return contract.Response{Version: contract.ManagementVersion, Kind: request.Kind, Action: request.Action, Status: "prepared", Result: json.RawMessage(`{"revision":"sha256:test"}`)}, nil
}

func TestRunManagement(t *testing.T) {
	fake := &fakeManagement{}
	var stdout, stderr strings.Builder
	input := `{"version":"1.0","kind":"extension","action":"prepare","input":{"source":"addon.zip"}}`
	if code := RunManagement(context.Background(), "Profile 1", strings.NewReader(input), &stdout, &stderr, fake); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !fake.called || !strings.Contains(stdout.String(), `"status":"prepared"`) {
		t.Fatalf("called=%v output=%s", fake.called, stdout.String())
	}
}

func TestRunManagementRejectsBadRequestBeforeBackend(t *testing.T) {
	fake := &fakeManagement{}
	var stdout, stderr strings.Builder
	if code := RunManagement(context.Background(), "Profile 1", strings.NewReader(`{"version":"1.0","kind":"extension","action":"inject"}`), &stdout, &stderr, fake); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if fake.called {
		t.Fatal("backend called for invalid request")
	}
}
