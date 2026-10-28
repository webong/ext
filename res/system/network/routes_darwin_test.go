//go:build darwin

package network

import (
	"testing"
)

const netstatOutput = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.100.1      UGSc                en0
127                127.0.0.1          UCS                 lo0
127.0.0.1          127.0.0.1          UH                  lo0
169.254            link#13            UCS                 en0 !
192.168.100        link#13            UCS                 en0 !
192.168.100.1/32   link#13            UCS                 en0
192.168.100.1      34:66:79:3:c7:e4   UHLWIir             en0 1187
224.0.0/4          link#13            UmCS                en0
255.255.255.255/32 link#13            UCS                 en0

Internet6:
Destination                             Gateway                                 Flags               Netif Expire
default                                 fe80::%utun0                          UGcIg               utun0
2001:db8::1                             fe80::1                               UGH                 utun1
ff02::1                                 link#13                               UmCI                  en0
`

func TestParseNetstatRoutes(t *testing.T) {
	routes, partial, err := parseNetstatRoutes([]byte(netstatOutput), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if partial {
		t.Fatal("unexpected partial route parse")
	}
	byDest := map[string]Route{}
	for _, r := range routes {
		byDest[r.Destination] = r
	}
	// Default IPv4 route.
	if r, ok := byDest["0.0.0.0/0"]; !ok || r.Gateway != "192.168.100.1" || r.Interface != "en0" || !r.Default {
		t.Fatalf("default route = %+v", byDest["0.0.0.0/0"])
	}
	// Host route with explicit /32.
	if r, ok := byDest["192.168.100.1/32"]; !ok || r.Gateway != "" {
		t.Fatalf("host route = %+v", byDest["192.168.100.1/32"])
	}
	// IPv4 multicast /4.
	if r, ok := byDest["224.0.0/4"]; !ok || r.Gateway != "" {
		t.Fatalf("multicast route = %+v", byDest["224.0.0/4"])
	}
	// Default IPv6 route.
	if r, ok := byDest["::/0"]; !ok || r.Gateway != "" || r.Interface != "utun0" {
		t.Fatalf("ipv6 default = %+v", byDest["::/0"])
	}
	// IPv6 host route.
	if r, ok := byDest["2001:db8::1/128"]; !ok || r.Gateway != "fe80::1" {
		t.Fatalf("ipv6 host = %+v", byDest["2001:db8::1/128"])
	}
	// Network destinations without an explicit prefix are skipped.
	if _, ok := byDest["192.168.100"]; ok {
		t.Fatal("network route without prefix should be skipped")
	}
	if _, ok := byDest["169.254"]; ok {
		t.Fatal("link-local network without prefix should be skipped")
	}
	if len(routes) == 0 {
		t.Fatal("no routes parsed")
	}
}

func TestRouteGatewayOnLink(t *testing.T) {
	if g := routeGateway("34:66:79:3:c7:e4"); g != "" {
		t.Fatalf("MAC gateway should be on-link: %q", g)
	}
	if g := routeGateway("link#13"); g != "" {
		t.Fatalf("link# gateway should be on-link: %q", g)
	}
	if g := routeGateway("192.168.100.1"); g != "192.168.100.1" {
		t.Fatalf("IP gateway = %q", g)
	}
	if g := routeGateway("*"); g != "" {
		t.Fatalf("star gateway = %q", g)
	}
}
