// One host and typed contract, three dynamically loaded runtime implementations.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/webong/ctx/examples/plugin-runtimes/echo"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin-cshared"
	"github.com/webong/ctx/pkg/plugin-wasm"
	"github.com/webong/ctx/pkg/plugin/author"
	"github.com/webong/ctx/pkg/plugin/nativego"
)

func main() {
	backend := flag.String("backend", "wasm", "wasm, nativego or cshared")
	artifact := flag.String("artifact", "", "path to a trusted locally built artifact")
	digest := flag.String("sha256", "", "reviewed SHA-256 digest of that artifact (required)")
	flag.Parse()
	if err := run(*backend, *artifact, *digest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(kind, path, digest string) error {
	if kind != "wasm" && kind != "nativego" && kind != "cshared" {
		return plugin.ErrUnsupported
	}
	want, err := hex.DecodeString(digest)
	if err != nil || len(want) != sha256.Size || path == "" {
		return fmt.Errorf("supply --artifact and its reviewed --sha256")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	ctx := context.Background()
	var artifact []byte
	var loaded plugin.Backend
	session, err := plugin.Open(ctx, echo.Descriptor(), plugin.Options{
		// This example trusts an explicit caller-supplied digest of a local build.
		// Real hosts additionally authenticate publisher/package and protect the
		// artifact and its native dependencies against replacement while loaded.
		Verify: func(ctx context.Context, selected plugin.Descriptor) error {
			if err := plugin.MatchHandshake(echo.Descriptor(), selected); err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return plugin.ErrInvalid
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			artifact, err = io.ReadAll(io.LimitReader(f, wasm.MaxModuleBytes+1))
			if err != nil {
				return err
			}
			if len(artifact) > wasm.MaxModuleBytes {
				return plugin.ErrInvalid
			}
			sum := sha256.Sum256(artifact)
			if hex.EncodeToString(sum[:]) != hex.EncodeToString(want) {
				return plugin.ErrMismatch
			}
			return ctx.Err()
		},
		Authorize: func(_ context.Context, request plugin.Request) error {
			return plugin.ValidateRequest(echo.Descriptor(), request)
		},
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			// Preserve a genuinely nil interface on failure (typed nils are not nil).
			switch kind {
			case "wasm":
				b, err := wasm.Open(ctx, artifact, wasm.Options{Stderr: os.Stderr})
				if err != nil {
					return nil, err
				}
				loaded = b
			case "nativego":
				b, err := nativego.Open(ctx, path)
				if err != nil {
					return nil, err
				}
				loaded = b
			case "cshared":
				b, err := cshared.Open(ctx, path)
				if err != nil {
					return nil, err
				}
				loaded = b
			}
			return loaded, nil
		},
	})
	if err != nil {
		return err
	}
	defer session.Abort()
	output, err := author.Call(ctx, session, echo.Method(), echo.Message{Message: "Hello from CTX"})
	if err != nil {
		return err
	}
	fmt.Println(output.Message)
	if err := session.Close(ctx); err != nil {
		return err
	}
	if b, ok := loaded.(*cshared.Backend); ok {
		cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return b.WaitClosed(cleanup)
	}
	return nil
}
