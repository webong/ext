//go:build windows

package network

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	networkIphlpapi      = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetNetworkParams = networkIphlpapi.NewProc("GetNetworkParams")
)

// getNetworkParamsCall is the injectable seam for GetNetworkParams. Tests can
// replace it to exercise the complete two-call orchestration without invoking
// the real global procedure.
var getNetworkParamsCall = func(buf *byte, size *uint32) (result uintptr, callErr error) {
	r1, _, err := procGetNetworkParams.Call(uintptr(unsafe.Pointer(buf)), uintptr(unsafe.Pointer(size)))
	return r1, err
}

// Windows error codes returned by GetNetworkParams and related IP helper APIs.
const (
	networkErrBufferOverflow = 111 // ERROR_BUFFER_OVERFLOW
	networkErrNoData         = 232 // ERROR_NO_DATA
	networkErrNotSupported   = 50  // ERROR_NOT_SUPPORTED
)

// platformResolver observes the local resolver configuration on Windows
// through GetNetworkParams, a stable system-owned source. It performs no
// DNS lookup.
func platformResolver(ctx context.Context) (Resolver, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	var size uint32
	// First call: size probe with NULL buffer. GetNetworkParams returns
	// ERROR_BUFFER_OVERFLOW (111) directly in its return value; callErr is
	// not set for this expected path. Compare the result code, not callErr.
	result, callErr := getNetworkParamsCall(nil, &size)
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
	result, callErr = getNetworkParamsCall(&buf[0], &size)
	if result != 0 {
		// Report the returned Win32 result code, not callErr which may be
		// stale or zero.
		return Resolver{}, status("unavailable", fmt.Sprintf("GetNetworkParams failed: result=%d err=%v", result, callErr)), nil
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
