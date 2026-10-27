package media

import (
	"testing"
	"time"
)

func TestRecordingOptionsNormalize(t *testing.T) {
	options, err := RecordingOptions{Format: "webm"}.Normalize()
	if err != nil || options.FrameRate != 30 || options.MaxDuration != DefaultRecordingDuration || options.MaxBytes != DefaultRecordingBytes {
		t.Fatalf("defaults = %#v, %v", options, err)
	}
	for name, options := range map[string]RecordingOptions{
		"format":    {Format: "avi"},
		"frameLow":  {Format: "webm", FrameRate: -1},
		"frameHigh": {Format: "webm", FrameRate: 61},
		"tooShort":  {Format: "webm", MaxDuration: time.Millisecond},
		"tooLong":   {Format: "webm", MaxDuration: MaxRecordingDuration + time.Second},
		"bytes":     {Format: "webm", MaxBytes: MaxRecordingBytes + 1},
		"zeroBytes": {Format: "webm", MaxBytes: -1},
	} {
		if _, err := options.Normalize(); err == nil {
			t.Errorf("%s options were accepted", name)
		}
	}
	options, err = RecordingOptions{Format: "mp4", FrameRate: 60, MaxDuration: time.Hour, MaxBytes: 1}.Normalize()
	if err != nil || options.FrameRate != 60 {
		t.Fatalf("explicit options = %#v, %v", options, err)
	}
}
