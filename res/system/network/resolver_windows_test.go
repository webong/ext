//go:build windows

package network

import (
	"math/bits"
	"net"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// testFixedInfoBuffer builds a FIXED_INFO-shaped buffer with an embedded
// DnsServerList at the correct offset for the current pointer size, followed by
// the given IP_ADDR_STRING entries. Each entry's Next pointer points to the
// next entry's address within buf, or 0 for the last entry.
func testFixedInfoBuffer(domain string, entries [][16]byte) []byte {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))

	// Layout: HostName[132] + DomainName[132] + CurrentDnsServer ptr + entries
	bufSize := dnsServerListOffset + len(entries)*entrySize
	buf := make([]byte, bufSize)

	// Copy domain into DomainName field.
	domainBytes := []byte(domain)
	copy(buf[132:132+len(domainBytes)], domainBytes)

	// Write entries and set up next pointers.
	addresses := make([]uintptr, len(entries))
	bufPtr := unsafe.Pointer(&buf[0])
	for i, ipBytes := range entries {
		entryOffset := dnsServerListOffset + i*entrySize
		entryPtr := unsafe.Add(bufPtr, entryOffset)
		addresses[i] = uintptr(entryPtr)
		entry := (*networkIPAddrString)(entryPtr)
		copy(entry.ipAddress[:], ipBytes[:])
		if i+1 < len(entries) {
			entry.next = addresses[i+1]
		} else {
			entry.next = 0
		}
	}
	return buf
}

func TestPlatformResolverErrorCodes(t *testing.T) {
	if networkErrBufferOverflow != 111 {
		t.Fatalf("ERROR_BUFFER_OVERFLOW = %d, want 111", networkErrBufferOverflow)
	}
	if networkErrNotSupported != 50 {
		t.Fatalf("ERROR_NOT_SUPPORTED = %d, want 50", networkErrNotSupported)
	}
	if networkErrNoData != 232 {
		t.Fatalf("ERROR_NO_DATA = %d, want 232", networkErrNoData)
	}
	// procGetNetworkParams must be resolvable.
	if procGetNetworkParams == nil {
		t.Fatal("GetNetworkParams procedure is nil")
	}
	_ = windows.NewLazySystemDLL
}

func TestFixedInfoOffsets(t *testing.T) {
	domain, dnsServerList := fixedInfoOffsets()
	if domain != 132 {
		t.Fatalf("domain offset = %d, want 132", domain)
	}
	if bits.UintSize == 64 {
		if dnsServerList != 272 {
			t.Fatalf("dnsServerList offset (x64) = %d, want 272", dnsServerList)
		}
	} else {
		if dnsServerList != 268 {
			t.Fatalf("dnsServerList offset (x86) = %d, want 268", dnsServerList)
		}
	}
}

func TestFixedInfoDomain(t *testing.T) {
	buf := make([]byte, 300)
	copy(buf[132:], "example.com")
	if got := fixedInfoDomain(buf); got != "example.com" {
		t.Fatalf("domain = %q, want %q", got, "example.com")
	}

	buf = make([]byte, 100)
	if got := fixedInfoDomain(buf); got != "" {
		t.Fatalf("small buffer domain = %q, want empty", got)
	}
}

func TestDNSServersFromFixedInfo(t *testing.T) {
	ip1 := net.ParseIP("10.0.0.1").To4()
	ip2 := net.ParseIP("10.0.0.2").To4()
	ip3 := net.ParseIP("10.0.0.3").To4()
	buf := testFixedInfoBuffer("example.com", [][16]byte{
		{ip1[0], ip1[1], ip1[2], ip1[3]},
		{ip2[0], ip2[1], ip2[2], ip2[3]},
		{ip3[0], ip3[1], ip3[2], ip3[3]},
	})
	servers, domain := dnsServersFromFixedInfo(buf)
	if domain != "example.com" {
		t.Fatalf("domain = %q, want %q", domain, "example.com")
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %v, want 3", servers)
	}
	if servers[0] != "10.0.0.1" || servers[1] != "10.0.0.2" || servers[2] != "10.0.0.3" {
		t.Fatalf("servers = %v", servers)
	}
}

