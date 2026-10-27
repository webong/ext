//go:build darwin

package ml

// platformProvider reports that native CoreML inference is not implemented in
// the Go module; the CoreML provider ships in res/ml/ios as part of the ExtML
// Swift target, which builds on both macOS and iOS.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
