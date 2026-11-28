package media

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWAVRoundTripCopiesPCM(t *testing.T) {
	original := PCM16{SampleRate: 48_000, Channels: 2, Data: []byte{0, 0, 1, 0, 2, 0, 3, 0}}
	encoded, err := WAV(original, 1_024)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadWAV(encoded, 1_024)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SampleRate != original.SampleRate || decoded.Channels != original.Channels ||
		!bytes.Equal(decoded.Data, original.Data) {
		t.Fatalf("decoded PCM = %#v", decoded)
	}
	encoded[44] ^= 0xff
	if bytes.Equal(decoded.Data, encoded[44:]) {
		t.Fatal("decoded PCM aliases its input container")
	}
}

func TestPCM16RejectsMisalignmentAndUnboundedData(t *testing.T) {
	for _, input := range []PCM16{
		{SampleRate: 48_000, Channels: 2},
		{SampleRate: 48_000, Channels: 2, Data: []byte{0, 0}},
		{SampleRate: 1, Channels: 1, Data: []byte{0, 0}},
		{SampleRate: 48_000, Channels: 9, Data: make([]byte, 18)},
	} {
		if err := input.Validate(64); err == nil {
			t.Fatalf("invalid PCM was accepted: %#v", input)
		}
	}
}

func TestReadWAVRejectsUnsupportedAndTruncatedContainers(t *testing.T) {
	encoded, err := WAV(PCM16{SampleRate: 48_000, Channels: 1, Data: []byte{0, 0}}, 64)
	if err != nil {
		t.Fatal(err)
	}
	floating := append([]byte(nil), encoded...)
	binary.LittleEndian.PutUint16(floating[20:22], 3)
	if _, err := ReadWAV(floating, 64); err == nil {
		t.Fatal("floating-point WAV was accepted")
	}
	if _, err := ReadWAV(encoded[:len(encoded)-1], 64); err == nil {
		t.Fatal("truncated WAV was accepted")
	}
	if _, err := ReadWAV(encoded, 1); err == nil {
		t.Fatal("oversized PCM was accepted")
	}
}
