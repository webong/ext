//go:build darwin

package network

import (
	"testing"
)

const scutilOutput = `DNS configuration

resolver #1
  nameserver[0] : 192.168.100.1
  if_index : 13 (en0)
  flags    : Request A records
  reach    : 0x00020002 (Reachable,Directly Reachable Address)

resolver #2
  domain   : local
  options  : mdns
  timeout  : 5
  flags    : Request A records
  reach    : 0x00000000 (Not Reachable)
  order    : 300000

resolver #3
  nameserver[0] : 8.8.8.8
  nameserver[1] : 8.8.4.4
  domain   : example.com
  options  : mdns
`

func TestParseScutilDNS(t *testing.T) {
	servers, search, options, err := parseScutilDNS([]byte(scutilOutput))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %#v", servers)
	}
	if servers[0] != "192.168.100.1" || servers[1] != "8.8.8.8" || servers[2] != "8.8.4.4" {
		t.Fatalf("servers = %#v", servers)
	}
	if len(search) != 2 || search[0] != "local" || search[1] != "example.com" {
		t.Fatalf("search = %#v", search)
	}
	if len(options) != 2 || options[0] != "mdns" || options[1] != "mdns" {
		t.Fatalf("options = %#v", options)
	}
}

func TestParseScutilDNSEmpty(t *testing.T) {
	servers, search, options, err := parseScutilDNS([]byte("DNS configuration\n\nresolver #1\n  flags : Request A records\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 0 || len(search) != 0 || len(options) != 0 {
		t.Fatalf("expected empty: %#v %#v %#v", servers, search, options)
	}
}
