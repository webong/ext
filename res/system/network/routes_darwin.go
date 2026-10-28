//go:build darwin

package network

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// platformRoutes observes the local route table on Apple platforms through
// netstat, a stable system-owned source. It performs no routing change.
func platformRoutes(ctx context.Context, limit int) ([]Route, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, err
	}
	command := exec.CommandContext(ctx, "netstat", "-rn")
	output, err := command.Output()
	if err != nil {
		return nil, status("unavailable", fmt.Sprintf("netstat is unavailable: %v", err)), nil
	}
	result, partial, err := parseNetstatRoutes(output, limit)
	if err != nil {
		return nil, status("unavailable", fmt.Sprintf("route table could not be parsed: %v", err)), nil
	}
	state := "complete"
	detail := ""
	if partial {
		state = "partial"
		detail = "one or more route entries were truncated or could not be interpreted"
	}
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, err
	}
	return result, status(state, detail), nil
}

// parseNetstatRoutes parses the output of netstat -rn. Only entries whose
// destination can be converted to a reliable CIDR are reported; network
// destinations without an explicit prefix length are skipped rather than
// guessing a prefix.
func parseNetstatRoutes(payload []byte, limit int) ([]Route, bool, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	result := []Route{}
	partial := false
	section := "" // "inet" or "inet6"
	for scanner.Scan() {
		if err := context.Background().Err(); err != nil {
			return result, partial, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Routing tables") {
			continue
		}
		switch line {
		case "Internet:":
			section = "inet"
			continue
		case "Internet6:":
			section = "inet6"
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if len(result) >= limit {
			partial = true
			continue
		}
		destination := fields[0]
		gateway := fields[1]
		netif := fields[3]
		cidr, ok := destinationToCIDR(destination, section)
		if !ok {
			continue
		}
		metric := 0
		if len(fields) > 4 {
			if m, err := strconv.Atoi(fields[4]); err == nil {
				metric = m
			}
		}
		defaultRoute := false
		if _, net, err := net.ParseCIDR(cidr); err == nil {
			if ones, _ := net.Mask.Size(); ones == 0 {
				defaultRoute = true
			}
		}
		result = append(result, Route{
			Destination: cidr,
			Gateway:     routeGateway(gateway),
			Interface:   netif,
			Metric:      metric,
			Default:     defaultRoute,
		})
	}
	return result, partial, scanner.Err()
}

func destinationToCIDR(destination, section string) (string, bool) {
	if destination == "default" {
		if section == "inet6" {
			return "::/0", true
		}
		return "0.0.0.0/0", true
	}
	if strings.Contains(destination, "/") {
		return destination, true
	}
	if ip := net.ParseIP(destination); ip != nil {
		if ip.To4() != nil {
			return destination + "/32", true
		}
		return destination + "/128", true
	}
	// Network destinations without an explicit prefix (for example
	// "192.168.100") cannot be interpreted reliably, so they are skipped.
	return "", false
}

func routeGateway(gateway string) string {
	if gateway == "" || gateway == "*" || gateway == "default" {
		return ""
	}
	if ip := net.ParseIP(gateway); ip != nil {
		return ip.String()
	}
	// Link-layer (MAC) or link#N gateway: on-link, no gateway address.
	if strings.Contains(gateway, ":") || strings.HasPrefix(gateway, "link#") {
		return ""
	}
	return gateway
}

// sortRoutes is used by tests to observe a deterministic ordering.
func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		return a.Destination+"\x00"+a.Gateway+"\x00"+a.Interface < b.Destination+"\x00"+b.Gateway+"\x00"+b.Interface
	})
}
