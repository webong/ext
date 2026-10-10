package jsonline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
)

var funcDescriptor = plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/f", Revision: "r1"},
	Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.svc", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}, {Name: "fail"}, {Name: "public"}, {Name: "long"}, {Name: "bad"}}}}}

func callFunc(t *testing.T, options FuncOptions, operation string) (json.RawMessage, error) {
	t.Helper()
	guest, err := NewFuncGuest(funcDescriptor, func(ctx context.Context, method string, payload json.RawMessage) (any, error) {
		switch method {
		case "example.svc.echo":
			return map[string]any{"method": method, "payload": payload}, nil
		case "example.svc.fail":
			return nil, errors.New("disk /secret/path unreadable")
		case "example.svc.public":
			return nil, fmt.Errorf("wrapped: %w", &plugin.RemoteError{Code: "busy", Message: "later", RetryAfterMilliseconds: 7})
		case "example.svc.long":
			return nil, errors.New(strings.Repeat("é", 3000))
		case "example.svc.bad":
			return make(chan int), nil
		}
		return nil, errors.New("unexpected method " + method)
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	go func() { _ = ServeGuest(context.Background(), b, guest) }()
	session, err := plugin.Open(context.Background(), funcDescriptor, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return NewClient(a), nil },
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Abort()
	return session.Call(context.Background(), funcDescriptor.Contracts[0].ContractRef, operation, json.RawMessage(`{"v":1}`))
}

func TestFuncMethodAndResult(t *testing.T) {
	out, err := callFunc(t, FuncOptions{}, "echo")
	if err != nil || string(out) != `{"method":"example.svc.echo","payload":{"v":1}}` {
		t.Fatalf("%s %v", out, err)
	}
}

func TestFuncErrors(t *testing.T) {
	var remote *plugin.RemoteError
	_, err := callFunc(t, FuncOptions{}, "fail")
	if !errors.As(err, &remote) || remote.Code != FailureCode || remote.Message != "disk /secret/path unreadable" {
		t.Fatalf("ordinary error: %v", err)
	}
	_, err = callFunc(t, FuncOptions{RedactErrors: true}, "fail")
	if !errors.As(err, &remote) || remote.Code != "operation_failed" || strings.Contains(remote.Message, "secret") {
		t.Fatalf("redacted error: %v", err)
	}
	_, err = callFunc(t, FuncOptions{}, "public")
	if !errors.As(err, &remote) || remote.Code != "busy" || remote.RetryAfterMilliseconds != 7 {
		t.Fatalf("remote error: %v", err)
	}
	_, err = callFunc(t, FuncOptions{}, "long")
	if !errors.As(err, &remote) || remote.Code != FailureCode || len(remote.Message) > MaxFailureMessageBytes || len(remote.Message)%2 != 0 {
		t.Fatalf("long error must cut on a character boundary: %v (%d bytes)", err, len(remote.Message))
	}
	_, err = callFunc(t, FuncOptions{}, "bad")
	if !errors.As(err, &remote) || remote.Code != FailureCode {
		t.Fatalf("unencodable result: %v", err)
	}
}

func TestFuncRequiresHandler(t *testing.T) {
	if _, err := NewFuncGuest(funcDescriptor, nil, FuncOptions{}); !errors.Is(err, plugin.ErrDenied) {
		t.Fatal(err)
	}
}

func TestTruncateKeepsCharacters(t *testing.T) {
	if got := truncate("aé", 2); got != "a" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("abc", 10); got != "abc" {
		t.Fatal(got)
	}
}
