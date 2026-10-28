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
	clipboardGDI                = windows.NewLazySystemDLL("gdi32.dll")
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
	procGetObject               = clipboardGDI.NewProc("GetObject")
	procGetDIBits               = clipboardGDI.NewProc("GetDIBits")
	procCreateCompatibleDC      = clipboardGDI.NewProc("CreateCompatibleDC")
	procDeleteDC                = clipboardGDI.NewProc("DeleteDC")
	procCreateDIBitmap          = clipboardGDI.NewProc("CreateDIBitmap")
	procGetDeviceCaps           = clipboardGDI.NewProc("GetDeviceCaps")
	procGetDC                   = clipboardUser32.NewProc("GetDC")
	procReleaseDC               = clipboardUser32.NewProc("ReleaseDC")
	procGetDesktopWindow        = clipboardUser32.NewProc("GetDesktopWindow")
	procDeleteObject            = clipboardGDI.NewProc("DeleteObject")
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
		var data []byte
		var err error
		if identifier == standardClipboardFormats["CF_BITMAP"] {
			// CF_BITMAP is an HBITMAP handle, not a global memory object. Copy
			// its pixels into a DIB block. The returned format stays CF_BITMAP
			// so the caller's format policy is honored; the data is the DIB
			// representation of the bitmap. The HBITMAP is borrowed from the
			// clipboard and is never released or retained.
			data, err = bitmapToDIB(handle, maxBytes-total)
			if err != nil {
				return Snapshot{}, err
			}
		} else {
			data, err = globalMemoryBytes(handle, maxBytes-total)
			if err != nil {
				return Snapshot{}, err
			}
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
			if representation.Format == "CF_BITMAP" {
				// CF_BITMAP is handle-backed: convert the DIB bytes into an
				// HBITMAP and hand ownership to the clipboard. On failure the
				// newly created bitmap is freed so nothing leaks. The limit is
				// the full maxBytes for the DIB itself, not reduced by prior
				// representations.
				hbitmap, err := dibToBitmap(data, maxBytes)
				if err != nil {
					return Receipt{}, err
				}
				if result, _, callErr := procSetClipboardData.Call(uintptr(identifier), hbitmap); result == 0 {
					procDeleteObject.Call(hbitmap)
					return Receipt{}, fmt.Errorf("%w: cannot set clipboard bitmap: %v", ErrDenied, callErr)
				}
				continue
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

func globalMemoryBytes(handle uintptr, maxBytes int64) ([]byte, error) {
	size, _, _ := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, fmt.Errorf("%w: clipboard memory has no size", ErrInvalid)
	}
	if int64(size) > maxBytes {
		return nil, fmt.Errorf("%w: clipboard memory of %d bytes exceeds the %d byte bound", ErrLimit, size, maxBytes)
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

// bitmap describes a device-independent bitmap as returned by GetObject. Only
// the dimensions are read; the pixel pointer is never dereferenced here.
type bitmap struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       unsafe.Pointer
}

type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
	bmiColors [4]uint32
}

// bitmapToDIB copies an HBITMAP's pixels into a device-independent bitmap (DIB)
// block. The HBITMAP is borrowed from the clipboard and is never released or
// retained; only its pixel bytes are copied out. maxBytes bounds the total DIB
// size before any allocation.
func bitmapToDIB(hbitmap uintptr, maxBytes int64) ([]byte, error) {
	var source bitmap
	size := uintptr(unsafe.Sizeof(source))
	if result, _, _ := procGetObject.Call(hbitmap, size, uintptr(unsafe.Pointer(&source))); result == 0 {
		return nil, fmt.Errorf("%w: cannot inspect clipboard bitmap", ErrInvalid)
	}
	width := int(source.bmWidth)
	height := int(source.bmHeight)
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("%w: clipboard bitmap has empty dimensions", ErrInvalid)
	}
	// Checked arithmetic: width*32 cannot overflow int on 64-bit platforms,
	// but validate defensively.
	if width > 1<<22 || height > 1<<22 {
		return nil, fmt.Errorf("%w: clipboard bitmap dimensions are implausible", ErrInvalid)
	}
	rowBytes := ((width*32 + 31) / 32) * 4
	imageSize := int64(rowBytes) * int64(height)
	if imageSize < 0 || imageSize > maxBytes {
		return nil, fmt.Errorf("%w: clipboard bitmap pixel data of %d bytes exceeds the %d byte bound", ErrLimit, imageSize, maxBytes)
	}
	headerSize := int64(unsafe.Sizeof(bitmapInfoHeader{}))
	if headerSize+imageSize > maxBytes {
		return nil, fmt.Errorf("%w: clipboard bitmap DIB of %d bytes exceeds the %d byte bound", ErrLimit, headerSize+imageSize, maxBytes)
	}

	hwnd, _, _ := procGetDesktopWindow.Call()
	hdc, _, _ := procGetDC.Call(hwnd)
	defer procReleaseDC.Call(hwnd, hdc)

	header := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(width),
		biHeight:      -int32(height), // top-down DIB
		biPlanes:      1,
		biBitCount:    32,
		biCompression: 0, // BI_RGB
		biSizeImage:   uint32(imageSize),
	}
	bits := make([]byte, imageSize)
	if result, _, _ := procGetDIBits.Call(hdc, hbitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&bits[0])), uintptr(unsafe.Pointer(&header)), 0); result == 0 {
		return nil, fmt.Errorf("%w: cannot copy clipboard bitmap bits", ErrInvalid)
	}

	headerBytes := unsafe.Slice((*byte)(unsafe.Pointer(&header)), unsafe.Sizeof(header))
	result := make([]byte, 0, len(headerBytes)+len(bits))
	result = append(result, headerBytes...)
	result = append(result, bits...)
	return result, nil
}

// dibToBitmap creates an HBITMAP from a DIB block and transfers ownership to
// the caller. The clipboard takes ownership once SetClipboardData succeeds.
// On failure the newly created bitmap is freed so nothing leaks. maxBytes
// bounds the DIB size before any GDI allocation.
func dibToBitmap(dib []byte, maxBytes int64) (uintptr, error) {
	if int64(len(dib)) > maxBytes {
		return 0, fmt.Errorf("%w: clipboard DIB of %d bytes exceeds the %d byte bound", ErrLimit, len(dib), maxBytes)
	}
	_, _, bitsOffset, err := validateDIBHeader(dib)
	if err != nil {
		return 0, err
	}
	bits := dib[bitsOffset:]

	// Build a complete BITMAPINFO: header + color table / bitfield masks, so
	// that CreateDIBITMAP reads a correctly sized structure. The fixed
	// bitmapInfo type only holds 4 color entries, which is insufficient for
	// palettes (4/8-bit) or BI_BITFIELDS masks.
	infoBuf := make([]byte, bitsOffset)
	copy(infoBuf, dib[:bitsOffset])
	biPtr := unsafe.Pointer(&infoBuf[0])

	hwnd, _, _ := procGetDesktopWindow.Call()
	hdc, _, _ := procGetDC.Call(hwnd)
	defer procReleaseDC.Call(hwnd, hdc)

	hbitmap, _, _ := procCreateDIBitmap.Call(
		hdc, uintptr(biPtr), 1, // CBM_INIT
		uintptr(unsafe.Pointer(&bits[0])), uintptr(biPtr), 0, // DIB_RGB_COLORS
	)
	if hbitmap == 0 {
		return 0, fmt.Errorf("%w: cannot create clipboard bitmap from DIB", ErrInvalid)
	}
	return hbitmap, nil
}

var _ Backend = windowsClipboard{}
