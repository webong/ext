//go:build windows

package model

// platformProvider reports that native ONNX inference is not implemented in
// the Go module; the optional C# backend ships in pkg/ext-windows/int/model.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
