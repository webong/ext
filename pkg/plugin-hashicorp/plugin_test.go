package hashicorp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	hc "github.com/hashicorp/go-plugin"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/plugintest"
)

func fixtureDescriptor() plugin.Descriptor {
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/hashicorp", Revision: "fixture-1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}, {Name: "wait"}, {Name: "private-error"}, {Name: "rate-limit"}, {Name: "forbidden"}}}}}
}

// A real go-plugin subprocess exercises the native startup handshake, cookie,
// TLS, Dispense, CTX handshake, invocation, and process cleanup for both RPCs.
func TestGuestProcess(t *testing.T) {
	transport := os.Getenv("EXT_PLUGIN_TEST_CHILD")
	if transport == "" {
		return
	}
	guest, err := plugin.NewGuest(fixtureDescriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		switch r.Operation {
		case "wait":
			<-ctx.Done()
			return nil, ctx.Err()
		case "private-error":
			return nil, errors.New("private provider details")
		case "rate-limit":
			return nil, &plugin.RemoteError{Code: "rate_limit", Message: "busy", RetryAfterMilliseconds: 123}
		case "forbidden":
			panic("host failed to enforce authorization")
		default:
			return r.Payload, nil
		}
	}})
	if os.Getenv("EXT_PLUGIN_CONFORMANCE") == "1" {
		guest, err = plugintest.Guest()
	}
	if err != nil {
		panic(err)
	}
	config := &hc.ServeConfig{HandshakeConfig: HandshakeConfig(), Plugins: hc.PluginSet{PluginName: &Plugin{Guest: guest}}, Logger: hclog.New(&hclog.LoggerOptions{Level: hclog.Error})}
	if transport == string(hc.ProtocolGRPC) {
		config.GRPCServer = GRPCServer
	}
	hc.Serve(config)
	os.Exit(0)
}

func fixtureClient(t *testing.T, transport hc.Protocol) *hc.Client {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	_, err = io.Copy(digest, file)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	command := exec.Command(executable, "-test.run=^TestGuestProcess$")
	command.Env = []string{"EXT_PLUGIN_TEST_CHILD=" + string(transport)}
	if os.Getenv("EXT_PLUGIN_CONFORMANCE") == "1" {
		command.Env = append(command.Env, "EXT_PLUGIN_CONFORMANCE=1")
	}
	// Only platform startup material is inherited by this fixture.
	for _, key := range []string{"PATH", "SystemRoot", "TMPDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	client := hc.NewClient(&hc.ClientConfig{
		HandshakeConfig: HandshakeConfig(), Plugins: hc.PluginSet{PluginName: &Plugin{}},
		AllowedProtocols: []hc.Protocol{transport}, Cmd: command, SkipHostEnv: true,
		SecureConfig: &hc.SecureConfig{Checksum: digest.Sum(nil), Hash: sha256.New()},
		AutoMTLS:     true, StartTimeout: 5 * time.Second, Logger: hclog.NewNullLogger(), Stderr: os.Stderr,
	})
	t.Cleanup(client.Kill)
	return client
}

func openFixture(t *testing.T, client *hc.Client, d plugin.Descriptor) (*plugin.Session, error) {
	t.Helper()
	verified := false
	return plugin.Open(context.Background(), d, plugin.Options{
		Verify: func(context.Context, plugin.Descriptor) error { verified = true; return nil },
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			if !verified {
				t.Error("launch preceded verification")
			}
			return Connect(ctx, client)
		},
		Authorize: func(_ context.Context, r plugin.Request) error {
			if r.Operation == "forbidden" {
				return plugin.ErrDenied
			}
			return nil
		},
	})
}

func TestHostGuestOverBothProtocols(t *testing.T) {
	for _, transport := range []hc.Protocol{hc.ProtocolNetRPC, hc.ProtocolGRPC} {
		t.Run(string(transport), func(t *testing.T) {
			client := fixtureClient(t, transport)
			d := fixtureDescriptor()
			s, err := openFixture(t, client, d)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Abort()
			ref := d.Contracts[0].ContractRef
			var calls sync.WaitGroup
			for i := 0; i < 8; i++ {
				calls.Add(1)
				go func() {
					defer calls.Done()
					result, err := s.Call(context.Background(), ref, "echo", json.RawMessage(`{"value":"hello"}`))
					if err != nil || string(result) != `{"value":"hello"}` {
						t.Errorf("echo: %s, %v", result, err)
					}
				}()
			}
			calls.Wait()
			if _, err := s.Call(context.Background(), ref, "forbidden", nil); !errors.Is(err, plugin.ErrDenied) {
				t.Fatal(err)
			}
			_, err = s.Call(context.Background(), ref, "rate-limit", nil)
			var remote *plugin.RemoteError
			if !errors.As(err, &remote) || remote.Code != "rate_limit" || remote.RetryAfterMilliseconds != 123 {
				t.Fatal(err)
			}
			_, err = s.Call(context.Background(), ref, "private-error", nil)
			if !errors.As(err, &remote) || strings.Contains(err.Error(), "private provider") {
				t.Fatalf("private error exposed: %v", err)
			}
			if err := s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !client.Exited() {
				t.Fatal("guest survived session shutdown")
			}
		})
	}
}

func TestMismatchAndCancellationCleanUpProcess(t *testing.T) {
	for _, transport := range []hc.Protocol{hc.ProtocolNetRPC, hc.ProtocolGRPC} {
		t.Run(string(transport)+"/mismatch", func(t *testing.T) {
			client := fixtureClient(t, transport)
			d := fixtureDescriptor()
			d.Identity.Revision = "different"
			if _, err := openFixture(t, client, d); !errors.Is(err, plugin.ErrMismatch) {
				t.Fatal(err)
			}
			if !client.Exited() {
				t.Fatal("handshake failure leaked guest")
			}
		})
		t.Run(string(transport)+"/cancel", func(t *testing.T) {
			client := fixtureClient(t, transport)
			d := fixtureDescriptor()
			s, err := openFixture(t, client, d)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Abort()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			_, err = s.Call(ctx, d.Contracts[0].ContractRef, "wait", nil)
			if err == nil {
				t.Fatal("deadline ignored")
			}
			if s.State() != plugin.StateFailed || !client.Exited() {
				t.Fatalf("cancellation left live session/process: %s", s.State())
			}
		})
	}
}

func TestExistingInterfaceTranslation(t *testing.T) {
	client := fixtureClient(t, hc.ProtocolNetRPC)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	called := false
	backend, err := ConnectInterface(ctx, client, PluginName, func(value interface{}) (plugin.Backend, error) { called = true; return value.(plugin.Backend), nil })
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("interface binding was skipped")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if !client.Exited() {
		t.Fatal("translated interface leaked process")
	}
}

func TestBackendConformance(t *testing.T) {
	t.Setenv("EXT_PLUGIN_CONFORMANCE", "1")
	for _, protocol := range []hc.Protocol{hc.ProtocolGRPC, hc.ProtocolNetRPC} {
		t.Run(string(protocol), func(t *testing.T) {
			plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) { return Connect(ctx, fixtureClient(t, protocol)) })
		})
	}
}
