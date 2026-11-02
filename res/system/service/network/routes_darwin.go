//go:build darwin

package network

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Read the kernel routing MIB directly; no selectable command-line tool is
// invoked. Bounds apply before allocation, with limited retries for table growth.
func platformRoutes(ctx context.Context, limit int) ([]Route, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, err
	}
	mib := [6]int32{unix.CTL_NET, unix.AF_ROUTE, 0, unix.AF_UNSPEC, unix.NET_RT_DUMP, 0}
	for attempt := 0; attempt < 3; attempt++ {
		var size uintptr
		if err := routeSysctl(&mib, nil, &size); err != 0 || size > 16<<20 {
			return nil, status("unavailable", "route snapshot unavailable or exceeds byte bound"), nil
		}
		if size == 0 {
			return []Route{}, status("complete", ""), nil
		}
		data := make([]byte, int(size))
		if err := routeSysctl(&mib, &data[0], &size); err != 0 {
			if err == syscall.ENOMEM {
				continue
			}
			return nil, status("unavailable", fmt.Sprintf("route snapshot: %v", err)), nil
		}
		if size > uintptr(len(data)) {
			continue
		}
		routes, partial := parseNativeRoutes(data[:size], limit)
		if err := ctx.Err(); err != nil {
			return nil, CollectionStatus{}, err
		}
		if partial {
			return routes, status("partial", "route snapshot truncated, malformed, or limited"), nil
		}
		return routes, status("complete", ""), nil
	}
	return nil, status("unavailable", "route snapshot kept growing"), nil
}

func routeSysctl(mib *[6]int32, data *byte, size *uintptr) syscall.Errno {
	_, _, err := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(mib)), 6,
		uintptr(unsafe.Pointer(data)), uintptr(unsafe.Pointer(size)), 0, 0)
	return err
}

func parseNativeRoutes(data []byte, limit int) ([]Route, bool) {
	routes := []Route{}
	partial := false
	for len(data) > 0 {
		if len(data) < syscall.SizeofRtMsghdr {
			return routes, true
		}
		// Copy into an aligned Go value; kernel messages need only 4-byte alignment.
		var h syscall.RtMsghdr
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&h)), syscall.SizeofRtMsghdr), data)
		n := int(h.Msglen)
		if n < syscall.SizeofRtMsghdr || n > len(data) {
			return routes, true
		}
		message := data[:n]
		data = data[n:]
		if h.Version != syscall.RTM_VERSION {
			partial = true
			continue
		}
		// Construct the aligned header ourselves: ParseRoutingMessage casts
		// the 4-byte-aligned kernel payload to an 8-byte-aligned Go header.
		addrs, err := syscall.ParseRoutingSockaddr(&syscall.RouteMessage{Header: h, Data: message[syscall.SizeofRtMsghdr:]})
		if err != nil || len(addrs) <= syscall.RTAX_DST {
			partial = true
			continue
		}
		ip := routeIP(addrs[syscall.RTAX_DST])
		if ip == nil {
			continue
		}
		var mask net.IPMask
		if len(addrs) > syscall.RTAX_NETMASK && addrs[syscall.RTAX_NETMASK] != nil {
			mask = net.IPMask(routeIP(addrs[syscall.RTAX_NETMASK]))
		} else if h.Flags&syscall.RTF_HOST != 0 {
			mask = net.CIDRMask(len(ip)*8, len(ip)*8)
		} else if ip.IsUnspecified() {
			mask = net.CIDRMask(0, len(ip)*8)
		}
		if len(mask) != len(ip) {
			partial = true
			continue
		}
		ones, bits := mask.Size()
		if bits == 0 {
			partial = true
			continue
		}
		gateway := ""
		if len(addrs) > syscall.RTAX_GATEWAY {
			if gw := routeIP(addrs[syscall.RTAX_GATEWAY]); gw != nil {
				gateway = gw.String()
			}
		}
		name := ""
		if iface, err := net.InterfaceByIndex(int(h.Index)); err == nil {
			name = iface.Name
		}
		if len(routes) >= limit {
			partial = true
			continue
		}
		routes = append(routes, Route{Destination: (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String(),
			Gateway: gateway, Interface: name, Default: ones == 0})
	}
	return routes, partial
}

func routeIP(address syscall.Sockaddr) net.IP {
	switch a := address.(type) {
	case *syscall.SockaddrInet4:
		return net.IP(append([]byte(nil), a.Addr[:]...))
	case *syscall.SockaddrInet6:
		ip := net.IP(append([]byte(nil), a.Addr[:]...))
		if ip.IsLinkLocalUnicast() {
			ip[2], ip[3] = 0, 0
		}
		return ip
	}
	return nil
}
