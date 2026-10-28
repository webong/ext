package clipboard

import (
	"testing"
	"unsafe"
)

// makeDIBHeader builds a minimal BITMAPINFOHEADER with the given values,
// followed by enough zero pixel data to satisfy the complete DIB layout.
func makeDIBHeader(width, height int32, bitCount uint16, compression uint32) []byte {
	h := bitmapInfoHeader{
		biSize:          uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:         width,
		biHeight:        height,
		biPlanes:        1,
		biBitCount:      bitCount,
		biCompression:   compression,
		biSizeImage:     0,
		biXPelsPerMeter: 0,
		biYPelsPerMeter: 0,
		biClrUsed:       0,
		biClrImportant:  0,
	}
	headerBytes := unsafe.Slice((*byte)(unsafe.Pointer(&h)), unsafe.Sizeof(h))
	// Compute required pixel data size.
	bpp := int64(bitCount)
	rowStride := ((int64(width)*bpp + 31) / 32) * 4
	hgt := int64(height)
	if hgt < 0 {
		hgt = -hgt
	}
	pixelDataSize := rowStride * hgt
	var colorTableSize int64
	if compression == 0 && bitCount <= 8 {
		clrUsed := int64(h.biClrUsed)
		if clrUsed == 0 {
			clrUsed = int64(1) << bpp
		}
		colorTableSize = clrUsed * 4
	} else if compression == 3 {
		colorTableSize = 12
	}
	total := int64(len(headerBytes)) + colorTableSize + pixelDataSize
	dib := make([]byte, total)
	copy(dib, headerBytes)
	return dib
}

func TestValidateDIBHeaderAcceptsValidDIB(t *testing.T) {
	dib := append(makeDIBHeader(100, 100, 32, 0), make([]byte, 10000)...)
	_, _, _, err := validateDIBHeader(dib)
	if err != nil {
		t.Fatalf("valid DIB rejected: %v", err)
	}
}

func TestValidateDIBHeaderRejectsTooSmall(t *testing.T) {
	if _, _, _, err := validateDIBHeader(makeDIBHeader(10, 10, 24, 0)[:20]); err == nil {
		t.Fatal("too-small DIB header was accepted")
	}
}

func TestValidateDIBHeaderRejectsBadHeaderSize(t *testing.T) {
	h := bitmapInfoHeader{
		biSize:          999, // wrong size
		biWidth:         100,
		biHeight:        100,
		biPlanes:        1,
		biBitCount:      24,
		biCompression:   0,
		biSizeImage:     0,
		biXPelsPerMeter: 0,
		biYPelsPerMeter: 0,
		biClrUsed:       0,
		biClrImportant:  0,
	}
	dib := unsafe.Slice((*byte)(unsafe.Pointer(&h)), unsafe.Sizeof(h))
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("bad biSize was accepted")
	}
}

func TestValidateDIBHeaderRejectsBITMAPV5HEADER(t *testing.T) {
	h := bitmapInfoHeader{
		biSize:          bitmapV5HeaderSize,
		biWidth:         100,
		biHeight:        100,
		biPlanes:        1,
		biBitCount:      32,
		biCompression:   0,
		biSizeImage:     0,
		biXPelsPerMeter: 0,
		biYPelsPerMeter: 0,
		biClrUsed:       0,
		biClrImportant:  0,
	}
	dib := unsafe.Slice((*byte)(unsafe.Pointer(&h)), unsafe.Sizeof(h))
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("BITMAPV5HEADER was accepted")
	}
}

func TestValidateDIBHeaderRejectsZeroWidth(t *testing.T) {
	dib := append(makeDIBHeader(0, 100, 24, 0), make([]byte, 100)...)
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("zero width was accepted")
	}
}

func TestValidateDIBHeaderRejectsZeroHeight(t *testing.T) {
	dib := append(makeDIBHeader(100, 0, 24, 0), make([]byte, 100)...)
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("zero height was accepted")
	}
}

func TestValidateDIBHeaderAcceptsNegativeHeight(t *testing.T) {
	dib := append(makeDIBHeader(100, -100, 24, 0), make([]byte, 100)...)
	_, h, _, err := validateDIBHeader(dib)
	if err != nil {
		t.Fatalf("negative height rejected: %v", err)
	}
	if h != 100 {
		t.Fatalf("height = %d, want 100", h)
	}
}

func TestValidateDIBHeaderRejectsUnsupportedCompression(t *testing.T) {
	dib := append(makeDIBHeader(100, 100, 24, 2), make([]byte, 100)...) // BI_JPEG = 2
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("unsupported compression was accepted")
	}
}

func TestValidateDIBHeaderRejectsInvalidBitCount(t *testing.T) {
	dib := append(makeDIBHeader(100, 100, 48, 0), make([]byte, 100)...) // 48 bpp invalid
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("invalid bit count was accepted")
	}
}

func TestValidateDIBHeaderRejectsHeaderOverlapsPixels(t *testing.T) {
	// Header claims 124 bytes (BITMAPV5HEADER size) but buffer only has 40.
	h := bitmapInfoHeader{
		biSize:          bitmapV5HeaderSize,
		biWidth:         100,
		biHeight:        100,
		biPlanes:        1,
		biBitCount:      32,
		biCompression:   0,
		biSizeImage:     0,
		biXPelsPerMeter: 0,
		biYPelsPerMeter: 0,
		biClrUsed:       0,
		biClrImportant:  0,
	}
	dib := unsafe.Slice((*byte)(unsafe.Pointer(&h)), unsafe.Sizeof(bitmapInfoHeader{}))
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("header overlapping pixels was accepted")
	}
}

