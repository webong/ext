//go:build windows && (amd64 || arm64)

package surface

type controlWindowsInputPayload struct {
	VirtualKey uint16
	Scan       uint16
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
	Padding    [8]byte
}

type controlWindowsInput struct {
	Type    uint32
	Padding uint32
	Payload controlWindowsInputPayload
}
