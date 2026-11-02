//go:build windows

package network

import (
	"context"
	"fmt"
	"net"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

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

// getNetworkParamsMock is an injectable mock for the GetNetworkParams seam.
type getNetworkParamsMock struct {
	firstResult  uintptr
	firstSize    uint32
	firstErr     error
	secondResult uintptr
	secondSize   uint32
	secondErr    error
	callCount    int
	buf          []byte
}

func (m *getNetworkParamsMock) Call(buf *byte, size *uint32) (uintptr, error) {
	m.callCount++
	if m.callCount == 1 {
		*size = m.firstSize
		return m.firstResult, m.firstErr
	}
	// Second call: copy the fixed-info buffer into the provided buffer.
	if m.buf != nil && buf != nil {
		copy(unsafe.Slice(buf, int(*size)), m.buf)
		*size = m.secondSize
	}
	return m.secondResult, m.secondErr
}

// buildFixedInfoBuffer builds a minimal FIXED_INFO buffer with one DNS server.
func buildFixedInfoBuffer(domain string, ip net.IP) []byte {
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	bufSize := dnsServerListOffset + entrySize
	buf := make([]byte, bufSize)
	bufPtr := unsafe.Pointer(&buf[0])
	copy(buf[132:132+len(domain)], domain)
	entry := (*networkIPAddrString)(unsafe.Add(bufPtr, uintptr(dnsServerListOffset)))
	copy(entry.ipAddress[:], ip.String())
	entry.next = 0
	return buf
}

func TestPlatformResolverNormalSizeProbe(t *testing.T) {
	orig := getNetworkParamsCall
	defer func() { getNetworkParamsCall = orig }()
	ip := net.ParseIP("10.0.0.1")
	fixedInfo := buildFixedInfoBuffer("example.com", ip)
	mock := &getNetworkParamsMock{
		firstResult:  networkErrBufferOverflow,
		firstSize:    uint32(len(fixedInfo)),
		secondResult: 0,
		secondSize:   uint32(len(fixedInfo)),
		buf:          fixedInfo,
	}
	getNetworkParamsCall = mock.Call

	resolver, status, err := platformResolver(context.Background())
	if err != nil {
		t.Fatalf("platformResolver = %v", err)
	}
	if status.State != "complete" {
		t.Fatalf("status = %#v", status)
	}
	if len(resolver.Servers) != 1 || resolver.Servers[0] != "10.0.0.1" {
		t.Fatalf("servers = %v", resolver.Servers)
	}
	if mock.callCount != 2 {
		t.Fatalf("call count = %d, want 2", mock.callCount)
	}
}

func TestPlatformResolverZeroSizeProbe(t *testing.T) {
	orig := getNetworkParamsCall
	defer func() { getNetworkParamsCall = orig }()
	mock := &getNetworkParamsMock{
		firstResult: networkErrBufferOverflow,
		firstSize:   0,
		firstErr:    nil,
	}
	getNetworkParamsCall = mock.Call

	resolver, status, err := platformResolver(context.Background())
	if err != nil {
		t.Fatalf("platformResolver = %v", err)
	}
	if status.State != "unavailable" {
		t.Fatalf("status = %#v, want unavailable", status)
	}
	if len(resolver.Servers) != 0 {
		t.Fatalf("servers = %v, want empty", resolver.Servers)
	}
	if mock.callCount != 1 {
		t.Fatalf("call count = %d, want 1", mock.callCount)
	}
}

func TestPlatformResolverUnexpectedProbeError(t *testing.T) {
	orig := getNetworkParamsCall
	defer func() { getNetworkParamsCall = orig }()
	mock := &getNetworkParamsMock{
		firstResult: networkErrNotSupported,
		firstSize:   0,
		firstErr:    fmt.Errorf("access denied"),
	}
	getNetworkParamsCall = mock.Call

	resolver, status, err := platformResolver(context.Background())
	if err != nil {
		t.Fatalf("platformResolver = %v", err)
	}
	if status.State != "unavailable" {
		t.Fatalf("status = %#v, want unavailable", status)
	}
	if mock.callCount != 1 {
		t.Fatalf("call count = %d, want 1 (should not make second call)", mock.callCount)
	}
	_ = resolver
}

func TestPlatformResolverSecondCallError(t *testing.T) {
	orig := getNetworkParamsCall
	defer func() { getNetworkParamsCall = orig }()
	mock := &getNetworkParamsMock{
		firstResult:  networkErrBufferOverflow,
		firstSize:    100,
		firstErr:     nil,
		secondResult: 1, // ERROR_INVALID_FUNCTION
		secondErr:    fmt.Errorf("call failed"),
	}
	getNetworkParamsCall = mock.Call

	resolver, status, err := platformResolver(context.Background())
	if err != nil {
		t.Fatalf("platformResolver = %v", err)
	}
	if status.State != "unavailable" {
		t.Fatalf("status = %#v, want unavailable", status)
	}
	if mock.callCount != 2 {
		t.Fatalf("call count = %d, want 2", mock.callCount)
	}
	_ = resolver
}

func TestPlatformResolverNoServers(t *testing.T) {
	orig := getNetworkParamsCall
	defer func() { getNetworkParamsCall = orig }()
	// Buffer with domain but an empty embedded DnsServerList entry. Allocate
	// the fixed-info prefix plus a complete zero-valued IP_ADDR_STRING so the
	// parser sees a complete entry with next == 0.
	_, dnsServerListOffset := fixedInfoOffsets()
	entrySize := int(unsafe.Sizeof(networkIPAddrString{}))
	bufSize := dnsServerListOffset + entrySize
	buf := make([]byte, bufSize)
	copy(buf[132:], "example.com")

	mock := &getNetworkParamsMock{
		firstResult:  networkErrBufferOverflow,
		firstSize:    uint32(len(buf)),
		secondResult: 0,
		secondSize:   uint32(len(buf)),
		buf:          buf,
	}
	getNetworkParamsCall = mock.Call

	resolver, status, err := platformResolver(context.Background())
	if err != nil {
		t.Fatalf("platformResolver = %v", err)
	}
	if status.State != "unavailable" {
		t.Fatalf("status = %#v, want unavailable", status)
	}
	if len(resolver.Servers) != 0 {
		t.Fatalf("servers = %v, want empty", resolver.Servers)
	}
	if len(resolver.Search) != 1 || resolver.Search[0] != "example.com" {
		t.Fatalf("search = %v, want [example.com]", resolver.Search)
	}
}
