package clipboard

import (
	"testing"
	"unsafe"
)

// makeDIBHeader builds a minimal BITMAPINFOHEADER with the given values.
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
	return unsafe.Slice((*byte)(unsafe.Pointer(&h)), unsafe.Sizeof(h))
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
