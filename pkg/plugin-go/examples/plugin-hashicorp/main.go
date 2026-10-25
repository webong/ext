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

	"github.com/webong/ext/pkg/plugin-go/cgoengine"
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
	descriptor := cgoengine.Descriptor{APIVersion: cgoengine.APIVersion, Identity: cgoengine.Identity{ID: "example/echo", Revision: "sha256:" + hex.EncodeToString(digest)}, Contracts: []cgoengine.Contract{{ContractRef: cgoengine.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []cgoengine.Operation{{Name: "echo", Surface: "observation"}}}}}
	if *guestMode {
		guest, err := cgoengine.NewGuest(descriptor, cgoengine.GuestOptions{Handler: func(_ context.Context, r cgoengine.Request) (json.RawMessage, error) {
			if r.Operation != "echo" {
				return nil, cgoengine.ErrDenied
			}
			return r.Payload, nil
		}})
		if err != nil {
			return err
		}
		return hashicorp.Serve(guest, *protocol)
	}
	ctx := context.Background()
	session, err := cgoengine.Open(ctx, descriptor, cgoengine.Options{
		Verify: func(_ context.Context, candidate cgoengine.Descriptor) error {
			return cgoengine.MatchHandshake(descriptor, candidate)
		},
		Authorize: func(_ context.Context, r cgoengine.Request) error {
			if r.Operation != "echo" {
				return cgoengine.ErrDenied
			}
			return nil
		},
		Connect: func(ctx context.Context, _ cgoengine.Descriptor) (cgoengine.Backend, error) {
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