func TestDNSServersFromFixedInfoDeduplicates(t *testing.T) {
	ip := net.ParseIP("10.0.0.1").To4()
	buf := testFixedInfoBuffer("example.com", [][16]byte{
		{ip[0], ip[1], ip[2], ip[3]},
		{ip[0], ip[1], ip[2], ip[3]},
	})
	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) != 1 || servers[0] != "10.0.0.1" {
		t.Fatalf("deduplicated servers = %v, want [10.0.0.1]", servers)
	}
}

func TestDNSServersFromFixedInfoEmpty(t *testing.T) {
	buf := testFixedInfoBuffer("example.com", nil)
	servers, domain := dnsServersFromFixedInfo(buf)
	if len(servers) != 0 {
		t.Fatalf("servers = %v, want empty", servers)
	}
	if domain != "example.com" {
		t.Fatalf("domain = %q, want %q", domain, "example.com")
	}
}

func TestDNSServersFromFixedInfoBufferTooSmall(t *testing.T) {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	buf := make([]byte, dnsServerListOffset+entrySize-1)
	servers, domain := dnsServersFromFixedInfo(buf)
	if servers != nil {
		t.Fatalf("servers = %v, want nil for small buffer", servers)
	}
	if domain != "" {
		t.Fatalf("domain = %q, want empty for small buffer", domain)
	}
}

func TestDNSServersFromFixedInfoStopsAtCycle(t *testing.T) {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	bufSize := dnsServerListOffset + entrySize
	buf := make([]byte, bufSize)
	ip := net.ParseIP("10.0.0.1").To4()
	bufPtr := unsafe.Pointer(&buf[0])
	entry := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))
	copy(entry.ipAddress[:], ip[:])
	entry.next = uintptr(unsafe.Add(bufPtr, uintptr(dnsServerListOffset))) // self-cycle

	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) != 1 {
		t.Fatalf("cycle traversal returned %d servers, want 1", len(servers))
	}
	if servers[0] != "10.0.0.1" {
		t.Fatalf("cycle servers = %v", servers)
	}
}

func TestDNSServersFromFixedInfoRespectsBound(t *testing.T) {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	count := maxResolverEntries + 10
	bufSize := dnsServerListOffset + count*entrySize
	buf := make([]byte, bufSize)
	bufPtr := unsafe.Pointer(&buf[0])
	addresses := make([]uintptr, count)
	for i := 0; i < count; i++ {
		entryOffset := dnsServerListOffset + i*entrySize
		entryPtr := unsafe.Add(bufPtr, uintptr(entryOffset))
		addresses[i] = uintptr(entryPtr)
		entry := (*networkIPAddrString)(entryPtr)
		entry.ipAddress[0] = 10
		entry.ipAddress[1] = 0
		entry.ipAddress[2] = 0
		entry.ipAddress[3] = byte(i)
		if i+1 < count {
			entry.next = addresses[i+1]
		} else {
			entry.next = 0
		}
	}
	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) > maxResolverEntries {
		t.Fatalf("traversal did not respect bound: %d servers", len(servers))
	}
}

func TestNextPointerOutOfBoundsStopsTraversal(t *testing.T) {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	bufSize := dnsServerListOffset + entrySize
	buf := make([]byte, bufSize)
	ip := net.ParseIP("10.0.0.1").To4()
	bufPtr := unsafe.Pointer(&buf[0])
	entry := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))
	copy(entry.ipAddress[:], ip[:])
	entry.next = uintptr(unsafe.Add(bufPtr, uintptr(len(buf))+100)) // beyond buffer

	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) != 1 || servers[0] != "10.0.0.1" {
		t.Fatalf("out-of-bounds next = %v", servers)
	}
}

func TestCString16(t *testing.T) {
	if got := cString16([]byte{104, 101, 108, 108, 111, 0, 120}); got != "hello" {
		t.Fatalf("cString16 = %q, want %q", got, "hello")
	}
	if got := cString16([]byte{0}); got != "" {
		t.Fatalf("empty cString16 = %q, want empty", got)
	}
	if got := cString16([]byte{1, 2, 3}); got != string([]byte{1, 2, 3}) {
		t.Fatalf("no-null cString16 = %q", got)
	}
}

func TestPtrSizeMatchesArch(t *testing.T) {
	ptrSize := networkPtrSize()
	if bits.UintSize == 64 && ptrSize != 8 {
		t.Fatalf("x64 ptrSize = %d, want 8", ptrSize)
	}
	if bits.UintSize == 32 && ptrSize != 4 {
		t.Fatalf("x86 ptrSize = %d, want 4", ptrSize)
	}
}
