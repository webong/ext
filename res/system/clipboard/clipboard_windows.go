//go:build windows

package clipboard

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	clipboardUser32             = windows.NewLazySystemDLL("user32.dll")
	clipboardKernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard           = clipboardUser32.NewProc("OpenClipboard")
	procCloseClipboard          = clipboardUser32.NewProc("CloseClipboard")
	procEmptyClipboard          = clipboardUser32.NewProc("EmptyClipboard")
	procGetClipboardData        = clipboardUser32.NewProc("GetClipboardData")
	procSetClipboardData        = clipboardUser32.NewProc("SetClipboardData")
	procEnumClipboardFormats    = clipboardUser32.NewProc("EnumClipboardFormats")
	procGetClipboardFormatName  = clipboardUser32.NewProc("GetClipboardFormatNameW")
	procRegisterClipboardFormat = clipboardUser32.NewProc("RegisterClipboardFormatW")
	procClipboardSequenceNumber = clipboardUser32.NewProc("GetClipboardSequenceNumber")
	procGlobalAlloc             = clipboardKernel32.NewProc("GlobalAlloc")
	procGlobalFree              = clipboardKernel32.NewProc("GlobalFree")
	procGlobalLock              = clipboardKernel32.NewProc("GlobalLock")
	procGlobalUnlock            = clipboardKernel32.NewProc("GlobalUnlock")
	procGlobalSize              = clipboardKernel32.NewProc("GlobalSize")
	procRtlMoveMemory           = clipboardKernel32.NewProc("RtlMoveMemory")
)

const clipboardGlobalMoveable = 0x0002

// WindowsBackend implements Backend with the Win32 clipboard. It retains no
// history, prompts for nothing, and performs no format conversion beyond UTF-16
// text, because Windows exposes one clipboard item with several formats.
func WindowsBackend() Backend { return windowsClipboard{} }

type windowsClipboard struct{}

func (windowsClipboard) Generation(context.Context) (string, error) {
	value, _, _ := procClipboardSequenceNumber.Call()
	return fmt.Sprintf("windows:%d", uint32(value)), nil
}

