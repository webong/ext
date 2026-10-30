//go:build windows

package model

// platformProvider reports that native ONNX inference is not implemented in
// the Go module; the optional C# backend ships in res/int/model/windows.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