func TestValidateDIBHeaderAcceptsBitfields(t *testing.T) {
	dib := append(makeDIBHeader(100, 100, 32, 3), make([]byte, 100)...) // BI_BITFIELDS = 3
	if _, _, _, err := validateDIBHeader(dib); err != nil {
		t.Fatalf("BI_BITFIELDS rejected: %v", err)
	}
}

func TestMakeDIBHeaderBitCounts(t *testing.T) {
	for _, bc := range []uint16{1, 4, 8, 16, 24, 32} {
		dib := append(makeDIBHeader(10, 10, bc, 0), make([]byte, 100)...)
		if _, _, _, err := validateDIBHeader(dib); err != nil {
			t.Fatalf("bit count %d rejected: %v", bc, err)
		}
	}
}

func TestValidateDIBHeaderRejectsBadPlanes(t *testing.T) {
	dib := append(makeDIBHeader(100, 100, 24, 0), make([]byte, 100)...)
	h := (*bitmapInfoHeader)(unsafe.Pointer(&dib[0]))
	h.biPlanes = 2
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("bad biPlanes was accepted")
	}
}

func TestValidateDIBHeaderRejectsExcessiveClrUsed(t *testing.T) {
	// 4-bit DIB can have at most 16 colors.
	dib := append(makeDIBHeader(100, 100, 4, 0), make([]byte, 100)...)
	h := (*bitmapInfoHeader)(unsafe.Pointer(&dib[0]))
	h.biClrUsed = 32 // exceeds 16 for 4-bit
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("excessive biClrUsed was accepted")
	}
}

func TestValidateDIBHeaderRejectsBitfields16Bit(t *testing.T) {
	// BI_BITFIELDS with 16-bit should be accepted.
	dib := append(makeDIBHeader(100, 100, 16, 3), make([]byte, 100)...)
	if _, _, _, err := validateDIBHeader(dib); err != nil {
		t.Fatalf("BI_BITFIELDS 16-bit rejected: %v", err)
	}
}

func TestValidateDIBHeaderRejectsBitfields24Bit(t *testing.T) {
	// BI_BITFIELDS with 24-bit should be rejected.
	dib := append(makeDIBHeader(100, 100, 24, 3), make([]byte, 100)...)
	if _, _, _, err := validateDIBHeader(dib); err == nil {
		t.Fatal("BI_BITFIELDS 24-bit was accepted")
	}
}

func TestValidateDIBHeaderRejectsTruncatedPalette(t *testing.T) {
	// Build a valid 8-bit DIB, then truncate below the required palette size.
	full := makeDIBHeader(100, 100, 8, 0)
	// Keep header + partial palette (less than the 1024 bytes needed).
	truncated := full[:int(unsafe.Sizeof(bitmapInfoHeader{}))+200]
	if _, _, _, err := validateDIBHeader(truncated); err == nil {
		t.Fatal("truncated palette was accepted")
	}
}

func TestValidateDIBHeaderRejectsTruncatedPixels(t *testing.T) {
	// Build a valid 24-bit DIB, then truncate below the required pixel size.
	full := makeDIBHeader(100, 100, 24, 0)
	// Keep header + partial pixels (less than the 30000 bytes needed).
	truncated := full[:int(unsafe.Sizeof(bitmapInfoHeader{}))+100]
	if _, _, _, err := validateDIBHeader(truncated); err == nil {
		t.Fatal("truncated pixels was accepted")
	}
}

func TestValidateDIBHeaderBitsOffset24Bit(t *testing.T) {
	dib := append(makeDIBHeader(100, 100, 24, 0), make([]byte, 30000)...)
	w, _, bitsOffset, err := validateDIBHeader(dib)
	if err != nil {
		t.Fatalf("valid 24-bit DIB rejected: %v", err)
	}
	if w != 100 {
		t.Fatalf("width = %d, want 100", w)
	}
	if bitsOffset != int(unsafe.Sizeof(bitmapInfoHeader{})) {
		t.Fatalf("bitsOffset = %d, want %d (header only for 24-bit BI_RGB)", bitsOffset, unsafe.Sizeof(bitmapInfoHeader{}))
	}
}

func TestValidateDIBHeaderBitsOffset8Bit(t *testing.T) {
	// 8-bit DIB: header + 256-color palette = 40 + 1024 = 1064 bytes offset.
	dib := append(makeDIBHeader(100, 100, 8, 0), make([]byte, 10000)...)
	w, _, bitsOffset, err := validateDIBHeader(dib)
	if err != nil {
		t.Fatalf("valid 8-bit DIB rejected: %v", err)
	}
	if w != 100 {
		t.Fatalf("width = %d, want 100", w)
	}
	expectedOffset := int(unsafe.Sizeof(bitmapInfoHeader{})) + 256*4
	if bitsOffset != expectedOffset {
		t.Fatalf("bitsOffset = %d, want %d (header + 256-color palette)", bitsOffset, expectedOffset)
	}
}

func TestValidateDIBHeaderBitsOffset32BitBitfields(t *testing.T) {
	// 32-bit BI_BITFIELDS: header + 3 masks = 40 + 12 = 52 bytes offset.
	dib := append(makeDIBHeader(100, 100, 32, 3), make([]byte, 10000)...)
	w, _, bitsOffset, err := validateDIBHeader(dib)
	if err != nil {
		t.Fatalf("valid 32-bit BI_BITFIELDS DIB rejected: %v", err)
	}
	if w != 100 {
		t.Fatalf("width = %d, want 100", w)
	}
	expectedOffset := int(unsafe.Sizeof(bitmapInfoHeader{})) + 12
	if bitsOffset != expectedOffset {
		t.Fatalf("bitsOffset = %d, want %d (header + 3 bitfield masks)", bitsOffset, expectedOffset)
	}
}