func (windowsClipboard) Read(ctx context.Context, maxBytes int64, formats []string) (Snapshot, error) {
	if err := openClipboard(ctx); err != nil {
		return Snapshot{}, err
	}
	defer procCloseClipboard.Call()
	generation, err := windowsClipboard{}.Generation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allowed := map[string]bool{}
	for _, format := range formats {
		allowed[format] = true
	}
	item := Item{Representations: []Representation{}}
	var total int64
	var identifier uint32
	for {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		next, _, _ := procEnumClipboardFormats.Call(uintptr(identifier))
		identifier = uint32(next)
		if identifier == 0 {
			break
		}
		name := clipboardFormatName(identifier)
		if !allowed[name] {
			continue
		}
		handle, _, callErr := procGetClipboardData.Call(uintptr(identifier))
		if handle == 0 {
			return Snapshot{}, fmt.Errorf("%w: cannot read clipboard format %s: %v", ErrInvalid, name, callErr)
		}
		data, err := globalMemoryBytes(handle)
		if err != nil {
			return Snapshot{}, err
		}
		total += int64(len(data))
		if total > maxBytes {
			return Snapshot{}, ErrLimit
		}
		if identifier == standardClipboardFormats["CF_UNICODETEXT"] {
			text, err := decodeUTF16LE(data)
			if err != nil {
				return Snapshot{}, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			data = []byte(text)
		}
		item.Representations = append(item.Representations, Representation{Format: name, Data: data})
	}
	if len(item.Representations) == 0 {
		return Snapshot{}, ErrDenied
	}
	return Snapshot{Generation: generation, Items: []Item{item}}, nil
}

func (windowsClipboard) Write(ctx context.Context, request WriteRequest, maxBytes int64, formats []string) (Receipt, error) {
	generation, err := windowsClipboard{}.Generation(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if generation != request.ExpectedGeneration {
		return Receipt{}, ErrChanged
	}
	if len(request.Items) > 1 {
		return Receipt{}, fmt.Errorf("%w: the Windows clipboard holds one item", ErrInvalid)
	}
	if err = openClipboard(ctx); err != nil {
		return Receipt{}, err
	}
	defer procCloseClipboard.Call()
	if _, _, callErr := procEmptyClipboard.Call(); callErr != nil {
		return Receipt{}, fmt.Errorf("%w: cannot clear the clipboard: %v", ErrDenied, callErr)
	}
	if len(request.Items) == 1 {
		allowed := map[string]bool{}
		for _, format := range formats {
			allowed[format] = true
		}
		for _, representation := range request.Items[0].Representations {
			if !allowed[representation.Format] || !supportedClipboardFormat(representation.Format) {
				return Receipt{}, ErrDenied
			}
			identifier, err := clipboardFormatIdentifier(representation.Format)
			if err != nil {
				return Receipt{}, err
			}
			data := representation.Data
			if representation.Format == "CF_UNICODETEXT" {
				data = utf16LEBytes(string(representation.Data))
			}
			if int64(len(data)) > maxBytes {
				return Receipt{}, ErrLimit
			}
			if err = setGlobalClipboardData(identifier, data); err != nil {
				return Receipt{}, err
			}
		}
	}
	updated, err := windowsClipboard{}.Generation(ctx)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{Generation: updated, Dispatch: "dispatched"}, nil
}

func openClipboard(ctx context.Context) error {
	for attempt := 0; attempt < 50; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, _, _ := procOpenClipboard.Call(0)
		if result != 0 {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("%w: the clipboard is held by another process", ErrDenied)
}

func clipboardFormatName(identifier uint32) string {
	if name, ok := standardClipboardFormatNames[identifier]; ok {
		return name
	}
	buffer := make([]uint16, 256)
	written, _, _ := procGetClipboardFormatName.Call(
		uintptr(identifier), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)),
	)
	if written == 0 {
		return fmt.Sprintf("CF_%d", identifier)
	}
	return windows.UTF16ToString(buffer[:written])
}

func clipboardFormatIdentifier(name string) (uint32, error) {
	if identifier, ok := standardClipboardFormats[name]; ok {
		return identifier, nil
	}
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid clipboard format name", ErrInvalid)
	}
	identifier, _, _ := procRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(pointer)))
	if identifier == 0 {
		return 0, fmt.Errorf("%w: cannot register clipboard format %s", ErrDenied, name)
	}
	return uint32(identifier), nil
}

func globalMemoryBytes(handle uintptr) ([]byte, error) {
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, fmt.Errorf("%w: clipboard memory has no size", ErrInvalid)
	}
	pointer, _, lockErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		return nil, fmt.Errorf("%w: cannot lock clipboard memory: %v", ErrDenied, lockErr)
	}
	defer procGlobalUnlock.Call(handle)
	data := make([]byte, int(size))
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&data[0])), pointer, size)
	return data, nil
}

func setGlobalClipboardData(identifier uint32, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: clipboard representation is empty", ErrInvalid)
	}
	handle, _, allocErr := procGlobalAlloc.Call(clipboardGlobalMoveable, uintptr(len(data)))
	if handle == 0 {
		return fmt.Errorf("%w: cannot allocate clipboard memory: %v", ErrDenied, allocErr)
	}
	pointer, _, lockErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		procGlobalFree.Call(handle)
		return fmt.Errorf("%w: cannot lock clipboard memory: %v", ErrDenied, lockErr)
	}
	procRtlMoveMemory.Call(pointer, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	procGlobalUnlock.Call(handle)
	result, _, callErr := procSetClipboardData.Call(uintptr(identifier), handle)
	if result == 0 {
		procGlobalFree.Call(handle)
		return fmt.Errorf("%w: cannot set clipboard data: %v", ErrDenied, callErr)
	}
	return nil
}

var _ Backend = windowsClipboard{}
