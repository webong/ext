package clipboard

import (
	"strings"
	"testing"
)

func TestUTF16ClipboardRoundTrip(t *testing.T) {
	data := utf16LEBytes("hello \u00e9\u4e16")
	if len(data)%2 != 0 || data[len(data)-2] != 0 || data[len(data)-1] != 0 {
		t.Fatalf("UTF-16 data is not NUL-terminated: %#v", data)
	}
	value, err := decodeUTF16LE(data)
	if err != nil || value != "hello \u00e9\u4e16" {
		t.Fatalf("round trip = %q, %v", value, err)
	}
	value, err = decodeUTF16LE(append(utf16LEBytes("text"), []byte("ignored!")...))
	if err != nil || value != "text" {
		t.Fatalf("terminated decode = %q, %v", value, err)
	}
	if _, err = decodeUTF16LE([]byte{1, 2, 3}); err == nil {
		t.Fatal("odd-length UTF-16 was accepted")
	}
}

func TestStandardClipboardFormatNames(t *testing.T) {
	if standardClipboardFormats["CF_UNICODETEXT"] != 13 {
		t.Fatalf("unicode text identifier = %d", standardClipboardFormats["CF_UNICODETEXT"])
	}
	if standardClipboardFormatNames[8] != "CF_DIB" {
		t.Fatalf("DIB name = %q", standardClipboardFormatNames[8])
	}
	if !supportedClipboardFormat("CF_DIBV5") || !supportedClipboardFormat("PNG") {
		t.Fatal("supported format was refused")
	}
	if supportedClipboardFormat("") || supportedClipboardFormat(strings.Repeat("x", 256)) {
		t.Fatal("invalid format was accepted")
	}
}
