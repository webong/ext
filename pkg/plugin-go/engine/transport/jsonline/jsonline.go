// Package jsonline binds ext.plugin/v1 to bounded newline-delimited JSON over
// a caller-owned duplex connection (for example, local IPC or process pipes).
// It neither starts processes nor chooses native endpoints.
package jsonline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"io"
	"sync"
	"time"
)

// Connections must allow Close concurrently with Read/Write and unblock both.
// The client owns the supplied connection. One client belongs to one session.
//
// One call is in flight at a time. While it is, the guest may send progress
// frames and reverse requests, and at any time after the handshake it may send
// session-scoped reverse requests; a reader goroutine, started with the first
// call, routes them so a blocked call never stops reverse calls from being
// served.
type Client struct {
	conn     io.ReadWriteCloser
	reader   *bufio.Reader
	gate     chan struct{}
	closed   chan struct{}
	once     sync.Once
	closeErr error

	wmu sync.Mutex // serializes frames written to conn

	mu      sync.Mutex
	pending *pendingCall
	reverse plugin.ReverseHost
	started sync.Once
	failure error
}

type pendingCall struct {
	id       string
	declared bool // the operation declared progress
	sink     plugin.ProgressSink
	result   chan callResult
}

type callResult struct {
	response plugin.Response
	err      error
}

func NewClient(conn io.ReadWriteCloser) *Client {
	return &Client{conn: conn, reader: bufio.NewReader(conn), gate: make(chan struct{}, 1), closed: make(chan struct{})}
}

// SetReverse registers the host that serves the guest's reverse calls. Open
// calls it before the handshake when the host configured Reverse.
func (c *Client) SetReverse(h plugin.ReverseHost) {
	c.mu.Lock()
	c.reverse = h
	c.mu.Unlock()
}

var _ plugin.ReverseBackend = (*Client)(nil)

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
	c.started.Do(func() { go c.readLoop() })
	call := &pendingCall{id: request.ID, declared: request.Progress != nil, sink: plugin.ProgressSinkFrom(ctx), result: make(chan callResult, 1)}
	c.mu.Lock()
	if c.failure != nil {
		err := c.failure
		c.mu.Unlock()
		return plugin.Response{}, err
	}
	c.pending = call
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.pending = nil
		c.mu.Unlock()
	}()
	written := make(chan error, 1)
	go func() { written <- c.write(data) }()
	for written != nil || call.result != nil {
		select {
		case <-ctx.Done():
			_ = c.Close()
			if written != nil {
				<-written // Close must unblock the outstanding write.
			}
			return plugin.Response{}, ctx.Err()
		case err := <-written:
			written = nil
			if err != nil {
				_ = c.Close()
				return plugin.Response{}, err
			}
		case result := <-call.result:
			if err := ctx.Err(); err != nil {
				_ = c.Close()
				return plugin.Response{}, err
			}
			if result.err == nil {
				result.err = result.response.Validate(request.ID)
			}
			if result.err != nil {
				_ = c.Close()
			}
			return result.response, result.err
		}
	}
	return plugin.Response{}, plugin.ErrClosed
}

func (c *Client) write(data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.conn, data)
}

// fail ends the session after a protocol or transport error: the pending call
// receives err and the connection closes, which stops the child.
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.failure == nil {
		c.failure = err
	}
	pending := c.pending
	c.mu.Unlock()
	if pending != nil {
		select {
		case pending.result <- callResult{err: err}:
		default:
		}
	}
	_ = c.Close()
}

func (c *Client) readLoop() {
	for {
		line, err := readFrame(c.reader)
		if err != nil {
			select {
			case <-c.closed:
				err = plugin.ErrClosed
			default:
			}
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			c.fail(err)
			return
		}
		if err := c.route(line); err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *Client) route(line []byte) error {
	kind, err := peekFrame(line)
	if err != nil {
		return err
	}
	switch kind {
	case "":
		var response plugin.Response
		if err := plugin.Decode(line, &response); err != nil {
			return err
		}
		c.mu.Lock()
		pending := c.pending
		c.mu.Unlock()
		if pending == nil || pending.id != response.ID {
			return fmt.Errorf("%w: response to no call in flight", plugin.ErrProtocol)
		}
		select {
		case pending.result <- callResult{response: response}:
		default:
			return fmt.Errorf("%w: second response to one call", plugin.ErrProtocol)
		}
		return nil
	case plugin.FrameProgress:
		var frame plugin.ProgressFrame
		if err := plugin.Decode(line, &frame); err != nil {
			return fmt.Errorf("%w: malformed progress frame", plugin.ErrProtocol)
		}
		if err := frame.Validate(); err != nil {
			return err
		}
		c.mu.Lock()
		pending := c.pending
		c.mu.Unlock()
		if pending == nil || pending.id != frame.Parent || !pending.declared {
			return fmt.Errorf("%w: progress for no call that declared it", plugin.ErrProtocol)
		}
		if pending.sink != nil {
			if _, err := pending.sink.Deliver(frame.Progress); err != nil {
				return err
			}
		}
		return nil
	case plugin.FrameReverseRequest:
		var frame plugin.ReverseRequestFrame
		if err := plugin.Decode(line, &frame); err != nil {
			return fmt.Errorf("%w: malformed reverse request", plugin.ErrProtocol)
		}
		if err := frame.Validate(); err != nil {
			return err
		}
		return c.serveReverse(frame)
	default:
		return fmt.Errorf("%w: unknown frame", plugin.ErrProtocol)
	}
}

func (c *Client) serveReverse(frame plugin.ReverseRequestFrame) error {
	c.mu.Lock()
	host := c.reverse
	c.mu.Unlock()
	if host == nil {
		return c.respond(frame.ID, plugin.ReverseResult{Error: &plugin.RemoteError{Code: plugin.ReverseUnavailable, Message: "reverse call refused"}})
	}
	run, immediate, err := host.AdmitReverse(frame)
	if err != nil {
		return err
	}
	if immediate != nil {
		return c.respond(frame.ID, *immediate)
	}
	go func() {
		if err := c.respond(frame.ID, run()); err != nil {
			c.fail(err)
		}
	}()
	return nil
}

func (c *Client) respond(id string, result plugin.ReverseResult) error {
	data, err := json.Marshal(plugin.ReverseResponseFrame{Frame: plugin.FrameReverseResponse, APIVersion: plugin.APIVersion, ID: id, Payload: result.Payload, Error: result.Error})
	if err != nil {
		return err
	}
	if len(data) > plugin.MaxFrameBytes {
		// An answer too large to send becomes a refusal, never a truncation.
		data, err = json.Marshal(plugin.ReverseResponseFrame{Frame: plugin.FrameReverseResponse, APIVersion: plugin.APIVersion, ID: id, Error: &plugin.RemoteError{Code: plugin.ReverseTooLarge, Message: "reverse call refused"}})
		if err != nil {
			return err
		}
	}
	return c.write(data)
}

func (c *Client) Close() error {
	c.once.Do(func() { close(c.closed); c.closeErr = c.conn.Close() })
	return c.closeErr
}

// peekFrame returns the value of a frame's "frame" field, or "" for a request
// or response, which carry none.
func peekFrame(line []byte) (string, error) {
	var probe struct {
		Frame *string `json:"frame"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return "", fmt.Errorf("%w: malformed frame", plugin.ErrProtocol)
	}
	if probe.Frame == nil {
		return "", nil
	}
	return *probe.Frame, nil
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
