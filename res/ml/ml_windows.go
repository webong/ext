//go:build windows

package ml

// platformProvider reports that native DirectML/ONNX Runtime inference is not
// implemented in the Go module; a Windows ML backend belongs in a product
// adapter, not in res/ml.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
