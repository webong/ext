package jsonline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
)

func descriptor() plugin.Descriptor {
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example", Revision: "r1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.echo", Version: "v1"}, Operations: []plugin.Operation{{Name: "echo"}}}}}
}

func TestRoundTripAndRemoteErrors(t *testing.T) {
	d := descriptor()
	host, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), server, d, func(_ context.Context, r plugin.Request) (json.RawMessage, error) {
			if string(r.Payload) == `"error"` {
				return nil, &plugin.RemoteError{Code: "rate_limit", Message: "busy", RetryAfterMilliseconds: 2000}
			}
			return r.Payload, nil
		})
	}()
	s, err := plugin.Open(context.Background(), d, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return NewClient(host), nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := json.RawMessage(fmt.Sprintf(`{"value":%d}`, i))
			got, err := s.Call(context.Background(), d.Contracts[0].ContractRef, "echo", payload)
			if err != nil || !bytes.Equal(got, payload) {
				t.Errorf("round trip: %s %v", got, err)
			}
		}(i)
	}
	wg.Wait()
	_, err = s.Call(context.Background(), d.Contracts[0].ContractRef, "echo", json.RawMessage(`"error"`))
	var remote *plugin.RemoteError
	if !errors.As(err, &remote) || remote.Code != "rate_limit" || remote.RetryAfterMilliseconds != 2000 {
		t.Fatal(err)
	}
	if s.State() != plugin.StateReady {
		t.Fatal("domain error broke session")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCancellationClosesConnection(t *testing.T) {
	host, server := net.Pipe()
	defer server.Close()
	client := NewClient(host)
	read := make(chan struct{})
	go func() { _, _ = bufio.NewReader(server).ReadString('\n'); close(read) }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.Handshake(ctx); done <- err }()
	<-read
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := client.Handshake(context.Background()); !errors.Is(err, plugin.ErrClosed) {
		t.Fatal(err)
	}
}

func TestMalformedResponsesCloseClient(t *testing.T) {
	for _, response := range []string{
		`{"apiVersion":"ext.plugin/v1","id":"hello","payload":null}`,
		`{"apiVersion":"ext.plugin/v1","id":"wrong","payload":null}`,
		`{"apiVersion":"ext.plugin/v1","id":"hello","id":"hello","payload":null}`,
		`{"apiVersion":"ext.plugin/v1","id":"hello","payload":null,"error":{"code":"failure","message":"x"}}`,
		`{"apiVersion":"ext.plugin/v1","id":"hello","payload":null,"extra":true}`,
	} {
		t.Run(response, func(t *testing.T) {
			host, server := net.Pipe()
			defer server.Close()
			client := NewClient(host)
			defer client.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = bufio.NewReader(server).ReadString('\n')
				_, _ = io.WriteString(server, response+"\n")
			}()
			if _, err := client.Handshake(context.Background()); err == nil {
				t.Fatal("accepted malformed response")
			}
			<-done
			select {
			case <-client.closed:
			default:
				t.Fatal("connection remained open")
			}
		})
	}
}

func TestFrameBoundsAndFraming(t *testing.T) {
	if _, err := readFrame(bufio.NewReader(strings.NewReader(strings.Repeat("x", plugin.MaxFrameBytes+1) + "\n"))); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := readFrame(bufio.NewReader(strings.NewReader("{}"))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

func TestServerRequiresHandshake(t *testing.T) {
	d := descriptor()
	host, server := net.Pipe()
	defer host.Close()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), server, d, func(context.Context, plugin.Request) (json.RawMessage, error) {
			t.Error("handler called without hello")
			return nil, nil
		})
	}()
	r := plugin.Request{APIVersion: plugin.APIVersion, ID: "1", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef, Operation: "echo", Deadline: time.Now().Add(time.Minute)}
	data, _ := json.Marshal(r)
	if err := writeFrame(host, data); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, plugin.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestServerRejectsExpiredCalls(t *testing.T) {
	d := descriptor()
	host, server := net.Pipe()
	defer host.Close()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), server, d, func(context.Context, plugin.Request) (json.RawMessage, error) {
			t.Error("expired request reached handler")
			return nil, nil
		})
	}()
	client := NewClient(host)
	if _, err := client.Handshake(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := plugin.Request{APIVersion: plugin.APIVersion, ID: "1", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef, Operation: "echo", Deadline: time.Now().Add(-time.Second)}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(host, data); err != nil {
		t.Fatal(err)
	}
	line, err := readFrame(bufio.NewReader(host))
	if err != nil {
		t.Fatal(err)
	}
	var response plugin.Response
	if err := plugin.Decode(line, &response); err != nil {
		t.Fatal(err)
	}
	if err := response.Validate("1"); err != nil || response.Error == nil {
		t.Fatalf("expired request accepted: %s, %v", line, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
