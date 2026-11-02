package network

import (
	"fmt"
	"math/bits"
	"net"
	"testing"
	"unsafe"
)

// testFixedInfoBuffer builds a FIXED_INFO-shaped buffer with an embedded
// DnsServerList at the correct offset for the current pointer size, followed by
// the given IP_ADDR_STRING entries. Each entry's Next pointer points to the
// next entry's address within buf, or 0 for the last entry.
func testFixedInfoBuffer(domain string, entries []string) []byte {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))

	// Layout: HostName[132] + DomainName[132] + CurrentDnsServer ptr + entries
	bufSize := dnsServerListOffset + max(1, len(entries))*entrySize
	buf := make([]byte, bufSize)
	bufPtr := unsafe.Pointer(&buf[0])

	// Copy domain into DomainName field.
	domainBytes := []byte(domain)
	copy(buf[132:264], domainBytes)

	// Populate all entry addresses first, then write entries and links.
	// Writing entry.next before the target address is known leaves the first
	// link zero, which breaks multi-entry lists.
	addresses := make([]uintptr, len(entries))
	for i := range entries {
		entryOffset := dnsServerListOffset + i*entrySize
		addresses[i] = uintptr(unsafe.Add(bufPtr, uintptr(entryOffset)))
	}
	for i, ip := range entries {
		entryPtr := unsafe.Add(bufPtr, uintptr(dnsServerListOffset+i*entrySize))
		entry := (*networkIPAddrString)(entryPtr)
		copy(entry.ipAddress[:], ip)
		if i+1 < len(entries) {
			entry.next = addresses[i+1]
		} else {
			entry.next = 0
		}
	}
	return buf
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
	buf := testFixedInfoBuffer("example.com", []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"})
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
	buf := testFixedInfoBuffer("example.com", []string{"10.0.0.1", "10.0.0.1"})
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
	ip := net.ParseIP("10.0.0.1")
	bufPtr := unsafe.Pointer(&buf[0])
	entry := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))
	copy(entry.ipAddress[:], ip.String())
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
	entries := make([]string, maxResolverEntries+10)
	for i := range entries {
		entries[i] = fmt.Sprintf("10.0.0.%d", i+1)
	}
	buf := testFixedInfoBuffer("example.com", entries)
	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) != maxResolverEntries {
		t.Fatalf("traversal did not respect bound: %d servers", len(servers))
	}
}

func TestNextPointerOutOfBoundsStopsTraversal(t *testing.T) {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	bufSize := dnsServerListOffset + entrySize
	buf := make([]byte, bufSize)
	ip := net.ParseIP("10.0.0.1")
	bufPtr := unsafe.Pointer(&buf[0])
	entry := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))
	copy(entry.ipAddress[:], ip.String())
	entry.next = uintptr(bufPtr) + uintptr(len(buf)) + 100 // invalid native address, never dereferenced

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

func TestStatusClassification(t *testing.T) {
	// No servers => unavailable state.
	s := status("unavailable", "no servers")
	if s.State != "unavailable" || s.Detail != "no servers" {
		t.Fatalf("unavailable status = %#v", s)
	}
	// Complete state.
	s = status("complete", "")
	if s.State != "complete" || s.Detail != "" {
		t.Fatalf("complete status = %#v", s)
	}
}

func TestCleanResolverDeduplicatesAndPreservesOrder(t *testing.T) {
	got := cleanResolver([]string{"10.0.0.2", "10.0.0.1", "10.0.0.2", "10.0.0.3"})
	want := []string{"10.0.0.2", "10.0.0.1", "10.0.0.3"}
	if len(got) != len(want) {
		t.Fatalf("cleanResolver = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cleanResolver[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCleanResolverEmpty(t *testing.T) {
	got := cleanResolver(nil)
	if len(got) != 0 {
		t.Fatalf("cleanResolver(nil) = %v, want empty", got)
	}
}

func TestDNSServersFromFixedInfoMixedValidInvalid(t *testing.T) {
	buf := testFixedInfoBuffer("example.com", []string{"", "10.0.0.1", "invalid", "255.255.255.255"})
	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) != 2 {
		t.Fatalf("expected two valid servers, got %v", servers)
	}
	found := false
	for _, s := range servers {
		if s == "10.0.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("10.0.0.1 not in %v", servers)
	}
}

func TestDNSServersFromFixedInfoNextZeroStops(t *testing.T) {
	// Single entry with next=0 should stop after one entry.
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	bufSize := dnsServerListOffset + entrySize
	buf := make([]byte, bufSize)
	ip := net.ParseIP("10.0.0.1")
	bufPtr := unsafe.Pointer(&buf[0])
	entry := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))
	copy(entry.ipAddress[:], ip.String())
	entry.next = 0 // explicit end of list

	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) != 1 || servers[0] != "10.0.0.1" {
		t.Fatalf("single entry = %v", servers)
	}
}

func TestDNSServersFromFixedInfoNonForwardingPointerStops(t *testing.T) {
	// next pointer that doesn't move forward should stop traversal.
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	bufSize := dnsServerListOffset + 2*entrySize
	buf := make([]byte, bufSize)
	ip := net.ParseIP("10.0.0.1")
	bufPtr := unsafe.Pointer(&buf[0])
	entry0 := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))
	copy(entry0.ipAddress[:], ip.String())
	// next points to itself (not forward) - should stop.
	entry0.next = uintptr(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))

	servers, _ := dnsServersFromFixedInfo(buf)
	if len(servers) != 1 || servers[0] != "10.0.0.1" {
		t.Fatalf("non-forwarding pointer = %v", servers)
	}
}
