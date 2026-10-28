//go:build darwin

package network

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// platformResolver observes the local resolver configuration on Apple
// platforms through scutil, a stable system-owned source. It performs no
// DNS lookup.
func platformResolver(ctx context.Context) (Resolver, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	command := exec.CommandContext(ctx, "scutil", "--dns")
	output, err := command.Output()
	if err != nil {
		return Resolver{Servers: []string{}, Search: []string{}, Options: []string{}},
			status("unavailable", fmt.Sprintf("scutil is unavailable: %v", err)), nil
	}
	servers, search, options, err := parseScutilDNS(output)
	if err != nil {
		return Resolver{Servers: []string{}, Search: []string{}, Options: []string{}},
			status("unavailable", fmt.Sprintf("resolver configuration could not be parsed: %v", err)), nil
	}
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	state := "complete"
	if len(servers) == 0 && len(search) == 0 {
		state = "unavailable"
	}
	return Resolver{Servers: cleanResolver(servers), Search: cleanResolver(search), Options: cleanResolver(options)},
		status(state, ""), nil
}

// parseScutilDNS parses the output of scutil --dns. It collects nameserver
// entries as servers, domain entries as search domains, and options as
// resolver options.
func parseScutilDNS(payload []byte) (servers, search, options []string, err error) {
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "nameserver[") && !strings.HasPrefix(line, "domain") && !strings.HasPrefix(line, "options") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch {
		case strings.HasPrefix(key, "nameserver"):
			ip := net.ParseIP(value)
			if ip == nil {
				continue
			}
			servers = append(servers, ip.String())
		case key == "domain":
			search = append(search, value)
		case key == "options":
			options = append(options, strings.Fields(value)...)
		}
	}
	return servers, search, options, scanner.Err()
}
