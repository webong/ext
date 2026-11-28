//go:build linux

package model

// platformProvider reports that native ONNX Runtime inference is not
// implemented in the Go module; an ONNX Runtime backend belongs in a product
// adapter (for example xallet.app/tab/onnxruntime), not in src/ctn/contract/model.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
