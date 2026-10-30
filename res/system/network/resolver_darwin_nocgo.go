//go:build darwin && !cgo

package network

import "context"

func platformResolver(ctx context.Context) (Resolver, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	return Resolver{}, status("unsupported", "SystemConfiguration DNS observation requires cgo"), nil
}
