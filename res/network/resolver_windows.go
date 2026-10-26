//go:build windows

package network

import "context"

func platformResolver(ctx context.Context) (Resolver, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	return Resolver{Servers: []string{}, Search: []string{}, Options: []string{}}, status("unsupported", "Windows resolver configuration is not available in this build"), nil
}
