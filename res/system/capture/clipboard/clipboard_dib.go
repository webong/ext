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

// dibLayout computes the required layout for a DIB block: header size, color
// table or bitfield mask size, row stride, pixel data size, and total size.
// All arithmetic is overflow-safe. Returns an error if the layout is invalid.
func dibLayout(header *bitmapInfoHeader) (headerSize, colorTableSize, rowStride, pixelDataSize, totalSize int64, err error) {
	headerSize = int64(unsafe.Sizeof(*header))
	if header.biPlanes != 1 {
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB biPlanes must be 1, got %d", ErrInvalid, header.biPlanes)
	}
	if header.biWidth <= 0 || header.biHeight == 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB has empty dimensions", ErrInvalid)
	}
	width := int64(header.biWidth)
	height := int64(header.biHeight)
	if height < 0 {
		height = -height
	}
	if width > 1<<22 || height > 1<<22 {
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB dimensions are implausible", ErrInvalid)
	}
	switch header.biCompression {
	case 0: // BI_RGB
	case 3: // BI_BITFIELDS
		// BI_BITFIELDS is only supported for 16- and 32-bit DIBs by Win32.
		if header.biBitCount != 16 && header.biBitCount != 32 {
			return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB BI_BITFIELDS is only supported for 16- and 32-bit, got %d-bit", ErrInvalid, header.biBitCount)
		}
	default:
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB compression %d is unsupported", ErrInvalid, header.biCompression)
	}
	switch header.biBitCount {
	case 1, 4, 8, 16, 24, 32:
	default:
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB bit count %d is unsupported", ErrInvalid, header.biBitCount)
	}
	bitsPerPixel := int64(header.biBitCount)
	// Row stride with overflow-safe arithmetic: round up to 32-bit boundary.
	rowStride = ((width*bitsPerPixel + 31) / 32) * 4
	pixelDataSize = rowStride * height

	// Color table or bitfield masks.
	switch header.biCompression {
	case 3: // BI_BITFIELDS
		colorTableSize = 12 // 3 DWORD masks
	case 0: // BI_RGB
		if header.biBitCount <= 8 {
			clrUsed := int64(header.biClrUsed)
			if clrUsed == 0 {
				clrUsed = int64(1) << bitsPerPixel
			}
			// Clamp to the maximum meaningful palette size for the bit depth.
			maxColors := int64(1) << bitsPerPixel
			if clrUsed > maxColors {
				return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB biClrUsed %d exceeds %d colors for %d-bit", ErrInvalid, clrUsed, maxColors, header.biBitCount)
			}
			colorTableSize = clrUsed * 4 // RGBQUAD entries
		}
	}

	totalSize = headerSize + colorTableSize + pixelDataSize

	// biSizeImage consistency where applicable.
	if header.biSizeImage != 0 && int64(header.biSizeImage) != pixelDataSize {
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: clipboard DIB biSizeImage %d does not match pixel data size %d", ErrInvalid, header.biSizeImage, pixelDataSize)
	}
	return headerSize, colorTableSize, rowStride, pixelDataSize, totalSize, nil
}

// validateDIBHeader checks a DIB block's BITMAPINFOHEADER for well-formedness
// and verifies that the buffer contains enough data for the complete layout
// (header, color table/bitfield masks, and pixel data). It is portable and
// performs no native calls.
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

	hdrSize, colorTableSize, _, _, totalSize, err := dibLayout(header)
	if err != nil {
		return 0, 0, 0, err
	}
	if int64(len(dib)) < totalSize {
		return 0, 0, 0, fmt.Errorf("%w: clipboard DIB buffer has %d bytes, need %d for complete layout", ErrInvalid, len(dib), totalSize)
	}

	height = int(header.biHeight)
	if height < 0 {
		height = -height
	}
	// bitsOffset includes the header plus any color table or bitfield masks,
	// so that pixel data starts at the correct offset.
	bitsOffset = int(hdrSize + colorTableSize)
	return int(header.biWidth), height, bitsOffset, nil
}
