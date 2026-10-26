//go:build linux

package network

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

func platformRoutes(ctx context.Context, limit int) ([]Route, CollectionStatus, error) {
	result := []Route{}
	partial := false
	if file, err := os.Open("/proc/net/route"); err == nil {
		values, readErr := readLinuxIPv4Routes(ctx, file, limit)
		_ = file.Close()
		result = append(result, values...)
		partial = partial || readErr != nil
	} else {
		partial = true
	}
	remaining := limit - len(result)
	if remaining > 0 {
		if file, err := os.Open("/proc/net/ipv6_route"); err == nil {
			values, readErr := readLinuxIPv6Routes(ctx, file, remaining)
			_ = file.Close()
			result = append(result, values...)
			partial = partial || readErr != nil
		} else {
			partial = true
		}
	} else {
		partial = true
	}
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, err
	}
	if partial {
		return result, status("partial", "one or more route tables were unavailable, malformed, or truncated"), nil
	}
	return result, status("complete", ""), nil
}

func readLinuxIPv4Routes(ctx context.Context, input io.Reader, limit int) ([]Route, error) {
	scanner := bufio.NewScanner(input)
	result := []Route{}
	first := true
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 11 {
			return result, fmt.Errorf("malformed IPv4 route")
		}
		if len(result) >= limit {
			return result, fmt.Errorf("route limit reached")
		}
		destination, err := linuxIPv4(fields[1])
		if err != nil {
			return result, err
		}
		gateway, err := linuxIPv4(fields[2])
		if err != nil {
			return result, err
		}
		mask, err := linuxIPv4(fields[7])
		if err != nil {
			return result, err
		}
		prefix, bits := net.IPMask(mask.To4()).Size()
		if bits != 32 || prefix < 0 {
			return result, fmt.Errorf("invalid IPv4 route mask")
		}
		metric, err := strconv.Atoi(fields[6])
		if err != nil {
			return result, fmt.Errorf("invalid IPv4 route metric")
		}
		gatewayText := gateway.String()
		if gateway.IsUnspecified() {
			gatewayText = ""
		}
		result = append(result, Route{Destination: destination.String() + "/" + strconv.Itoa(prefix), Gateway: gatewayText, Interface: fields[0], Metric: metric, Default: destination.IsUnspecified() && prefix == 0})
	}
	return result, scanner.Err()
}

func linuxIPv4(value string) (net.IP, error) {
	parsed, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid IPv4 route address")
	}
	return net.IPv4(byte(parsed), byte(parsed>>8), byte(parsed>>16), byte(parsed>>24)), nil
}

func readLinuxIPv6Routes(ctx context.Context, input io.Reader, limit int) ([]Route, error) {
	scanner := bufio.NewScanner(input)
	result := []Route{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			return result, fmt.Errorf("malformed IPv6 route")
		}
		if len(result) >= limit {
			return result, fmt.Errorf("route limit reached")
		}
		destinationBytes, err := hex.DecodeString(fields[0])
		if err != nil || len(destinationBytes) != net.IPv6len {
			return result, fmt.Errorf("invalid IPv6 route destination")
		}
		prefixValue, err := strconv.ParseUint(fields[1], 16, 8)
		if err != nil || prefixValue > 128 {
			return result, fmt.Errorf("invalid IPv6 route prefix")
		}
		gatewayBytes, err := hex.DecodeString(fields[4])
		if err != nil || len(gatewayBytes) != net.IPv6len {
			return result, fmt.Errorf("invalid IPv6 route gateway")
		}
		metricValue, err := strconv.ParseUint(fields[5], 16, 32)
		if err != nil {
			return result, fmt.Errorf("invalid IPv6 route metric")
		}
		destination := net.IP(destinationBytes)
		gateway := net.IP(gatewayBytes)
		gatewayText := gateway.String()
		if gateway.IsUnspecified() {
			gatewayText = ""
		}
		prefix := int(prefixValue)
		result = append(result, Route{Destination: destination.String() + "/" + strconv.Itoa(prefix), Gateway: gatewayText, Interface: fields[9], Metric: int(metricValue), Default: destination.IsUnspecified() && prefix == 0})
	}
	return result, scanner.Err()
}
