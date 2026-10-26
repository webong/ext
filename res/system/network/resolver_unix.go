//go:build !windows

package network

import (
	"bufio"
	"context"
	"os"
	"strings"
)

func platformResolver(ctx context.Context) (Resolver, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	file, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return Resolver{Servers: []string{}, Search: []string{}, Options: []string{}}, status("unavailable", "resolver configuration is unavailable"), nil
	}
	defer file.Close()
	result := Resolver{Servers: []string{}, Search: []string{}, Options: []string{}}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return Resolver{}, CollectionStatus{}, err
		}
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			result.Servers = append(result.Servers, fields[1])
		case "search":
			result.Search = append(result.Search, fields[1:]...)
		case "domain":
			if len(result.Search) == 0 {
				result.Search = append(result.Search, fields[1])
			}
		case "options":
			result.Options = append(result.Options, fields[1:]...)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, status("partial", "resolver configuration could not be read completely"), nil
	}
	result.Servers = cleanResolver(result.Servers)
	result.Search = cleanResolver(result.Search)
	result.Options = cleanResolver(result.Options)
	return result, status("complete", ""), nil
}
