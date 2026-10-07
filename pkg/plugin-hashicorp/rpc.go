package hashicorp

import (
	"context"
	"encoding/json"
	"net/rpc"
	"sync"
	"time"

	"github.com/webong/ext/pkg/plugin"
)

type hello struct {
	Deadline time.Time `json:"deadline"`
}
type rpcServer struct{ guest plugin.Endpoint }

func (s *rpcServer) Handshake(data []byte, reply *[]byte) error {
	var request hello
	if err := plugin.Decode(data, &request); err != nil {
		return plugin.ErrInvalid
	}
	if request.Deadline.IsZero() {
		return plugin.ErrInvalid
	}
	ctx, cancel := context.WithDeadline(context.Background(), request.Deadline)
	defer cancel()
	d, err := s.guest.Handshake(ctx)
	if err != nil {
		return err
	}
	*reply, err = encode(d)
	return err
}

func (s *rpcServer) Invoke(data []byte, reply *[]byte) error {
	var request plugin.Request
	if err := plugin.Decode(data, &request); err != nil {
		return plugin.ErrInvalid
	}
	response, err := s.guest.Invoke(context.Background(), request)
	if err != nil {
		return plugin.ErrInvalid
	}
	*reply, err = encode(response)
	return err
}

type rpcBackend struct {
	client *rpc.Client
	once   sync.Once
	err    error
}

func (b *rpcBackend) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	ctx, cancel := bounded(ctx, time.Time{})
	defer cancel()
	deadline, _ := ctx.Deadline()
	var descriptor plugin.Descriptor
	err := b.call(ctx, "Plugin.Handshake", hello{Deadline: deadline}, &descriptor)
	if err == nil {
		err = descriptor.Validate()
	}
	return descriptor, err
}

func (b *rpcBackend) Invoke(ctx context.Context, request plugin.Request) (plugin.Response, error) {
	ctx, cancel := bounded(ctx, request.Deadline)
	defer cancel()
	var response plugin.Response
	err := b.call(ctx, "Plugin.Invoke", request, &response)
	if err == nil {
		err = response.Validate(request.ID)
	}
	return response, err
}

func (b *rpcBackend) call(ctx context.Context, method string, request, response interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := encode(request)
	if err != nil {
		return err
	}
	var reply []byte
	done := make(chan error, 1)
	// net/rpc has no context API. Run both its potentially blocking send and
	// receive in this goroutine; cancellation closes this dispensed RPC stream.
	go func() { done <- b.client.Call(method, data, &reply) }()
	select {
	case <-ctx.Done():
		_ = b.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return plugin.Decode(reply, response)
	}
}

func (b *rpcBackend) Close() error { b.once.Do(func() { b.err = b.client.Close() }); return b.err }

func encode(value interface{}) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > plugin.MaxFrameBytes {
		return nil, plugin.ErrInvalid
	}
	return data, nil
}

func bounded(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	if deadline.IsZero() {
		if existing, ok := ctx.Deadline(); ok {
			deadline = existing
		} else {
			deadline = time.Now().Add(plugin.DefaultTimeout)
		}
	}
	return context.WithDeadline(ctx, deadline)
}
