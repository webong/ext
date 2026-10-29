//go:build windows

package ml

// platformProvider reports that native ONNX inference is not implemented in
// the Go module; the optional C# backend ships in res/ml/windows.
func platformProvider() (Provider, error) {
	return nil, ErrUnavailable
}
