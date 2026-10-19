package jsonline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// ServeGuest binds a shared guest endpoint to a JSON-line connection. This
// owns only conn; the embedding application owns the endpoint's lifecycle.
//
// A reader goroutine routes frames: requests go to the call loop, which handles
// one call at a time, and reverse responses go to the reverse call that waits
// for them. A handler that blocks on a reverse call therefore never stops the
// reader, so a serialized connection cannot deadlock. Handlers reach the host
// with plugin.ReverseFrom and plugin.SessionReverseFrom, and report progress
// with plugin.ProgressFrom.
func ServeGuest(ctx context.Context, conn io.ReadWriteCloser, guest plugin.Endpoint) error {
	defer conn.Close()
	if guest == nil {
		return plugin.ErrDenied
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	s := &serveState{conn: conn, waiting: map[string]chan plugin.ReverseResponseFrame{}, gone: make(chan struct{}), requests: make(chan plugin.Request, 1)}
	go s.read()
	return s.loop(ctx, guest)
}

type serveState struct {
	conn io.ReadWriteCloser
	wmu  sync.Mutex // serializes frames written to conn

	mu       sync.Mutex
	busy     bool // a forward request is being handled
	issued   uint64
	waiting  map[string]chan plugin.ReverseResponseFrame
	readErr  error
	gone     chan struct{} // closed when the reader stops
	requests chan plugin.Request
	desc     plugin.Descriptor
	hello    bool
}

func (s *serveState) write(frame any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) > plugin.MaxFrameBytes {
		return plugin.ErrInvalid
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return writeFrame(s.conn, data)
}

func (s *serveState) read() {
	reader := bufio.NewReader(s.conn)
	var err error
	defer func() {
		s.mu.Lock()
		s.readErr = err
		s.mu.Unlock()
		close(s.gone)
		close(s.requests)
	}()
	for {
		var line []byte
		if line, err = readFrame(reader); err != nil {
			return
		}
		var kind string
		if kind, err = peekFrame(line); err != nil {
			return
		}
		switch kind {
		case "":
			var request plugin.Request
			if err = plugin.Decode(line, &request); err != nil {
				return
			}
			s.mu.Lock()
			if s.busy {
				s.mu.Unlock()
				err = fmt.Errorf("%w: request while a call is in flight", plugin.ErrProtocol)
				return
			}
			s.busy = true
			s.mu.Unlock()
			s.requests <- request // capacity one, and busy guards it
		case plugin.FrameReverseResponse:
			var frame plugin.ReverseResponseFrame
			if err = plugin.Decode(line, &frame); err != nil {
				err = fmt.Errorf("%w: malformed reverse response", plugin.ErrProtocol)
				return
			}
			if err = s.deliver(frame); err != nil {
				return
			}
		default:
			err = fmt.Errorf("%w: unexpected frame", plugin.ErrProtocol)
			return
		}
	}
}

// deliver hands a reverse response to its waiter. A response for a call that
// was already abandoned is dropped; one for an id never issued is a violation.
func (s *serveState) deliver(frame plugin.ReverseResponseFrame) error {
	s.mu.Lock()
	ch, ok := s.waiting[frame.ID]
	delete(s.waiting, frame.ID)
	issued := s.issued
	s.mu.Unlock()
	if ok {
		if err := frame.Validate(frame.ID); err != nil {
			return err
		}
		ch <- frame // capacity one
		return nil
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(frame.ID, "g"), 10, 64)
	if !strings.HasPrefix(frame.ID, "g") || err != nil || n == 0 || n > issued {
		return fmt.Errorf("%w: reverse response to no call", plugin.ErrProtocol)
	}
	return nil
}

func (s *serveState) loop(ctx context.Context, guest plugin.Endpoint) error {
	for request := range s.requests {
		response := plugin.Response{APIVersion: plugin.APIVersion, ID: request.ID}
		var err error
		if !s.hello {
			if request.APIVersion != plugin.APIVersion || request.ID != "hello" || request.Operation != "plugin.hello" || request.Plugin != (plugin.Identity{}) || request.Contract != (plugin.ContractRef{}) || request.Surface != "" || len(request.Payload) != 0 || request.Deadline.IsZero() || request.Progress != nil || request.Parent != "" {
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
			s.mu.Lock()
			s.desc, s.hello = descriptor, true
			s.mu.Unlock()
		} else {
			response, err = s.invoke(ctx, guest, request)
		}
		if err != nil {
			return err
		}
		if err := response.Validate(request.ID); err != nil {
			return err
		}
		s.mu.Lock()
		s.busy = false // before the response, so the next request is never early
		s.mu.Unlock()
		if err := s.write(response); err != nil {
			return err
		}
	}
	s.mu.Lock()
	err := s.readErr
	s.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// invoke runs one forward call with its reverse callers and progress reporter,
// and cancels the handler if the connection ends under it.
func (s *serveState) invoke(ctx context.Context, guest plugin.Endpoint, request plugin.Request) (plugin.Response, error) {
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watched := callCtx.Done() // the goroutine must not read callCtx, which is reassigned below
	go func() {
		select {
		case <-s.gone:
			cancel()
		case <-watched:
		}
	}()
	s.mu.Lock()
	descriptor := s.desc
	s.mu.Unlock()
	callCtx = plugin.WithReverse(callCtx, wireReverse{s: s, descriptor: descriptor, parent: request.ID})
	callCtx = plugin.WithSessionReverse(callCtx, wireReverse{s: s, descriptor: descriptor})
	reporter := &reporter{s: s, parent: request.ID}
	if op, err := descriptor.Lookup(request.Contract, request.Operation); err == nil && op.Progress && request.Progress != nil {
		reporter.limiter = plugin.NewLimiter(*request.Progress, nil)
	}
	callCtx = plugin.WithProgressReporter(callCtx, reporter)
	response, err := guest.Invoke(callCtx, request)
	reporter.finish() // no event may follow the response
	return response, err
}

// wireReverse is the plugin's side of a reverse call.
type wireReverse struct {
	s          *serveState
	descriptor plugin.Descriptor
	parent     string // empty for session scope
}

func (r wireReverse) Call(ctx context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	if _, err := r.descriptor.LookupReverse(ref, operation); err != nil {
		return nil, &plugin.RemoteError{Code: plugin.ReverseUndeclared, Message: "reverse call refused"}
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(plugin.DefaultTimeout)
	}
	s := r.s
	s.mu.Lock()
	s.issued++
	id := "g" + strconv.FormatUint(s.issued, 10)
	reply := make(chan plugin.ReverseResponseFrame, 1)
	s.waiting[id] = reply
	s.mu.Unlock()
	abandon := func() {
		s.mu.Lock()
		delete(s.waiting, id)
		s.mu.Unlock()
	}
	frame := plugin.ReverseRequestFrame{Frame: plugin.FrameReverseRequest, APIVersion: plugin.APIVersion, ID: id, Parent: r.parent, Contract: ref, Operation: operation, Deadline: deadline, Payload: payload}
	if err := frame.Validate(); err != nil {
		abandon()
		return nil, plugin.ErrInvalid
	}
	if err := s.write(frame); err != nil {
		abandon()
		return nil, err
	}
	select {
	case response := <-reply:
		if response.Error != nil {
			e := *response.Error
			return nil, &e
		}
		return append(json.RawMessage(nil), response.Payload...), nil
	case <-ctx.Done():
		abandon()
		return nil, ctx.Err()
	case <-s.gone:
		abandon()
		return nil, plugin.ErrClosed
	}
}

// reporter sends the progress frames of one call.
type reporter struct {
	s       *serveState
	parent  string
	limiter *plugin.Limiter // nil when the operation did not declare progress

	mu       sync.Mutex
	finished bool
}

func (r *reporter) Report(p plugin.Progress) error {
	if r.limiter == nil {
		return plugin.ErrUnsupported
	}
	p.Dropped = 0
	if err := p.Validate(); err != nil {
		return plugin.ErrInvalid
	}
	dropped, ok := r.limiter.Admit(p)
	if !ok {
		return plugin.ErrProgressDropped
	}
	p.Dropped = dropped
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return plugin.ErrClosed
	}
	return r.s.write(plugin.ProgressFrame{Frame: plugin.FrameProgress, APIVersion: plugin.APIVersion, Parent: r.parent, Progress: p})
}

// finish ends reporting, after sending a marker for events never reported.
func (r *reporter) finish() {
	if r.limiter == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.finished = true
	if n := r.limiter.Pending(); n > 0 {
		_ = r.s.write(plugin.ProgressFrame{Frame: plugin.FrameProgress, APIVersion: plugin.APIVersion, Parent: r.parent, Progress: plugin.Progress{Kind: "log", Dropped: n}})
	}
}
