//go:build ext_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/instance"
	"github.com/webong/ext/pkg/plugin/stream"
)

func TestCallbackErrorCauses(t *testing.T) {
	want := errors.New("private local error")
	for _, stage := range []string{"verify", "connect", "authorize", "invoke"} {
		t.Run(stage, func(t *testing.T) {
			d := testDescriptor()
			b := &testBackend{d: d}
			opts := referenceOptions(b)
			switch stage {
			case "verify":
				opts.Verify = func(context.Context, plugin.Descriptor) error { return want }
			case "connect":
				opts.Connect = func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return nil, want }
			case "authorize":
				opts.Authorize = func(context.Context, plugin.Request) error { return want }
			case "invoke":
				b.invoke = func(context.Context, plugin.Request) (plugin.Response, error) { return plugin.Response{}, want }
			}
			s, err := Open(context.Background(), d, opts)
			if s != nil {
				defer s.Abort()
				_, err = s.Call(context.Background(), d.Contracts[0].ContractRef, "inspect", nil)
			}
			if !errors.Is(err, want) {
				t.Fatalf("lost %s cause: %v", stage, err)
			}
		})
	}
	m, err := NewInstances(instance.Options[int]{Capacity: 1, Validate: func(json.RawMessage) error { return nil }, Create: func(context.Context, string, json.RawMessage) (int, func() error, error) { return 0, nil, want }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	if err = m.Configure(context.Background(), "key", "r1", json.RawMessage("null")); !errors.Is(err, want) {
		t.Fatal(err)
	}
	s, err := NewStreams(stream.Options{Capacity: 1, MaxAge: time.Second, Scope: func(context.Context, plugin.Request) (string, error) { return "scope", nil }, Open: func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) { return nil, want }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.open(context.Background(), plugin.Request{}, stream.OpenRequest{Parameters: json.RawMessage("null")}); !errors.Is(err, want) {
		t.Fatal(err)
	}
}
func TestInstanceDomainValidationKeepsCauseAndInput(t *testing.T) {
	want := errors.New("bad domain config")
	m, err := NewInstances(instance.Options[int]{Capacity: 1, Validate: func(config json.RawMessage) error { config[0] = '7'; return want }, Create: func(context.Context, string, json.RawMessage) (int, func() error, error) {
		t.Fatal("factory admitted invalid config")
		return 0, nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	config := json.RawMessage("null")
	if err = m.Configure(context.Background(), "key", "r1", config); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if string(config) != "null" {
		t.Fatal("caller input mutated")
	}
}
