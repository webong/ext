package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/jsonline"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

type jsonlineProcess struct {
	Executable  string   `json:"executable"`
	SHA256      string   `json:"sha256"`
	Arguments   []string `json:"arguments,omitempty"`
	Environment []string `json:"environment,omitempty"`
}

func (p jsonlineProcess) Validate() error {
	digest, err := hex.DecodeString(p.SHA256)
	if err != nil || len(digest) != sha256.Size || !filepath.IsAbs(p.Executable) || len(p.Executable) > 4096 || strings.ContainsRune(p.Executable, 0) || !utf8.ValidString(p.Executable) || len(p.Arguments) > 256 || len(p.Environment) > 256 {
		return plugin.ErrInvalid
	}
	for _, arg := range p.Arguments {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return plugin.ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, env := range p.Environment {
		k, _, ok := strings.Cut(env, "=")
		if !ok || k == "" || len(env) > 8192 || strings.ContainsRune(env, 0) || seen[k] {
			return plugin.ErrInvalid
		}
		seen[k] = true
	}
	return nil
}
func (p jsonlineProcess) Verify() error {
	if err := p.Validate(); err != nil {
		return err
	}
	f, err := os.Open(p.Executable)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return plugin.ErrInvalid
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(p.SHA256) {
		return plugin.ErrMismatch
	}
	return nil
}

type ownedProcess struct {
	read    io.ReadCloser
	write   io.WriteCloser
	command *exec.Cmd
	life    *os.File
	once    sync.Once
	err     error
}

func (c *ownedProcess) Read(p []byte) (int, error)  { return c.read.Read(p) }
func (c *ownedProcess) Write(p []byte) (int, error) { return c.write.Write(p) }
func (c *ownedProcess) Close() error {
	c.once.Do(func() {
		_ = c.write.Close()
		_ = c.read.Close()
		if c.life != nil {
			_ = c.life.Close()
		} else {
			_ = c.command.Process.Kill()
		}
		err := c.command.Wait()
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			c.err = err
		}
	})
	return c.err
}
func (p jsonlineProcess) Connect(ctx context.Context) (plugin.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.Verify(); err != nil {
		return nil, err
	}
	cmd := exec.Command(p.Executable, append([]string(nil), p.Arguments...)...)
	cmd.Env = append([]string{}, p.Environment...)
	conn, err := startPiped(cmd)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return jsonline.NewClient(conn), nil
}
func startPiped(cmd *exec.Cmd) (*ownedProcess, error) {
	read, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	write, err := cmd.StdinPipe()
	if err != nil {
		_ = read.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, err
	}
	return &ownedProcess{read: read, write: write, command: cmd}, nil
}
