// Package media provides bounded, transport-free media primitives shared by
// local system capture and playback implementations.
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const maximumMediaBytes = 64 << 20

// PCM16 contains signed little-endian interleaved samples. Data is finite and
// caller-owned; PCM16 opens no capture device, player, file, or connection.
type PCM16 struct {
	SampleRate int
	Channels   int
	Data       []byte
}

// Validate checks format, frame alignment, and a caller-selected byte ceiling.
func (p PCM16) Validate(maxBytes int) error {
	if maxBytes <= 0 || maxBytes > maximumMediaBytes || len(p.Data) == 0 || len(p.Data) > maxBytes ||
		p.SampleRate < 8_000 || p.SampleRate > 192_000 || p.Channels < 1 || p.Channels > 8 ||
		len(p.Data)%(2*p.Channels) != 0 {
		return errors.New("invalid PCM16 format, alignment, or byte ceiling")
	}
	return nil
}

// WAV wraps samples in a PCM16 RIFF/WAVE container without resampling or
// changing their amplitude.
func WAV(p PCM16, maxBytes int) ([]byte, error) {
	if err := p.Validate(maxBytes); err != nil {
		return nil, err
	}
	output := make([]byte, 44+len(p.Data))
	copy(output[0:4], "RIFF")
	binary.LittleEndian.PutUint32(output[4:8], uint32(36+len(p.Data)))
	copy(output[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(output[16:20], 16)
	binary.LittleEndian.PutUint16(output[20:22], 1)
	binary.LittleEndian.PutUint16(output[22:24], uint16(p.Channels))
	binary.LittleEndian.PutUint32(output[24:28], uint32(p.SampleRate))
	binary.LittleEndian.PutUint32(output[28:32], uint32(p.SampleRate*p.Channels*2))
	binary.LittleEndian.PutUint16(output[32:34], uint16(p.Channels*2))
	binary.LittleEndian.PutUint16(output[34:36], 16)
	copy(output[36:40], "data")
	binary.LittleEndian.PutUint32(output[40:44], uint32(len(p.Data)))
	copy(output[44:], p.Data)
	return output, nil
}

// ReadWAV accepts bounded PCM16 RIFF/WAVE containers. Compressed,
// floating-point, extensible, duplicated, and truncated data fails explicitly;
// the function never guesses a codec.
func ReadWAV(data []byte, maxBytes int) (PCM16, error) {
	var result PCM16
	if maxBytes <= 0 || maxBytes > maximumMediaBytes || len(data) > maxBytes+(1<<20) || len(data) < 12 ||
		!bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) ||
		uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return result, errors.New("invalid or oversized WAV container")
	}
	foundFormat, foundData := false, false
	for offset, chunks := 12, 0; offset < len(data); chunks++ {
		if chunks >= 1_024 || len(data)-offset < 8 {
			return result, errors.New("invalid WAV chunks")
		}
		name := string(data[offset : offset+4])
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if size > uint64(len(data)-offset) {
			return result, errors.New("truncated WAV chunk")
		}
		chunk := data[offset : offset+int(size)]
		switch name {
		case "fmt ":
			if foundFormat || len(chunk) < 16 || binary.LittleEndian.Uint16(chunk[:2]) != 1 ||
				binary.LittleEndian.Uint16(chunk[14:16]) != 16 {
				return result, errors.New("WAV must contain one PCM16 format")
			}
			result.Channels = int(binary.LittleEndian.Uint16(chunk[2:4]))
			result.SampleRate = int(binary.LittleEndian.Uint32(chunk[4:8]))
			if int(binary.LittleEndian.Uint16(chunk[12:14])) != 2*result.Channels ||
				uint64(binary.LittleEndian.Uint32(chunk[8:12])) != uint64(result.SampleRate)*uint64(result.Channels)*2 {
				return result, errors.New("WAV format rates disagree")
			}
			foundFormat = true
		case "data":
			if foundData || size > uint64(maxBytes) {
				return result, errors.New("WAV data is duplicated or oversized")
			}
			result.Data = chunk
			foundData = true
		}
		offset += int(size) + int(size%2)
		if offset > len(data) {
			return result, errors.New("missing WAV chunk padding")
		}
	}
	if !foundFormat || !foundData {
		return result, errors.New("missing WAV format or data")
	}
	if err := result.Validate(maxBytes); err != nil {
		return result, err
	}
	result.Data = append([]byte(nil), result.Data...)
	return result, nil
}
