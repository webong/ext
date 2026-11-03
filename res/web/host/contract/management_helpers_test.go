package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

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
