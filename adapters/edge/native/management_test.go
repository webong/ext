package main

import (
	"encoding/json"
	"strings"
	"testing"

	management "github.com/webong/ctx/res/browser/contract"
	"github.com/webong/ctx/res/browser/extension"
)

func TestManagementRequestReachesAdapterBackend(t *testing.T) {
	var stdout, stderr strings.Builder
	input := `{"version":"1.0","kind":"extension","action":"capabilities"}`
	code := run([]string{"share", "Default", "--", "management", "extension", "capabilities"}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var response management.Response
	if err := json.Unmarshal([]byte(stdout.String()), &response); err != nil {
		t.Fatal(err)
	}
	var capability extension.InstallCapability
	if err := json.Unmarshal(response.Result, &capability); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ready" || capability.Browser != "edge" {
		t.Fatalf("response=%+v capability=%+v", response, capability)
	}
}
