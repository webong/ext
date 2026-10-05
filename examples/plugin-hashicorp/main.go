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
	"os/exec"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/hashicorp"
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
	descriptor := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/echo", Revision: "sha256:" + hex.EncodeToString(digest)}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo", Surface: "observation"}}}}}
	if *guestMode {
		guest, err := plugin.NewGuest(descriptor, plugin.GuestOptions{Handler: func(_ context.Context, r plugin.Request) (json.RawMessage, error) {
			if r.Operation != "echo" {
				return nil, plugin.ErrDenied
			}
			return r.Payload, nil
		}})
		if err != nil {
			return err
		}
		config := &goplugin.ServeConfig{HandshakeConfig: hashicorp.HandshakeConfig(), Plugins: goplugin.PluginSet{hashicorp.PluginName: &hashicorp.Plugin{Guest: guest}}, Logger: hclog.NewNullLogger()}
		if *protocol == "grpc" {
			config.GRPCServer = hashicorp.GRPCServer
		}
		goplugin.Serve(config)
		return nil
	}
	ctx := context.Background()
	session, err := plugin.Open(ctx, descriptor, plugin.Options{
		Verify: func(_ context.Context, candidate plugin.Descriptor) error {
			return plugin.MatchHandshake(descriptor, candidate)
		},
		Authorize: func(_ context.Context, r plugin.Request) error {
			if r.Operation != "echo" {
				return plugin.ErrDenied
			}
			return nil
		},
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			command := exec.Command(executable, "--guest", "--protocol", *protocol)
			for _, key := range []string{"SystemRoot", "TMPDIR", "TEMP", "TMP"} {
				if value := os.Getenv(key); value != "" {
					command.Env = append(command.Env, key+"="+value)
				}
			}
			deadline, _ := ctx.Deadline()
			client := goplugin.NewClient(&goplugin.ClientConfig{
				HandshakeConfig: hashicorp.HandshakeConfig(), Plugins: goplugin.PluginSet{hashicorp.PluginName: &hashicorp.Plugin{}},
				AllowedProtocols: []goplugin.Protocol{goplugin.Protocol(*protocol)}, Cmd: command, SkipHostEnv: true,
				SecureConfig: &goplugin.SecureConfig{Checksum: digest, Hash: sha256.New()}, AutoMTLS: true,
				StartTimeout: time.Until(deadline), Logger: hclog.NewNullLogger(),
			})
			return hashicorp.Connect(ctx, client)
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
