//go:build !darwin && !linux && !windows

package ml

// platformProvider reports that native model inference is not implemented on
// this platform.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
