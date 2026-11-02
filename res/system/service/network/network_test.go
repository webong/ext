package network

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDiscoverIsBoundedAndDoesNotExposeRemoteActivity(t *testing.T) {
	if _, err := Discover(context.Background(), Options{MaxRoutes: -1}); err == nil {
		t.Fatal("negative route limit was accepted")
	}
	snapshot, err := Discover(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Platform == "" || snapshot.InterfaceStatus.State == "" || snapshot.RouteStatus.State == "" || snapshot.ResolverStatus.State == "" {
		t.Fatalf("incomplete network observation: %+v", snapshot)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"request_body", "response_body", "credential", "connection_token"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("network observation exposed %q", forbidden)
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
