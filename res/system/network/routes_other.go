//go:build !linux && !darwin

package network

import "context"

func platformRoutes(ctx context.Context, _ int) ([]Route, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, CollectionStatus{}, err
	}
	return []Route{}, status("unsupported", "native route-table observation is unavailable on this platform"), nil
}
