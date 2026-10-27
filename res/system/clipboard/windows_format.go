package clipboard

import (
	"errors"
	"unicode/utf16"
)

// CF_TEXT, CF_DIB, CF_UNICODETEXT, and CF_DIBV5 are the standard Windows
// clipboard formats this backend exposes. Registered formats keep their own
// names. Bitmap and metafile handles are refused rather than guessed.
var standardClipboardFormats = map[string]uint32{
	"CF_TEXT":        1,
	"CF_DIB":         8,
	"CF_UNICODETEXT": 13,
	"CF_DIBV5":       17,
}

var standardClipboardFormatNames = map[uint32]string{
	1:  "CF_TEXT",
	8:  "CF_DIB",
	13: "CF_UNICODETEXT",
	17: "CF_DIBV5",
}

// supportedClipboardFormat reports whether a format carries memory this backend
// can copy. CF_BITMAP and other handle-only formats are explicit gaps.
func supportedClipboardFormat(name string) bool {
	if _, ok := standardClipboardFormats[name]; ok {
		return true
	}
	return name != "" && len(name) <= 255
}

func utf16LEBytes(value string) []byte {
	units := utf16.Encode([]rune(value))
	data := make([]byte, (len(units)+1)*2)
	for index, unit := range units {
		data[index*2] = byte(unit)
		data[index*2+1] = byte(unit >> 8)
	}
	return data
}

func decodeUTF16LE(data []byte) (string, error) {
	if len(data)%2 != 0 {
		return "", errors.New("UTF-16 clipboard data has an odd length")
	}
	units := make([]uint16, 0, len(data)/2)
	for index := 0; index < len(data); index += 2 {
		unit := uint16(data[index]) | uint16(data[index+1])<<8
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return string(utf16.Decode(units)), nil
}
