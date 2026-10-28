package clipboard

import (
	"fmt"
	"unsafe"
)

// bitmapV5HeaderSize is the size of the Windows BITMAPV5HEADER structure.
const bitmapV5HeaderSize = 124

// bitmapInfoHeader matches the Windows SDK BITMAPINFOHEADER structure. It is
// portable so that DIB header validation can be tested without native calls.
type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

// validateDIBHeader checks a DIB block's BITMAPINFOHEADER for well-formedness.
// It rejects zero or impossible dimensions, unsupported compression, invalid
// bit counts, and headers that overlap pixel data. It is portable and performs
// no native calls.
func validateDIBHeader(dib []byte) (width, height, bitsOffset int, err error) {
	if len(dib) < int(unsafe.Sizeof(bitmapInfoHeader{})) {
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB is too small", ErrInvalid)
	}
	header := (*bitmapInfoHeader)(unsafe.Pointer(&dib[0]))
	if header.biSize != uint32(unsafe.Sizeof(bitmapInfoHeader{})) &&
		header.biSize != bitmapV5HeaderSize {
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB header is malformed", ErrInvalid)
	}
	// BITMAPV5HEADER is not accepted for creation; only BITMAPINFOHEADER.
	if header.biSize == bitmapV5HeaderSize {
		return 0, 0, 0, fmt.Errorf("%w: BITMAPV5HEADER DIBs are not accepted for clipboard creation", ErrInvalid)
	}
	if header.biWidth <= 0 || header.biHeight == 0 {
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB has empty dimensions", ErrInvalid)
	}
	if header.biWidth > 1<<22 {
		h := int32(header.biHeight)
		if h < 0 {
			h = -h
		}
		if int(h) > 1<<22 {
			return 0, 0, 0, fmt.Errorf("%w: clipboard DIB dimensions are implausible", ErrInvalid)
		}
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB dimensions are implausible", ErrInvalid)
	}
	switch header.biCompression {
	case 0: // BI_RGB
	case 3: // BI_BITFIELDS
	default:
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB compression %d is unsupported", ErrInvalid, header.biCompression)
	}
	switch header.biBitCount {
	case 1, 4, 8, 16, 24, 32:
	default:
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB bit count %d is unsupported", ErrInvalid, header.biBitCount)
	}
	height = int(header.biHeight)
	if height < 0 {
		height = -height
	}
	if height > 1<<22 {
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB dimensions are implausible", ErrInvalid)
	}
	bitsOffset = int(unsafe.Sizeof(bitmapInfoHeader{}))
	if header.biSize == bitmapV5HeaderSize {
		bitsOffset = bitmapV5HeaderSize
	}
	if bitsOffset > len(dib) {
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB header overlaps pixel data", ErrInvalid)
	}
	return int(header.biWidth), height, bitsOffset, nil
}
