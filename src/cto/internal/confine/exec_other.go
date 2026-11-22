//go:build !darwin && !linux

package confine

// start is unreachable without a driver on this platform.
func start(plan) (int, error) { return 0, ErrNoDriver }
