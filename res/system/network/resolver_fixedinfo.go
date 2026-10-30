package network

import (
	"net"
	"unsafe"
)

// Windows SDK constants for FIXED_INFO.
const (
	networkMaxHostNameLen   = 128 // MAX_HOST_NAME_LEN
	networkMaxDomainNameLen = 128 // MAX_DOMAIN_NAME_LEN
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
	for offset, visited := dnsServerListOffset, 0; visited < maxResolverEntries && offset+int(entrySize) <= len(buf); visited++ {
		var entry networkIPAddrString
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&entry)), int(entrySize)), buf[offset:offset+int(entrySize)])
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
