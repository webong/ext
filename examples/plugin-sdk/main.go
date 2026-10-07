// A runnable host/guest template covering typed methods, configuration,
// instance leases, health, streams and a separately authorized host callback.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/author"
	"github.com/webong/ext/pkg/plugin/capability"
	"github.com/webong/ext/pkg/plugin/inprocess"
	"github.com/webong/ext/pkg/plugin/instance"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/schema"
	"github.com/webong/ext/pkg/plugin/stream"
)

type Message struct {
	Message string `json:"message"`
}

func echoMethod() author.Method[Message, Message] {
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"message": {Type: "string", MaxLength: 256}}, Required: []string{"message"}}
	return author.Method[Message, Message]{Contract: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operation: plugin.Operation{Name: "echo", Surface: "observation"}, Input: &s, Output: &s}
}
func main() {
	binding := flag.String("backend", "jsonline", "jsonline or inprocess")
	flag.Parse()
	if err := run(*binding); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(binding string) error {
	if binding != "jsonline" && binding != "inprocess" {
		return plugin.ErrUnsupported
	}
	ctx := context.Background()
	// The example authorizes only compiled-in implementations and operations.
	// Production hosts replace these policies with their own reviewed selection.
	allow := func(context.Context, plugin.Request) error { return nil }
	hostRegistry, _ := author.New(plugin.Identity{ID: "example/host-service", Revision: "compiled-1"})
	if err := author.Register(hostRegistry, echoMethod(), func(_ context.Context, _ plugin.Request, m Message) (Message, error) { return m, nil }); err != nil {
		return err
	}
	hostGuest, err := hostRegistry.Guest(author.Options{Authorize: allow})
	if err != nil {
		return err
	}
	hostBackend, err := inprocess.New(hostGuest)
	if err != nil {
		return err
	}
	hostSession, err := open(ctx, hostRegistry.Descriptor(), hostBackend)
	if err != nil {
		return err
	}
	defer hostSession.Abort()
	configSchema := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"prefix": {Type: "string", MaxLength: 64}}, Required: []string{"prefix"}}
	instances, err := instance.New(instance.Options[string]{Capacity: 4, Validate: func(raw json.RawMessage) error { return configSchema.Check(raw) }, Create: func(_ context.Context, _ string, raw json.RawMessage) (string, func() error, error) {
		var c struct {
			Prefix string `json:"prefix"`
		}
		if err := plugin.Decode(raw, &c); err != nil {
			return "", nil, err
		}
		return c.Prefix, func() error { return nil }, nil
	}})
	if err != nil {
		return err
	}
	defer instances.Close(ctx)
	registry, _ := author.New(plugin.Identity{ID: "example/sdk", Revision: "compiled-1"})
	if err := capability.RegisterConfiguration(registry, instances, configSchema, func(context.Context, plugin.Request, string) (string, error) { return "example-subject/default", nil }); err != nil {
		return err
	}
	if err := author.Register(registry, echoMethod(), func(ctx context.Context, _ plugin.Request, m Message) (Message, error) {
		lease, err := instances.Acquire("example-subject/default")
		if err != nil {
			return Message{}, err
		}
		defer lease.Release()
		m.Message = lease.Value + m.Message
		return author.Call(ctx, hostSession, echoMethod(), m)
	}); err != nil {
		return err
	}
	if err := capability.RegisterHealth(registry, func(context.Context) (capability.Health, error) {
		return capability.Health{Status: "healthy", Ready: true}, nil
	}); err != nil {
		return err
	}
	streams, err := stream.New(stream.Options{Capacity: 4, MaxAge: time.Minute, Scope: func(context.Context, plugin.Request) (string, error) { return "example-subject", nil }, Open: func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) { return &numbers{}, nil }})
	if err != nil {
		return err
	}
	defer streams.Close()
	if err := streams.Register(registry); err != nil {
		return err
	}
	guest, err := registry.Guest(author.Options{Authorize: allow})
	if err != nil {
		return err
	}
	var backend plugin.Backend
	if binding == "inprocess" {
		backend, err = inprocess.New(guest)
	} else {
		a, b := net.Pipe()
		go func() { _ = jsonline.ServeGuest(ctx, b, guest) }()
		backend = jsonline.NewClient(a)
	}
	if err != nil {
		return err
	}
	session, err := open(ctx, registry.Descriptor(), backend)
	if err != nil {
		return err
	}
	defer session.Abort()
	if _, err := author.Call(ctx, session, capability.ConfigurationMethod(), capability.Configuration{Instance: "default", Revision: "config-1", Values: json.RawMessage(`{"prefix":"hello "}`)}); err != nil {
		return err
	}
	output, err := author.Call(ctx, session, echoMethod(), Message{Message: "CTX"})
	if err != nil {
		return err
	}
	fmt.Println(output.Message)
	health, err := author.Call(ctx, session, capability.HealthMethod(), capability.HealthRequest{})
	if err != nil {
		return err
	}
	fmt.Println("health:", health.Status)
	feed, err := stream.Open(ctx, session, json.RawMessage(`{}`))
	if err != nil {
		return err
	}
	defer feed.Close(ctx)
	for {
		batch, err := feed.Read(ctx, 2)
		if err != nil {
			return err
		}
		for _, item := range batch.Items {
			fmt.Println("item:", string(item))
		}
		if batch.Done {
			break
		}
	}
	if err := feed.Close(ctx); err != nil {
		return err
	}
	return session.Close(ctx)
}
func open(ctx context.Context, d plugin.Descriptor, b plugin.Backend) (*plugin.Session, error) {
	return plugin.Open(ctx, d, plugin.Options{Verify: func(_ context.Context, actual plugin.Descriptor) error { return plugin.MatchHandshake(d, actual) }, Authorize: func(_ context.Context, r plugin.Request) error {
		_, err := d.Lookup(r.Contract, r.Operation)
		return err
	}, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }})
}

type numbers struct{ next int }

func (n *numbers) Read(ctx context.Context, limit int) ([]json.RawMessage, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	items := []json.RawMessage{}
	for len(items) < limit && n.next < 3 {
		n.next++
		items = append(items, json.RawMessage(fmt.Sprint(n.next)))
	}
	return items, n.next == 3, nil
}
func (*numbers) Close() error { return nil }
