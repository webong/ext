//go:build !darwin && !linux && !windows

package model

// platformProvider reports that native model inference is not implemented on
// this platform.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
