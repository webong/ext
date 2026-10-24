package crosslang_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/author"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/capability"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/stream"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	"github.com/webong/ext/pkg/plugin-go/contract/validate/schema"
	"github.com/webong/ext/pkg/plugin-go/wasm"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// capabilityDescriptor is what a guest serving the optional capability contracts declares, with the
// behavior the guests under test share: health reports degraded and not ready; an update is
// echoed, except that revision "reject" is refused with the public code invalid_config; a stream
// opens as "s1" and yields the items 1 and 2, then ends, reading in sequence; close succeeds.
func capabilityDescriptor() plugin.Descriptor {
	ref := func(name string) plugin.ContractRef { return plugin.ContractRef{Name: name, Version: "v1"} }
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/capabilities", Revision: "1"}, Contracts: []plugin.Contract{
		{ContractRef: ref("ext.health"), Operations: []plugin.Operation{{Name: "check", Surface: "observation"}}},
		{ContractRef: ref("ext.configuration"), Operations: []plugin.Operation{{Name: "update", Surface: "configuration"}}},
		{ContractRef: ref("ext.stream"), Operations: []plugin.Operation{{Name: "open"}, {Name: "read"}, {Name: "close"}}},
	}}
}

// TestCapabilityGuests calls the capability contracts of a guest with the Go library's own methods
// and stream client, which is what a Go host does with a guest in another language. A language joins
// by setting EXT_<NAME>_CAPABILITY_GUEST to a process that serves capabilityDescriptor (or
// EXT_<NAME>_CAPABILITY_WASM to a WASI command that does), and is required by listing it, as ZIG or
// ZIG_WASI, in EXT_CAPABILITY_REQUIRED (separated by spaces or commas).
func TestCapabilityGuests(t *testing.T) {
	required := func(name string) bool {
		for _, r := range strings.FieldsFunc(os.Getenv("EXT_CAPABILITY_REQUIRED"), func(c rune) bool { return c == ' ' || c == ',' }) {
			if r == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"ZIG"} {
		t.Run(name, func(t *testing.T) {
			variable := name + "_CAPABILITY_GUEST"
			if os.Getenv("EXT_"+variable) == "" && !required(name) {
				t.Skip("set EXT_" + variable + " to a guest process that serves the capability contracts")
			}
			path := artifact(t, variable)
			runCapability(t, func(context.Context) (plugin.Backend, error) { return process(path) })
		})
		t.Run(name+"_WASI", func(t *testing.T) {
			variable := name + "_CAPABILITY_WASM"
			if os.Getenv("EXT_"+variable) == "" && !required(name+"_WASI") {
				t.Skip("set EXT_" + variable + " to a WASI command that serves the capability contracts")
			}
			data, err := os.ReadFile(artifact(t, variable))
			if err != nil {
				t.Fatal(err)
			}
			runCapability(t, func(ctx context.Context) (plugin.Backend, error) {
				b, err := wasm.Open(ctx, data, wasm.Options{})
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() {
					_ = b.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
					defer cancel()
					if err := b.Wait(ctx); ctx.Err() != nil {
						t.Errorf("WASI cleanup: %v", err)
					}
				})
				return b, nil
			})
		})
	}
}

