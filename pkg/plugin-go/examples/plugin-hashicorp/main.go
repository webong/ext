// This example runs the same CTX guest over either HashiCorp RPC protocol.
// The host trusts its own executable; real consumers provide their package
// trust and authorization policy rather than trusting arbitrary binaries.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	goengine "github.com/webong/ext/pkg/plugin-go/cengine"
	"github.com/webong/ext/pkg/plugin-go/hashicorp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	guestMode := flag.Bool("guest", false, "run as the plugin subprocess")
	protocol := flag.String("protocol", "grpc", "grpc or netrpc")
	flag.Parse()
	if *protocol != "grpc" && *protocol != "netrpc" {
		return errors.New("protocol must be grpc or netrpc")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	file, err := os.Open(executable)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	digest := hash.Sum(nil)
	descriptor := goengine.Descriptor{APIVersion: goengine.APIVersion, Identity: goengine.Identity{ID: "example/echo", Revision: "sha256:" + hex.EncodeToString(digest)}, Contracts: []goengine.Contract{{ContractRef: goengine.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []goengine.Operation{{Name: "echo", Surface: "observation"}}}}}
	if *guestMode {
		guest, err := goengine.NewGuest(descriptor, goengine.GuestOptions{Handler: func(_ context.Context, r goengine.Request) (json.RawMessage, error) {
			if r.Operation != "echo" {
				return nil, goengine.ErrDenied
			}
			return r.Payload, nil
		}})
		if err != nil {
			return err
		}
		return hashicorp.Serve(guest, *protocol)
	}
	ctx := context.Background()
	session, err := goengine.Open(ctx, descriptor, goengine.Options{
		Verify: func(_ context.Context, candidate goengine.Descriptor) error {
			return goengine.MatchHandshake(descriptor, candidate)
		},
		Authorize: func(_ context.Context, r goengine.Request) error {
			if r.Operation != "echo" {
				return goengine.ErrDenied
			}
			return nil
		},
		Connect: func(ctx context.Context, _ goengine.Descriptor) (goengine.Backend, error) {
			process := hashicorp.Process{Executable: executable, SHA256: hex.EncodeToString(digest), Protocol: *protocol, Arguments: []string{"--guest", "--protocol", *protocol}}
			for _, key := range []string{"SystemRoot", "TMPDIR", "TEMP", "TMP"} {
				if value := os.Getenv(key); value != "" {
					process.Environment = append(process.Environment, key+"="+value)
				}
			}
			return hashicorp.ConnectProcess(ctx, process)
		},
	})
	if err != nil {
		return err
	}
	defer session.Abort()
	result, err := session.Call(ctx, descriptor.Contracts[0].ContractRef, "echo", json.RawMessage(`{"message":"hello over HashiCorp"}`))
	if err != nil {
		return err
	}
	fmt.Println(string(result))
	return session.Close(ctx)
}
