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

// Windows SDK constants for FIXED_INFO.
const (
	networkMaxHostNameLen   = 128 // MAX_HOST_NAME_LEN
	networkMaxDomainNameLen = 128 // MAX_DOMAIN_NAME_LEN
)

// Windows error codes returned by GetNetworkParams and related IP helper APIs.
const (
	networkErrBufferOverflow = 111 // ERROR_BUFFER_OVERFLOW
	networkErrNoData         = 232 // ERROR_NO_DATA
	networkErrNotSupported   = 50  // ERROR_NOT_SUPPORTED
)

// maxResolverEntries bounds the number of DnsServerList entries walked, so a
// corrupt linked list cannot cause unbounded traversal.
const maxResolverEntries = 64

// networkIPAddrString matches the Windows SDK IP_ADDR_STRING structure. The
// Next field is a raw uintptr rather than a Go pointer, so no Go pointer is
// retained in native memory and the garbage collector cannot scan it.
type networkIPAddrString struct {
	next      uintptr
	ipAddress [16]byte
	ipMask    [16]byte
	context   uint32
}

// fixedInfoOffsets returns the offsets of DomainName and DnsServerList within
// FIXED_INFO for the current pointer size. FIXED_INFO lays out as:
//
//	HostName[132] (offset 0)
//	DomainName[132] (offset 132)
//	CurrentDnsServer pointer (offset 264, 4 bytes x86 / 8 bytes x64)
//	DnsServerList embedded IP_ADDR_STRING (offset 268 x86 / 272 x64)
func fixedInfoOffsets() (domainName, dnsServerList int) {
	domainName = networkMaxHostNameLen + 4 // 132
	dnsServerList = domainName + (networkMaxDomainNameLen + 4) + networkPtrSize()
	return
}

func networkPtrSize() int {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return 8
	}
	return 4
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
	// The first call with a NULL buffer is a size probe. GetNetworkParams
	// returns ERROR_BUFFER_OVERFLOW (111) directly in its return value during
	// the normal size probe; callErr is not set for this expected path.
	// Compare result to networkErrBufferOverflow, not callErr.
	if result != 0 {
		if errno, ok := callErr.(windows.Errno); ok && errno == networkErrBufferOverflow && size > 0 {
			// Proceed with the allocated buffer.
		} else if uintptr(result) != uintptr(networkErrBufferOverflow) || size == 0 {
			return Resolver{}, status("unavailable", fmt.Sprintf("GetNetworkParams size probe failed: result=%d err=%v", result, callErr)), nil
		}
	}
	if size == 0 {
		return Resolver{}, status("unavailable", "GetNetworkParams returned zero size"), nil
	}
	buf := make([]byte, int(size))
	result, _, callErr = procGetNetworkParams.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if result != 0 {
		return Resolver{}, status("unavailable", fmt.Sprintf("GetNetworkParams failed: %v", callErr)), nil
	}
	servers, domain := dnsServersFromFixedInfo(buf)
	search := []string{}
	if domain != "" {
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

// dnsServersFromFixedInfo walks the embedded DnsServerList in FIXED_INFO and
// returns the configured DNS server addresses and the primary DNS domain name.
// Traversal is bounded and cycle-safe: native next pointers are validated to
// remain within the buffer and to move forward, and iteration stops after
// maxResolverEntries entries.
func dnsServersFromFixedInfo(buf []byte) ([]string, string) {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := unsafe.Sizeof(networkIPAddrString{})
	needed := dnsServerListOffset + int(entrySize)
	if len(buf) < needed {
		return nil, ""
	}
	domain := fixedInfoDomain(buf)
	result := []string{}
	seen := map[string]bool{}
	bufPtr := unsafe.Pointer(&buf[0])
	for offset := dnsServerListOffset; offset+int(entrySize) <= len(buf); {
		entry := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(offset)))
		ip := net.ParseIP(cString16(entry.ipAddress[:]))
		if ip != nil && !seen[ip.String()] {
			seen[ip.String()] = true
			result = append(result, ip.String())
		}
		if entry.next == 0 {
			break
		}
		// next is a native address; validate it points within buf and moves
		// forward to prevent walking a corrupt or cyclic list.
		nextOffset := int(entry.next) - int(uintptr(bufPtr))
		if nextOffset < 0 || nextOffset >= len(buf) || nextOffset <= offset {
			break
		}
		offset = nextOffset
		if len(result) >= maxResolverEntries {
			break
		}
	}
	return result, domain
}

// fixedInfoDomain reads the DomainName field of FIXED_INFO, located immediately
// before the CurrentDnsServer pointer.
func fixedInfoDomain(buf []byte) string {
	domainOffset := networkMaxHostNameLen + 4 // 132
	end := domainOffset + (networkMaxDomainNameLen + 4)
	if end > len(buf) {
		return ""
	}
	var field [networkMaxDomainNameLen + 4]byte
	copy(field[:], buf[domainOffset:end])
	return cString16(field[:])
}

func cString16(value []byte) string {
	n := 0
	for n < len(value) && value[n] != 0 {
		n++
	}
	return string(value[:n])
}