func runCapability(t *testing.T, connect func(context.Context) (plugin.Backend, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := plugin.Open(ctx, capabilityDescriptor(), plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Connect:   func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) { return connect(ctx) },
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Abort() })

	t.Run("health", func(t *testing.T) {
		health, err := author.Call(ctx, session, capability.HealthMethod(), capability.HealthRequest{})
		if err != nil || health != (capability.Health{Status: "degraded", Ready: false}) {
			t.Fatalf("health = %+v, %v", health, err)
		}
	})
	t.Run("configuration", func(t *testing.T) {
		configured, err := author.Call(ctx, session, capability.ConfigurationMethod(), capability.Configuration{Instance: "one", Revision: "r1", Values: json.RawMessage(`{"name":"a"}`)})
		if err != nil || configured != (capability.Configured{Instance: "one", Revision: "r1"}) {
			t.Fatalf("configured = %+v, %v", configured, err)
		}
		long := strings.Repeat("x", 256)
		configured, err = author.Call(ctx, session, capability.ConfigurationMethod(), capability.Configuration{Instance: long, Revision: strings.Repeat("r", 256), Values: json.RawMessage(`null`)})
		if err != nil || configured.Instance != long {
			t.Fatalf("configured at the limit = %+v, %v", configured, err)
		}
		_, err = author.Call(ctx, session, capability.ConfigurationMethod(), capability.Configuration{Instance: "one", Revision: "reject", Values: json.RawMessage(`{}`)})
		var remote *plugin.RemoteError
		if !errors.As(err, &remote) || remote.Code != "invalid_config" {
			t.Fatalf("a refused configuration = %v", err)
		}
	})
	// What the Go library would not decode or accept is refused by the guest too, and the guest
	// stays up for the next call. A raw call goes around the host's own checks.
	t.Run("malformed requests", func(t *testing.T) {
		refused := func(contract, operation, payload string) {
			t.Helper()
			_, err := session.Call(ctx, plugin.ContractRef{Name: contract, Version: "v1"}, operation, json.RawMessage(payload))
			var remote *plugin.RemoteError
			if !errors.As(err, &remote) {
				t.Errorf("%s/%s %s: %v", contract, operation, payload, err)
			}
		}
		refused("ext.health", "check", `{"x":1}`)
		refused("ext.configuration", "update", `{"instance":"","revision":"r1","values":{}}`)
		refused("ext.configuration", "update", `{"instance":"one","revision":"","values":{}}`)
		refused("ext.configuration", "update", `{"instance":"one","revision":"r1"}`)
		refused("ext.configuration", "update", `{"instance":"one","revision":"r1","values":{},"extra":1}`)
		refused("ext.configuration", "update", `{"instance":"`+strings.Repeat("x", 257)+`","revision":"r1","values":{}}`)
		refused("ext.configuration", "update", `{"instance":1,"revision":"r1","values":{}}`)
		health, err := author.Call(ctx, session, capability.HealthMethod(), capability.HealthRequest{})
		if err != nil || health.Status != "degraded" {
			t.Fatalf("the guest did not survive: %+v, %v", health, err)
		}
	})
	t.Run("stream", func(t *testing.T) {
		client, err := stream.Open(ctx, session, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		// None of these may reach the guest's handler: it counts the reads it accepts, so the first
		// real read below would then carry the wrong sequence.
		for _, payload := range []string{
			`{"id":"s1","sequence":1.0,"limit":1}`,
			`{"id":"s1","sequence":1,"limit":1e0}`,
			`{"id":"s1","sequence":0,"limit":1}`,
			`{"id":"s1","sequence":1,"limit":0}`,
			`{"id":"s1","sequence":1,"limit":-1}`,
			`{"id":"s1","sequence":1,"limit":257}`,
			`{"id":"","sequence":1,"limit":1}`,
			`{"id":"` + strings.Repeat("i", 65) + `","sequence":1,"limit":1}`,
			`{"id":"s1","sequence":true,"limit":1}`,
			`{"id":"s1","sequence":"1","limit":1}`,
			`{"id":"s1","sequence":18446744073709551616,"limit":1}`,
			`{"id":"s1","sequence":1,"limit":1,"extra":1}`,
			`{"id":"s1","sequence":1}`,
		} {
			_, err := session.Call(ctx, stream.Contract(), "read", json.RawMessage(payload))
			var remote *plugin.RemoteError
			if !errors.As(err, &remote) {
				t.Errorf("read %s: %v", payload, err)
			}
		}
		first, err := client.Read(ctx, 5)
		if err != nil || first.Done || !reflect.DeepEqual(rawItems(first.Items), []string{"1", "2"}) {
			t.Fatalf("first batch = %v, done %v, %v", rawItems(first.Items), first.Done, err)
		}
		second, err := client.Read(ctx, 1)
		if err != nil || !second.Done || !reflect.DeepEqual(rawItems(second.Items), []string{"1"}) {
			t.Fatalf("second batch = %v, done %v, %v", rawItems(second.Items), second.Done, err)
		}
		after, err := client.Read(ctx, 1)
		if err != nil || !after.Done || len(after.Items) != 0 {
			t.Fatalf("after the end = %+v, %v", after, err)
		}
		if err := client.Close(ctx); err != nil {
			t.Fatal(err)
		}
		// A read out of sequence is refused by the guest with its own public code.
		if _, err := stream.Open(ctx, session, json.RawMessage(`null`)); err != nil {
			t.Fatal(err)
		}
		_, err = session.Call(ctx, stream.Contract(), "read", json.RawMessage(`{"id":"s1","sequence":5,"limit":1}`))
		var remote *plugin.RemoteError
		if !errors.As(err, &remote) || remote.Code != "sequence" {
			t.Fatalf("a read out of sequence = %v", err)
		}
	})
	if got := session.State(); got != plugin.StateReady {
		t.Fatalf("session state = %s", got)
	}
}

func rawItems(items []json.RawMessage) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = string(item)
	}
	return out
}

// capabilityGuestGo serves the capability contracts with the Go library's own services: health
// reports degraded; an update is checked against a schema that requires "name" and applied to a
// configurer that accepts it; streams read the numbers 1 to 3.
func capabilityGuestGo(t *testing.T) *plugin.Guest {
	t.Helper()
	registry, err := author.New(plugin.Identity{ID: "example/capabilities", Revision: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := capability.RegisterHealth(registry, func(context.Context) (capability.Health, error) {
		return capability.Health{Status: "degraded", Ready: false}, nil
	}); err != nil {
		t.Fatal(err)
	}
	config := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"name": {Type: "string"}}, Required: []string{"name"}}
	if err := capability.RegisterConfiguration(registry, acceptor{}, config, func(_ context.Context, _ plugin.Request, instance string) (string, error) {
		return "tenant/" + instance, nil
	}); err != nil {
		t.Fatal(err)
	}
	service, err := stream.New(stream.Options{Capacity: 4, MaxAge: time.Minute,
		Scope: func(context.Context, plugin.Request) (string, error) { return "tenant", nil },
		Open: func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) {
			return &numbers{}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if err := service.Register(registry); err != nil {
		t.Fatal(err)
	}
	guest, err := registry.Guest(author.Options{Authorize: func(context.Context, plugin.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	return guest
}

type acceptor struct{}

func (acceptor) Configure(context.Context, string, string, json.RawMessage) error { return nil }

type numbers struct{ next int }

func (n *numbers) Read(_ context.Context, limit int) ([]json.RawMessage, bool, error) {
	var items []json.RawMessage
	for len(items) < limit && n.next < 3 {
		n.next++
		items = append(items, json.RawMessage(strconv.Itoa(n.next)))
	}
	return items, n.next >= 3, nil
}
func (n *numbers) Close() error { return nil }

// TestCapabilityHosts runs a foreign host that calls the capability contracts of a guest served by
// the Go library, with the host's own methods and stream client: the other direction of
// TestCapabilityGuests.
func TestCapabilityHosts(t *testing.T) {
	host := artifact(t, "ZIG_CAPABILITY_HOST")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, host)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	conn := &duplex{Reader: out, Writer: in, read: out, write: in}
	if err := jsonline.ServeGuest(ctx, conn, capabilityGuestGo(t)); err != nil {
		t.Error(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("foreign host: %v\n%s", err, stderr.String())
	}
}
