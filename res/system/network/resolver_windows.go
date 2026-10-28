//go:build windows

package network

import (
	"context"
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	networkIphlpapi      = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetNetworkParams = networkIphlpapi.NewProc("GetNetworkParams")
)

// MAX_HOST_NAME_LEN and MAX_DOMAIN_NAME_LEN are the Windows SDK limits for the
// FIXED_INFO structure used by GetNetworkParams. The native struct reserves 4
// spare bytes after each name field.
const (
	networkMaxHostNameLen   = 128
	networkMaxDomainNameLen = 128
)

// networkFixedInfoFieldOffsets describes the FIXED_INFO layout used to locate
// the domain name and the DnsServerList pointer without importing the full
// Windows SDK struct.
const (
	networkFixedInfoDomainNameOffset    = networkMaxHostNameLen + 4
	networkFixedInfoDnsServerListOffset = networkFixedInfoDomainNameOffset + (networkMaxDomainNameLen + 4)
)

type networkIPAddrString struct {
	next      *networkIPAddrString
	ipAddress [16]byte
	ipMask    [16]byte
	context   uint32
	_         [4]byte
}

// platformResolver observes the local resolver configuration on Windows
// through GetNetworkParams, a stable system-owned source. It performs no
// DNS lookup.
func platformResolver(ctx context.Context) (Resolver, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	var size uint32
	result, _, callErr := procGetNetworkParams.Call(0, uintptr(unsafe.Pointer(&size)))
	if result != 0 || size == 0 {
		return Resolver{Servers: []string{}, Search: []string{}, Options: []string{}},
			status("unsupported", fmt.Sprintf("GetNetworkParams is unavailable: %v", callErr)), nil
	}
	buf := make([]byte, int(size))
	result, _, callErr = procGetNetworkParams.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if result != 0 {
		return Resolver{Servers: []string{}, Search: []string{}, Options: []string{}},
			status("unsupported", fmt.Sprintf("GetNetworkParams failed: %v", callErr)), nil
	}
	servers := dnsServersFromFixedInfo(buf)
	search := []string{}
	if domain := fixedInfoDomain(buf); domain != "" {
		search = append(search, domain)
	}
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	state := "complete"
	if len(servers) == 0 {
		state = "unavailable"
	}
	return Resolver{Servers: cleanResolver(servers), Search: cleanResolver(search), Options: []string{}},
		status(state, ""), nil
}

// dnsServersFromFixedInfo walks the DnsServerList linked list embedded in the
// FIXED_INFO buffer and returns the configured DNS server addresses.
func dnsServersFromFixedInfo(buf []byte) []string {
	needed := int(networkFixedInfoDnsServerListOffset) + int(unsafe.Sizeof(networkIPAddrString{}))
	if len(buf) < needed {
		return nil
	}
	result := []string{}
	for current := (*networkIPAddrString)(unsafe.Pointer(&buf[networkFixedInfoDnsServerListOffset])); current != nil; current = current.next {
		ip := net.ParseIP(cString16(current.ipAddress[:]))
		if ip == nil {
			continue
		}
		result = append(result, ip.String())
	}
	return result
}

// fixedInfoDomain reads the DomainName field of FIXED_INFO, located immediately
// before DnsServerList.
func fixedInfoDomain(buf []byte) string {
	start := networkFixedInfoDomainNameOffset
	end := start + (networkMaxDomainNameLen + 4)
	if end > len(buf) {
		return ""
	}
	var field [networkMaxDomainNameLen + 4]byte
	copy(field[:], buf[start:end])
	return cString16(field[:])
}

func cString16(value []byte) string {
	n := 0
	for n < len(value) && value[n] != 0 {
		n++
	}
	return string(value[:n])
}
