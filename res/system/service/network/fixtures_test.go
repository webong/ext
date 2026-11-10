package network

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/webong/ext/res/system/internal/fixtures"
)

type networkCases struct {
	Options []struct {
		Name          string  `json:"name"`
		MaxInterfaces int     `json:"maxInterfaces"`
		MaxAddresses  int     `json:"maxAddresses"`
		MaxRoutes     int     `json:"maxRoutes"`
		Code          *string `json:"code"`
	} `json:"options"`
	Addresses []struct {
		Name   string  `json:"name"`
		IP     string  `json:"ip"`
		Prefix int     `json:"prefix"`
		Code   *string `json:"code"`
		Want   *struct {
			IP           string `json:"ip"`
			PrefixLength int    `json:"prefixLength"`
			Family       string `json:"family"`
			Scope        string `json:"scope"`
		} `json:"want"`
	} `json:"addresses"`
	Resolver []struct {
		Name    string `json:"name"`
		Servers []string
		Search  []string
		Options []string
		Want    struct {
			Servers []string `json:"servers"`
			Search  []string `json:"search"`
			Options []string `json:"options"`
		} `json:"want"`
	} `json:"resolver"`
}

type capabilityCases struct {
	States  []string `json:"states"`
	Reasons []string `json:"reasons"`
	Cases   []struct {
		State  string  `json:"state"`
		Reason *string `json:"reason"`
		Code   *string `json:"code"`
	} `json:"cases"`
}

func expectCode(t *testing.T, codes map[string]bool, want *string, err error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		return
	}
	if !codes[*want] {
		t.Fatalf("code %q is not in testdata/codes.json", *want)
	}
	if err == nil {
		t.Fatalf("accepted, want %s", *want)
	}
	if got := Code(err); got != *want {
		t.Fatalf("code %q (%v), want %s", got, err, *want)
	}
}

// TestFixtures runs testdata/network/v1 and testdata/capability/v1, which the Swift and Java
// bindings run as well.
func TestFixtures(t *testing.T) {
	var cases networkCases
	fixtures.Load(t, "network", 1, &cases)
	codes := fixtures.Codes(t)
	if len(cases.Options) == 0 || len(cases.Addresses) == 0 || len(cases.Resolver) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases.Options {
		t.Run("options/"+c.Name, func(t *testing.T) {
			_, err := normalizeOptions(Options{MaxInterfaces: c.MaxInterfaces, MaxAddresses: c.MaxAddresses, MaxRoutes: c.MaxRoutes})
			expectCode(t, codes, c.Code, err)
		})
	}
	for _, c := range cases.Addresses {
		t.Run("addresses/"+c.Name, func(t *testing.T) {
			got, ok := parseAddress(fmt.Sprintf("%s/%d", c.IP, c.Prefix))
			if c.Code != nil {
				if ok {
					t.Fatalf("accepted %+v, want %s", got, *c.Code)
				}
				return
			}
			want := Address{IP: c.Want.IP, PrefixLength: c.Want.PrefixLength, Family: c.Want.Family, Scope: c.Want.Scope}
			if !ok || got != want {
				t.Fatalf("%+v (%v), want %+v", got, ok, want)
			}
		})
	}
	for _, c := range cases.Resolver {
		t.Run("resolver/"+c.Name, func(t *testing.T) {
			for name, pair := range map[string][2][]string{"servers": {c.Servers, c.Want.Servers}, "search": {c.Search, c.Want.Search}, "options": {c.Options, c.Want.Options}} {
				got := cleanResolver(pair[0])
				if !reflect.DeepEqual(got, append([]string{}, pair[1]...)) {
					t.Fatalf("%s %q, want %q", name, got, pair[1])
				}
			}
		})
	}
	var capability capabilityCases
	fixtures.Load(t, "capability", 1, &capability)
	for _, c := range capability.Cases {
		name := c.State
		reason := ""
		if c.Reason != nil {
			reason = *c.Reason
			name += "+" + reason
		}
		t.Run("capability/"+name, func(t *testing.T) {
			expectCode(t, codes, c.Code, CollectionStatus{State: c.State, Reason: reason}.Validate())
		})
	}
}
