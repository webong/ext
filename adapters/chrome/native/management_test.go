package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	management "github.com/webong/ext/res/web/contract"
	"github.com/webong/ext/res/web/extension"
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
	if response.Status != "ready" || capability.Browser != "chrome" {
		t.Fatalf("response=%+v capability=%+v", response, capability)
	}
}

func TestSessionEndpointReachesAdapterBackendAndIsDeclared(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "session.endpoint") {
		t.Fatal("plugin.json does not declare session.endpoint")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connection.Close()
		}
	}()
	const guid = "0f3c2b1a-9d4e-4a77-8c1d-5e6f7a8b9c0d"
	port := listener.Addr().(*net.TCPAddr).Port
	profile := t.TempDir()
	marker := fmt.Sprintf("%d\n/devtools/browser/%s\n", port, guid)
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	input := `{"version":"1.0","kind":"session","action":"endpoint"}`
	code := run([]string{"share", profile, "--", "management", "session", "endpoint"}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), guid) {
		t.Fatal("stderr leaks the endpoint")
	}
	var response management.Response
	if err := json.Unmarshal([]byte(stdout.String()), &response); err != nil {
		t.Fatal(err)
	}
	request := management.Request{Version: management.ManagementVersion, Kind: "session", Action: "endpoint"}
	if err := management.ValidateResponse(response, request); err != nil || response.Status != management.StatusReady {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	var result management.EndpointResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Protocol != "cdp" || result.Endpoint != fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser/%s", port, guid) {
		t.Fatalf("unexpected endpoint %v", result)
	}
	// A profile without a marker fails with text that carries no endpoint.
	stdout.Reset()
	stderr.Reset()
	empty := t.TempDir()
	if code := run([]string{"share", empty, "--", "management", "session", "endpoint"}, strings.NewReader(input), &stdout, &stderr); code == 0 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}
