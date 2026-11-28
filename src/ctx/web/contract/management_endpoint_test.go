package contract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const secretPath = "/devtools/browser/0f3c2b1a-9d4e-4a77-8c1d-5e6f7a8b9c0d"

func TestEndpointIsAnAllowedSessionOperation(t *testing.T) {
	found := false
	for _, action := range Operations()["session"] {
		found = found || action == "endpoint"
	}
	if !found {
		t.Fatal("session.endpoint is not an allowed operation")
	}
	request, err := NewRequest("session", "endpoint", nil)
	if err != nil || request.Action != "endpoint" {
		t.Fatalf("%+v %v", request, err)
	}
	if _, err := NewRequest("extension", "endpoint", nil); err == nil {
		t.Fatal("endpoint accepted for another kind")
	}
}

func TestValidateEndpoint(t *testing.T) {
	valid := []EndpointResult{
		{"cdp", "ws://127.0.0.1:9222" + secretPath},
		{"cdp", "ws://[::1]:9222" + secretPath},
		{"cdp", "ws://localhost:9222" + secretPath},
		{"bidi", "ws://127.0.0.1:4444/session"},
		{"cdp", "wss://127.8.9.10:1" + secretPath}, // anywhere in 127/8 is loopback
	}
	for _, v := range valid {
		if err := ValidateEndpoint(v); err != nil {
			t.Errorf("%s rejected: %v", v, err)
		}
	}
	invalid := map[string]EndpointResult{
		"unknown protocol":    {"http", "ws://127.0.0.1:9222/x"},
		"empty protocol":      {"", "ws://127.0.0.1:9222/x"},
		"empty endpoint":      {"cdp", ""},
		"http scheme":         {"cdp", "http://127.0.0.1:9222/x"},
		"not a URL":           {"cdp", "::::"},
		"no host":             {"cdp", "ws:///x"},
		"no port":             {"cdp", "ws://127.0.0.1/x"},
		"userinfo":            {"cdp", "ws://user:pass@127.0.0.1:9222/x"},
		"query":               {"cdp", "ws://127.0.0.1:9222/x?token=1"},
		"empty query":         {"cdp", "ws://127.0.0.1:9222/x?"},
		"fragment":            {"cdp", "ws://127.0.0.1:9222/x#f"},
		"public host":         {"cdp", "ws://example.com:9222/x"},
		"public IP":           {"cdp", "ws://8.8.8.8:9222/x"},
		"private LAN IP":      {"cdp", "ws://192.168.1.5:9222/x"},
		"unspecified IP":      {"cdp", "ws://0.0.0.0:9222/x"},
		"localhost lookalike": {"cdp", "ws://localhost.example.com:9222/x"},
		"too long":            {"cdp", "ws://127.0.0.1:9222/" + strings.Repeat("a", MaxEndpointBytes)},
	}
	for name, v := range invalid {
		err := ValidateEndpoint(v)
		if err == nil {
			t.Errorf("%s accepted", name)
			continue
		}
		if v.Endpoint != "" && strings.Contains(err.Error(), v.Endpoint) {
			t.Errorf("%s: error contains the endpoint: %v", name, err)
		}
	}
}

func TestValidateResponseChecksEndpointResults(t *testing.T) {
	request := Request{Version: ManagementVersion, Kind: "session", Action: "endpoint"}
	respond := func(result string) Response {
		return Response{Version: ManagementVersion, Kind: "session", Action: "endpoint", Status: StatusReady, Result: json.RawMessage(result)}
	}
	if err := ValidateResponse(respond(`{"protocol":"cdp","endpoint":"ws://127.0.0.1:9222`+secretPath+`"}`), request); err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]string{
		"non-loopback": `{"protocol":"cdp","endpoint":"ws://203.0.113.9:9222` + secretPath + `"}`,
		"extra field":  `{"protocol":"cdp","endpoint":"ws://127.0.0.1:9222/x","token":"t"}`,
		"wrong shape":  `["ws://127.0.0.1:9222/x"]`,
		"no result":    ``,
		"oversize":     `{"protocol":"cdp","endpoint":"ws://127.0.0.1:9222/x","pad":"` + strings.Repeat("a", MaxEndpointResultBytes) + `"}`,
	} {
		err := ValidateResponse(respond(result), request)
		if err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(err.Error(), "203.0.113.9") || strings.Contains(err.Error(), secretPath) {
			t.Errorf("%s: error leaks the endpoint: %v", name, err)
		}
	}
	// Other session actions are unaffected by the endpoint rules.
	other := Request{Version: ManagementVersion, Kind: "session", Action: "targets"}
	if err := ValidateResponse(Response{Version: ManagementVersion, Kind: "session", Action: "targets", Status: StatusReady, Result: json.RawMessage(`[]`)}, other); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointRedaction(t *testing.T) {
	result := EndpointResult{Protocol: "cdp", Endpoint: "ws://127.0.0.1:9222" + secretPath}
	for _, rendered := range []string{
		result.String(), fmt.Sprintf("%v", result), fmt.Sprintf("%+v", result), fmt.Sprintf("%#v", result), fmt.Sprint(result), fmt.Sprintf("%s", result),
		fmt.Sprintf("%v", &result), fmt.Sprintf("%+v", []EndpointResult{result}), fmt.Sprintf("%v", map[string]EndpointResult{"k": result}),
	} {
		if strings.Contains(rendered, "0f3c2b1a") || strings.Contains(rendered, "/devtools/") {
			t.Errorf("formatted value leaks the endpoint: %s", rendered)
		}
	}
	if got := RedactEndpoint(result.Endpoint); got != "ws://127.0.0.1:9222/<redacted>" {
		t.Fatalf("redaction: %q", got)
	}
	if RedactEndpoint("not a url") != "<redacted>" || RedactEndpoint("") != "<redacted>" {
		t.Fatal("unparsable values must be fully redacted")
	}
	// The wire form keeps the real value.
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), "0f3c2b1a") {
		t.Fatalf("wire form lost the endpoint: %s %v", encoded, err)
	}
}
