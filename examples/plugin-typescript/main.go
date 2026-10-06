// Run from the CTX repository root: go run ./examples/plugin-typescript.
// This example trusts the explicitly selected repository fixture and hashes it
// before launch; production hosts must authenticate their own artifact source.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/jsonline"
)

type pipes struct {
	io.Reader
	io.Writer
	once sync.Once
	stop func()
}

func (p *pipes) Close() error { p.once.Do(p.stop); return nil }
func main() {
	var err error
	if len(os.Args) == 2 && os.Args[1] == "--guest" {
		err = serveGoGuest()
	} else {
		err = run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	node, err := exec.LookPath("node")
	if err != nil {
		return err
	}
	script, err := filepath.Abs("examples/plugin-typescript/guest.mjs")
	if err != nil {
		return err
	}
	sdk, err := filepath.Abs("pkg/plugin-ts")
	if err != nil {
		return err
	}
	// These are development fixtures selected by the person running the example.
	scriptBytes, err := os.ReadFile(script)
	if err != nil {
		return err
	}
	scriptHash := sha256.Sum256(scriptBytes)
	expected, err := plugin.DirectoryDigest(sdk)
	if err != nil {
		return err
	}
	descriptor := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/typescript", Revision: "compiled-1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo", Surface: "observation"}}}}}
	ctx := context.Background()
	s, err := plugin.Open(ctx, descriptor, plugin.Options{Verify: func(context.Context, plugin.Descriptor) error {
		actual, err := plugin.DirectoryDigest(sdk)
		if err != nil {
			return err
		}
		currentScript, err := os.ReadFile(script)
		if err != nil {
			return err
		}
		if actual != expected || sha256.Sum256(currentScript) != scriptHash {
			return plugin.ErrMismatch
		}
		return nil
	}, Authorize: func(_ context.Context, r plugin.Request) error {
		if r.Operation != "echo" {
			return plugin.ErrDenied
		}
		return nil
	}, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
		cmd := exec.Command(node, script)
		cmd.Env = []string{}
		for _, key := range []string{"SystemRoot", "TMPDIR", "TEMP", "TMP"} {
			if value := os.Getenv(key); value != "" {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		cmd.Stderr = os.Stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, errors.Join(err, stdout.Close())
		}
		if err := cmd.Start(); err != nil {
			return nil, errors.Join(err, stdout.Close(), stdin.Close())
		}
		conn := &pipes{Reader: stdout, Writer: stdin, stop: func() { _ = stdin.Close(); _ = stdout.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }}
		return jsonline.NewClient(conn), nil
	}})
	if err != nil {
		return err
	}
	defer s.Abort()
	result, err := s.Call(ctx, descriptor.Contracts[0].ContractRef, "echo", json.RawMessage(`{"message":"hello from Go to TypeScript"}`))
	if err != nil {
		return err
	}
	fmt.Println(string(result))
	return s.Close(ctx)
}
