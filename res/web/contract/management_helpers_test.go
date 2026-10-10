package contract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/extension"
)

func TestProgressRoundTrip(t *testing.T) {
	in := extension.InstallResult{Status: StatusAwaitingBrowserAction, Browser: "example", ID: "abc", Source: "/tmp/x", NextAction: "Do <this> & that"}
	line, err := ProgressLine(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(line, "\r\n") || !strings.HasPrefix(line, ProgressPrefix) {
		t.Fatalf("bad line %q", line)
	}
	for _, withNewline := range []string{line, line + "\n", line + "\r\n"} {
		out, ok := ParseProgress(withNewline)
		if !ok || out.Status != in.Status || out.ID != in.ID || out.NextAction != in.NextAction {
			t.Fatalf("parse %q: %+v %v", withNewline, out, ok)
		}
	}
}

func TestParseProgressIgnoresOtherLines(t *testing.T) {
	for _, line := range []string{
		"", "launching browser", "browser management progress:", ProgressPrefix, ProgressPrefix + "{", ProgressPrefix + "[]",
		ProgressPrefix + `{"browser":"x"}`, // no status
		" " + ProgressPrefix + `{"status":"installed"}`,
		ProgressPrefix + `{"status":"` + strings.Repeat("a", MaxProgressLineBytes) + `"}`,
	} {
		if _, ok := ParseProgress(line); ok {
			t.Errorf("accepted %.60q", line)
		}
	}
}

func TestAdapterProgressMatchesProtocol(t *testing.T) {
	// The prefix is a wire constant; changing it breaks hosts.
	if ProgressPrefix != "browser management progress: " {
		t.Fatal("progress prefix changed")
	}
	var buffer bytes.Buffer
	line, _ := ProgressLine(extension.InstallResult{Status: StatusInstalled})
	buffer.WriteString(line)
	var payload extension.InstallResult
	if err := json.Unmarshal(buffer.Bytes()[len(ProgressPrefix):], &payload); err != nil || payload.Status != "installed" {
		t.Fatalf("%v %+v", err, payload)
	}
}

func TestStatusBoundaryAndPersistence(t *testing.T) {
	if !Persistent(StatusInstalled) {
		t.Fatal("installed is persistent")
	}
	for _, status := range []string{StatusActivated, StatusPrepared, StatusStaged, StatusPackaged, StatusSigned, StatusRequested, StatusRequestRemoved, StatusPolicyUpdated, StatusAwaitingBrowserAction, StatusAwaitingBrowserConfirmation, "unknown", ""} {
		if Persistent(status) {
			t.Errorf("%q must not be persistent", status)
		}
	}
	activate := Request{Version: ManagementVersion, Kind: "extension", Action: "activate"}
	install := Request{Version: ManagementVersion, Kind: "extension", Action: "install"}
	if err := ValidateResponse(Response{Version: ManagementVersion, Kind: "extension", Action: "activate", Status: StatusInstalled}, activate); err == nil {
		t.Error("activate reported installed")
	}
	if err := ValidateResponse(Response{Version: ManagementVersion, Kind: "extension", Action: "install", Status: StatusActivated}, install); err == nil {
		t.Error("install reported activated")
	}
	if err := ValidateResponse(Response{Version: ManagementVersion, Kind: "extension", Action: "install", Status: StatusInstalled}, install); err != nil {
		t.Error(err)
	}
}

func TestSizeBoundsAreEnforced(t *testing.T) {
	if MaxRequestInputBytes != 8<<20 || MaxResultBytes != 16<<20 || MaxResponseBytes <= MaxResultBytes {
		t.Fatal("bounds changed")
	}
	big := `{"a":"` + strings.Repeat("x", MaxRequestInputBytes) + `"}`
	if err := ValidateRequest(Request{Version: ManagementVersion, Kind: "extension", Action: "prepare", Input: json.RawMessage(big)}); err == nil {
		t.Error("oversize input accepted")
	}
	request := Request{Version: ManagementVersion, Kind: "extension", Action: "prepare"}
	over := Response{Version: ManagementVersion, Kind: "extension", Action: "prepare", Status: StatusPrepared, Result: json.RawMessage(`"` + strings.Repeat("x", MaxResultBytes) + `"`)}
	if err := ValidateResponse(over, request); err == nil {
		t.Error("oversize result accepted")
	}
}
