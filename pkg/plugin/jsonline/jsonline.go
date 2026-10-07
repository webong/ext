// Package jsonline binds ext.plugin/v1 to bounded newline-delimited JSON over
// a caller-owned duplex connection (for example, local IPC or process pipes).
// It neither starts processes nor chooses native endpoints.
package jsonline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/webong/ext/pkg/plugin"
)

// Connections must allow Close concurrently with Read/Write and unblock both.
// The client owns the supplied connection. One client belongs to one session.
type Client struct {
	conn     io.ReadWriteCloser
	reader   *bufio.Reader
	gate     chan struct{}
	closed   chan struct{}
	once     sync.Once
	closeErr error
}

func NewClient(conn io.ReadWriteCloser) *Client {
	return &Client{conn: conn, reader: bufio.NewReader(conn), gate: make(chan struct{}, 1), closed: make(chan struct{})}
}

func (c *Client) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	deadline := time.Now().Add(plugin.DefaultTimeout)
	if callerDeadline, ok := ctx.Deadline(); ok {
		deadline = callerDeadline
	}
	r, err := c.exchange(ctx, plugin.Request{APIVersion: plugin.APIVersion, ID: "hello", Operation: "plugin.hello", Deadline: deadline})
	if err != nil {
		return plugin.Descriptor{}, err
	}
	if r.Error != nil {
		_ = c.Close()
		return plugin.Descriptor{}, r.Error
	}
	var d plugin.Descriptor
	if err := plugin.Decode(r.Payload, &d); err != nil {
		_ = c.Close()
		return d, err
	}
	if err := d.Validate(); err != nil {
		_ = c.Close()
		return d, err
	}
	return d, nil
}

func (c *Client) Invoke(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	return c.exchange(ctx, r)
}

func (c *Client) exchange(ctx context.Context, request plugin.Request) (plugin.Response, error) {
	if !request.Deadline.IsZero() {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithDeadline(ctx, request.Deadline)
		defer deadlineCancel()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, plugin.DefaultTimeout)
		defer cancel()
	}
	select {
	case <-ctx.Done():
		return plugin.Response{}, ctx.Err()
	case <-c.closed:
		return plugin.Response{}, plugin.ErrClosed
	case c.gate <- struct{}{}:
	}
	defer func() { <-c.gate }()
	if err := ctx.Err(); err != nil {
		return plugin.Response{}, err
	}
	select {
	case <-c.closed:
		return plugin.Response{}, plugin.ErrClosed
	default:
	}
	data, err := json.Marshal(request)
	if err != nil {
		return plugin.Response{}, err
	}
	if len(data) > plugin.MaxFrameBytes {
		return plugin.Response{}, plugin.ErrInvalid
	}
	type outcome struct {
		response plugin.Response
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		if err := writeFrame(c.conn, data); err != nil {
			done <- outcome{err: err}
			return
		}
		line, err := readFrame(c.reader)
		var response plugin.Response
		if err == nil {
			err = plugin.Decode(line, &response)
		}
		if err == nil {
			err = response.Validate(request.ID)
		}
		done <- outcome{response, err}
	}()
	select {
	case <-ctx.Done():
		_ = c.Close()
		<-done // Close must unblock the outstanding read/write.
		return plugin.Response{}, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			_ = c.Close()
			return plugin.Response{}, err
		}
		if result.err != nil {
			_ = c.Close()
		}
		return result.response, result.err
	}
}

func (c *Client) Close() error {
	c.once.Do(func() { close(c.closed); c.closeErr = c.conn.Close() })
	return c.closeErr
}

// Handler performs consumer authorization and executes one domain call. It
// must honor ctx. Return a RemoteError to expose a deliberate public error;
// other errors are reduced to a stable generic response.
type Handler = plugin.Handler

// Server configures a domain endpoint. MaxCallDuration is a server-owned bound,
// independent of caller deadlines; zero defaults to plugin.DefaultTimeout.
type Server struct {
	Descriptor      plugin.Descriptor
	Handler         Handler
	MaxCallDuration time.Duration
}

// Serve requires hello first and validates every subsequent request against
// the descriptor before calling handler. Calls are sequential. It owns conn;
// cancellation closes it. Domain payloads and error text are never logged.
func Serve(ctx context.Context, conn io.ReadWriteCloser, descriptor plugin.Descriptor, handler Handler) error {
	return (Server{Descriptor: descriptor, Handler: handler}).Serve(ctx, conn)
}

func (s Server) Serve(ctx context.Context, conn io.ReadWriteCloser) error {
	guest, err := plugin.NewGuest(s.Descriptor, plugin.GuestOptions{Handler: s.Handler, MaxCallDuration: s.MaxCallDuration})
	if err != nil {
		_ = conn.Close()
		return err
	}
	return ServeGuest(ctx, conn, guest)
}

// ServeGuest binds a shared guest endpoint to a JSON-line connection. This
// owns only conn; the embedding application owns the endpoint's lifecycle.
func ServeGuest(ctx context.Context, conn io.ReadWriteCloser, guest plugin.Endpoint) error {
	defer conn.Close()
	if guest == nil {
		return plugin.ErrDenied
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	reader := bufio.NewReader(conn)
	hello := false
	for {
		line, err := readFrame(reader)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var request plugin.Request
		if err := plugin.Decode(line, &request); err != nil {
			return err
		}
		response := plugin.Response{APIVersion: plugin.APIVersion, ID: request.ID}
		if !hello {
			if request.APIVersion != plugin.APIVersion || request.ID != "hello" || request.Operation != "plugin.hello" || request.Plugin != (plugin.Identity{}) || request.Contract != (plugin.ContractRef{}) || request.Surface != "" || len(request.Payload) != 0 || request.Deadline.IsZero() {
				return plugin.ErrInvalid
			}
			if !time.Now().Before(request.Deadline) {
				return context.DeadlineExceeded
			}
			var descriptor plugin.Descriptor
			helloCtx, cancel := context.WithDeadline(ctx, request.Deadline)
			descriptor, err = guest.Handshake(helloCtx)
			cancel()
			if err == nil {
				err = descriptor.Validate()
			}
			if err == nil {
				response.Payload, err = json.Marshal(descriptor)
			}
			hello = true
		} else {
			response, err = guest.Invoke(ctx, request)
		}
		if err != nil {
			return err
		}
		if err := response.Validate(request.ID); err != nil {
			return err
		}
		data, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if len(data) > plugin.MaxFrameBytes {
			return plugin.ErrInvalid
		}
		if err := writeFrame(conn, data); err != nil {
			return err
		}
	}
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	var frame []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(frame)+len(fragment) > plugin.MaxFrameBytes+1 {
			return nil, plugin.ErrInvalid
		}
		frame = append(frame, fragment...)
		if err == nil {
			return frame[:len(frame)-1], nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(frame) != 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
}

func writeFrame(writer io.Writer, data []byte) error {
	frame := append(append([]byte(nil), data...), '\n')
	for len(frame) > 0 {
		n, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
