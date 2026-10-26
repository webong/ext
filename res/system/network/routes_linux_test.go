//go:build linux

package network

import (
	"context"
	"strings"
	"testing"
)

func TestLinuxRouteParsers(t *testing.T) {
	ipv4 := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\neth0 00000000 010011AC 0003 0 0 100 00000000 0 0 0\neth0 000011AC 00000000 0001 0 0 100 0000FFFF 0 0 0\n"
	routes, err := readLinuxIPv4Routes(context.Background(), strings.NewReader(ipv4), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || !routes[0].Default || routes[0].Gateway != "172.17.0.1" || routes[1].Destination != "172.17.0.0/16" {
		t.Fatalf("unexpected IPv4 routes: %+v", routes)
	}
	ipv6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000064 00000000 00000000 00000001 eth0\n"
	routes, err = readLinuxIPv6Routes(context.Background(), strings.NewReader(ipv6), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || !routes[0].Default || routes[0].Gateway != "fe80::1" || routes[0].Metric != 100 {
		t.Fatalf("unexpected IPv6 route: %+v", routes)
	}
}
