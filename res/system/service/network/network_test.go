package network

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNetworkContractIsBoundedAndDoesNotExposeRemoteActivity(t *testing.T) {
	options := DefaultOptions()
	if err := options.Validate(); err != nil {
		t.Fatal(err)
	}
	options.MaxRoutes = -1
	if err := options.Validate(); err == nil {
		t.Fatal("negative route limit was accepted")
	}
	snapshot := Snapshot{
		Platform: "fixture", ObservedAt: time.Now().UTC(),
		Interfaces: []Interface{}, Routes: []Route{},
		Resolver:        Resolver{Servers: []string{}, Search: []string{}, Options: []string{}},
		InterfaceStatus: CollectionStatus{State: "complete"},
		RouteStatus:     CollectionStatus{State: "unsupported", Reason: "unsupported"},
		ResolverStatus:  CollectionStatus{State: "unavailable", Reason: "no_device"},
	}
	for _, status := range []CollectionStatus{snapshot.InterfaceStatus, snapshot.RouteStatus, snapshot.ResolverStatus} {
		if err := status.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"request_body", "response_body", "credential", "connection_token"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("network contract exposed %q", forbidden)
		}
	}
}

func TestAddressClassification(t *testing.T) {
	cases := map[string]struct{ family, scope string }{
		"127.0.0.1/8":    {"ipv4", "loopback"},
		"10.0.0.2/24":    {"ipv4", "private"},
		"fe80::1/64":     {"ipv6", "link"},
		"2001:db8::1/64": {"ipv6", "global"},
	}
	for input, want := range cases {
		got, ok := parseAddress(input)
		if !ok || got.Family != want.family || got.Scope != want.scope {
			t.Fatalf("parseAddress(%q) = %+v, %v", input, got, ok)
		}
	}
}
