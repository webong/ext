//go:build linux && cgo

package main

/*
#cgo CFLAGS: -D_GNU_SOURCE -I${SRCDIR}/../../../../../../pkg/ext-linux/net/include
#include <net/if.h>
#include "ext_system_net.h"
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
	"unsafe"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "network guest:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 || os.Args[1] != "run" {
		return plugin.ErrInvalid
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	file, err := os.Open(binary)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	ref := plugin.ContractRef{Name: "ext.system.network", Version: "v1"}
	descriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity: plugin.Identity{ID: "system.network.linux",
			Revision: hex.EncodeToString(hash.Sum(nil)), Version: "1.0.0"},
		Contracts: []plugin.Contract{{
			ContractRef: ref,
			Operations:  []plugin.Operation{{Name: "observe", Surface: "observation"}},
		}},
	}
	guest, err := jsonline.NewFuncGuest(descriptor,
		func(ctx context.Context, method string, input json.RawMessage) (any, error) {
			if method != "ext.system.network.observe" {
				return nil, plugin.ErrUnsupported
			}
			var request struct {
				MaxInterfaces int `json:"max_interfaces"`
				MaxAddresses  int `json:"max_addresses"`
				MaxRoutes     int `json:"max_routes"`
				TimeoutMS     int `json:"timeout_ms"`
			}
			if err := json.Unmarshal(input, &request); err != nil {
				return nil, plugin.ErrInvalid
			}
			if request.MaxInterfaces == 0 || request.MaxAddresses == 0 ||
				request.MaxRoutes == 0 || request.TimeoutMS == 0 {
				return nil, plugin.ErrInvalid
			}
			return observe(ctx, request.MaxInterfaces, request.MaxAddresses,
				request.MaxRoutes, request.TimeoutMS)
		}, jsonline.FuncOptions{})
	if err != nil {
		return err
	}
	return jsonline.ServeStdio(context.Background(), guest)
}

func observe(ctx context.Context, maxInterfaces, maxAddresses, maxRoutes, timeoutMS int) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxInterfaces < 1 || maxAddresses < 1 || maxRoutes < 1 ||
		timeoutMS < 1 || timeoutMS > int(time.Hour/time.Millisecond) {
		return nil, plugin.ErrInvalid
	}
	options := C.ext_system_net_options{
		max_interfaces: C.uint32_t(maxInterfaces),
		max_addresses:  C.uint32_t(maxAddresses),
		max_routes:     C.uint32_t(maxRoutes),
		timeout_ms:     C.uint32_t(timeoutMS),
	}
	if C.ext_system_net_validate_options(options) != 0 {
		return nil, plugin.ErrInvalid
	}
	var native *C.ext_system_net_snapshot
	switch code := C.ext_system_net_observe(options, &native); code {
	case 0:
	case -1:
		return nil, plugin.ErrInvalid
	case -2:
		return nil, context.DeadlineExceeded
	default:
		return nil, fmt.Errorf("native network observation failed: %d", int(code))
	}
	defer C.ext_system_net_free(native)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result := map[string]any{
		"platform": "linux", "observed_at": now,
		"interface_status": status(native.interface_state, now),
		"route_status":     status(native.route_state, now),
		"resolver_status":  status(native.resolver_state, now),
	}
	interfaces := unsafe.Slice(native.interfaces, int(native.interface_count))
	addresses := unsafe.Slice(native.addresses, int(native.address_count))
	interfaceRecords := make([]any, 0, len(interfaces))
	for _, item := range interfaces {
		flags := []string{}
		for _, flag := range []struct {
			bit  C.uint
			name string
		}{
			{C.IFF_UP, "up"}, {C.IFF_BROADCAST, "broadcast"},
			{C.IFF_LOOPBACK, "loopback"}, {C.IFF_POINTOPOINT, "point-to-point"},
			{C.IFF_MULTICAST, "multicast"}, {C.IFF_RUNNING, "running"},
		} {
			if C.uint(item.flags)&flag.bit != 0 {
				flags = append(flags, flag.name)
			}
		}
		first, end := int(item.address_first), int(item.address_first+item.address_count)
		if first < 0 || end > len(addresses) {
			return nil, fmt.Errorf("native address bounds are invalid")
		}
		addressRecords := make([]any, 0, int(item.address_count))
		for _, address := range addresses[first:end] {
			family := "ipv4"
			if address.family == 6 {
				family = "ipv6"
			}
			addressRecords = append(addressRecords, map[string]any{
				"ip": C.GoString(&address.ip[0]), "prefix_length": int(address.prefix_length),
				"family": family, "scope": C.GoString(&address.scope[0]),
			})
		}
		interfaceRecords = append(interfaceRecords, map[string]any{
			"index": int(item.index), "name": C.GoString(&item.name[0]),
			"mtu": int(item.mtu), "hardware_address": C.GoString(&item.hardware_address[0]),
			"flags": flags, "addresses": addressRecords,
		})
	}
	result["interfaces"] = interfaceRecords
	routeRecords := make([]any, 0, int(native.route_count))
	for _, route := range unsafe.Slice(native.routes, int(native.route_count)) {
		routeRecords = append(routeRecords, map[string]any{
			"destination": C.GoString(&route.destination[0]),
			"gateway":     C.GoString(&route.gateway[0]),
			"interface":   C.GoString(&route.interface_name[0]),
			"metric":      int(route.metric), "default": route.is_default != 0,
		})
	}
	result["routes"] = routeRecords
	result["resolver"] = map[string]any{
		"servers": strings256(native.servers, native.server_count),
		"search":  strings256(native.search, native.search_count),
		"options": strings256(native.resolver_options, native.resolver_option_count),
	}
	return result, nil
}

func status(state C.ext_system_net_state, now time.Time) map[string]any {
	name := "unavailable"
	switch state {
	case C.EXT_SYSTEM_NET_COMPLETE:
		name = "complete"
	case C.EXT_SYSTEM_NET_PARTIAL:
		name = "partial"
	case C.EXT_SYSTEM_NET_UNSUPPORTED:
		name = "unsupported"
	}
	value := map[string]any{"state": name, "observed_at": now}
	if name == "unsupported" {
		value["reason"] = "unsupported"
	}
	return value
}

func strings256(pointer *[256]C.char, count C.size_t) []string {
	result := make([]string, 0, int(count))
	for _, value := range unsafe.Slice(pointer, int(count)) {
		result = append(result, C.GoString(&value[0]))
	}
	return result
}
