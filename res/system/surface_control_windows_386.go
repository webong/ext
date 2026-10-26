//go:build windows && 386

package system

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
	Payload controlWindowsInputPayload
}
