package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/bridge"
	"github.com/webong/ext/pkg/plugin/inprocess"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/plugintest"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func open(t *testing.T, authorize func(context.Context, plugin.Request) error) *bridge.Relay {
	t.Helper()
	g, err := plugintest.Guest()
	if err != nil {
		t.Fatal(err)
	}
	r, err := bridge.Open(context.Background(), plugintest.Descriptor(), plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: authorize, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return inprocess.New(g) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}
func allow(context.Context, plugin.Request) error { return nil }
func TestConformance(t *testing.T) {
	plugintest.Run(t, func(context.Context) (plugin.Backend, error) { return open(t, allow), nil })
}
func req(op string) plugin.Request {
	d := plugintest.Descriptor()
	return plugin.Request{APIVersion: plugin.APIVersion, ID: "outer-123", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef, Operation: op, Deadline: time.Now().Add(time.Second), Payload: json.RawMessage(`{"value":7}`)}
}
func TestPoliciesAndCorrelation(t *testing.T) {
	deny := atomic.Bool{}
	deny.Store(true)
	r := open(t, func(_ context.Context, in plugin.Request) error {
		if in.ID == "outer-123" {
			t.Error("upstream reused external correlation")
		}
		if deny.Load() {
			return errors.New("private authority detail")
		}
		return nil
	})
	response, err := r.Invoke(context.Background(), req("echo"))
	if err != nil || response.ID != "outer-123" || response.Error.Code != "permission_denied" {
		t.Fatalf("%+v %v", response, err)
	}
	deny.Store(false)
	response, err = r.Invoke(context.Background(), req("public-error"))
	if err != nil || response.Error.Code != "busy" || response.Error.RetryAfterMilliseconds != 10 {
		t.Fatalf("%+v %v", response, err)
	}
	bad := req("echo")
	bad.Plugin.Revision = "changed"
	response, err = r.Invoke(context.Background(), bad)
	if err != nil || response.Error.Code != "invalid_request" {
		t.Fatalf("%+v %v", response, err)
	}
	response, err = r.Invoke(context.Background(), req("echo"))
	if err != nil || response.ID != "outer-123" || string(response.Payload) != `{"value":7}` {
		t.Fatalf("%+v %v", response, err)
	}
}
func TestDisconnectCancelsUpstream(t *testing.T) {
	r := open(t, allow)
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- bridge.ServeJSONLine(context.Background(), server, r) }()
	c := jsonline.NewClient(client)
	if _, err := c.Handshake(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := req("wait")
	b, _ := json.Marshal(request)
	if _, err := client.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnect left upstream alive")
	}
	if _, err := r.Handshake(context.Background()); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
}

type broken struct{ closed atomic.Int32 }

func (*broken) Handshake(context.Context) (plugin.Descriptor, error) {
	return plugintest.Descriptor(), nil
}
func (*broken) Invoke(context.Context, plugin.Request) (plugin.Response, error) {
	return plugin.Response{}, io.ErrUnexpectedEOF
}
func (b *broken) Close() error { b.closed.Add(1); return nil }
func TestTransportFailureIsNotDomainFailure(t *testing.T) {
	b := &broken{}
	r, err := bridge.Open(context.Background(), plugintest.Descriptor(), plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: allow, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }})
	if err != nil {
		t.Fatal(err)
	}
	response, err := r.Invoke(context.Background(), req("echo"))
	if !errors.Is(err, io.ErrUnexpectedEOF) || response.Error != nil {
		t.Fatalf("%+v %v", response, err)
	}
	_ = r.Close()
	if b.closed.Load() != 1 {
		t.Fatal("backend cleanup not singular")
	}
}

type readError struct{}

func (readError) Read([]byte) (int, error)    { return 0, io.ErrUnexpectedEOF }
func (readError) Write(p []byte) (int, error) { return len(p), nil }
func (readError) Close() error                { return nil }
func TestReadFailureIsNotCleanEOF(t *testing.T) {
	if err := bridge.ServeJSONLine(context.Background(), readError{}, open(t, allow)); err == nil {
		t.Fatal("read failure became clean EOF")
	}
}
