//go:build darwin

package network

import (
	"context"
	"net"
	"syscall"
	"testing"
	"unsafe"
)

func nativeRouteFixture() []byte {
	h := syscall.RtMsghdr{Version: syscall.RTM_VERSION, Type: syscall.RTM_GET,
		Addrs: syscall.RTA_DST | syscall.RTA_GATEWAY | syscall.RTA_NETMASK}
	data := make([]byte, syscall.SizeofRtMsghdr)
	for _, value := range []string{"192.0.2.7", "192.0.2.1", "255.255.255.0"} {
		addr := make([]byte, 16)
		addr[0], addr[1] = 16, syscall.AF_INET
		copy(addr[4:8], net.ParseIP(value).To4())
		data = append(data, addr...)
	}
	h.Msglen = uint16(len(data))
	copy(data, unsafe.Slice((*byte)(unsafe.Pointer(&h)), syscall.SizeofRtMsghdr))
	return data
}

func TestParseNativeRoutes(t *testing.T) {
	data := nativeRouteFixture()
	got, partial := parseNativeRoutes(data, 10)
	if partial || len(got) != 1 || got[0].Destination != "192.0.2.0/24" || got[0].Gateway != "192.0.2.1" {
		t.Fatalf("routes = %#v, partial=%v", got, partial)
	}
	got, partial = parseNativeRoutes(append(data, data...), 1)
	if !partial || len(got) != 1 {
		t.Fatalf("limit: %#v, %v", got, partial)
	}
	for _, cut := range []int{1, syscall.SizeofRtMsghdr - 1, len(data) - 1} {
		if _, partial := parseNativeRoutes(data[:cut], 10); !partial {
			t.Fatalf("accepted truncation at %d", cut)
		}
	}
}

func TestNativeRouteObservation(t *testing.T) {
	routes, state, err := platformRoutes(context.Background(), 4096)
	if err != nil || state.State == "unavailable" || len(routes) == 0 {
		t.Fatalf("native routes=%d state=%#v err=%v", len(routes), state, err)
	}
}
