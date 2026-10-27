package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// MaxCommandOutput bounds captured manager output. Manager output is diagnostic
// text, never a protocol, and is truncated rather than streamed to the host.
const MaxCommandOutput = 64 << 10

// ExecRunner runs one native service command with captured, bounded output.
// It is the default CommandRunner for the platform backends.
func ExecRunner(ctx context.Context, directory, executable string, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: command requires a context", ErrInvalid)
	}
	if executable == "" {
		return nil, fmt.Errorf("%w: command executable is required", ErrInvalid)
	}
	command := exec.CommandContext(ctx, executable, args...)
	if directory != "" {
		command.Dir = directory
	}
	output := &cappedBuffer{limit: MaxCommandOutput}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	return output.data, err
}

type cappedBuffer struct {
	data  []byte
	limit int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	if len(b.data)+len(value) > b.limit {
		return 0, errors.New("service manager output exceeds limit")
	}
	b.data = append(b.data, value...)
	return len(value), nil
}

// OutputText returns trimmed command output for a status detail.
func OutputText(output []byte) string {
	text := string(output)
	for len(text) > 0 && (text[len(text)-1] == '\n' || text[len(text)-1] == '\r') {
		text = text[:len(text)-1]
	}
	if len(text) > 4096 {
		text = text[:4096]
	}
	return text
}
