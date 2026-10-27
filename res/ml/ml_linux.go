//go:build linux

package ml

// platformProvider reports that native ONNX Runtime inference is not
// implemented in the Go module; an ONNX Runtime backend belongs in a product
// adapter (for example adapters/onnxruntime), not in res/ml.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
