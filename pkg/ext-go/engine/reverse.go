package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Frame values for the tagged frames that share a connection with requests and
// responses. A frame with no "frame" field is a request or response as before.
const (
	FrameReverseRequest  = "reverse_request"
	FrameReverseResponse = "reverse_response"
	FrameProgress        = "progress"
)

// Stable codes of the errors a reverse call fails with. They let a guest tell a
// feature that is not offered (fall back) from one that is forbidden, and
// debug a missing declaration.
const (
	// ReverseUndeclared: the plugin's descriptor does not list the operation in
	// Reverse. Fix the descriptor.
	ReverseUndeclared = "reverse_undeclared"
	// ReverseUnavailable: the host offers no reverse services, or not yet.
	ReverseUnavailable = "reverse_unavailable"
	// ReverseDenied: the host's Authorize refused this call.
	ReverseDenied   = "denied"
	ReverseTooLarge = "too_large"
	ReverseBusy     = "busy"
	ReverseExpired  = "expired"
)

// Bounds and defaults for reverse calls and progress.
const (
	DefaultReversePayloadBytes = 1 << 20
	DefaultReverseInFlight     = 4
	MaxProgressTextBytes       = 2048
	DefaultProgressEvents      = 50
	DefaultProgressBytes       = 64 << 10
)

var (
	// ErrProgressDropped tells a reporter that its event was dropped by the
	// rate limit. It is informational: the call continues, and the next
	// delivered event carries the count.
	ErrProgressDropped = errors.New("plugin progress event dropped")
	// ErrProtocol marks a peer violating the framing rules. A session that
	// sees one fails and is not reusable.
	ErrProtocol = errors.New("plugin protocol violation")
)

// publicError returns err's RemoteError when it is one a peer will accept, and
// the generic failure otherwise. A handler's own RemoteError is copied into a
// frame, and a frame whose error has an invalid code or an over-long message is
// a protocol violation for the receiver, which would end its connection: one
// handler bug must not take the connection down.
func publicError(remote *RemoteError, generic string) *RemoteError {
	copied := *remote
	if (Response{APIVersion: APIVersion, ID: "x", Error: &copied}).Validate("x") != nil {
		return &RemoteError{Code: "operation_failed", Message: generic}
	}
	return &copied
}

// ReverseCaller calls an operation the host offers. It has the method set of
// author.Caller, so author.Call works with it unchanged.
type ReverseCaller interface {
	Call(context.Context, ContractRef, string, json.RawMessage) (json.RawMessage, error)
}

type reverseKey struct{}
type sessionReverseKey struct{}
type reporterKey struct{}
type sinkKey struct{}

// WithReverse attaches the caller for the call in flight; ReverseFrom reads it.
func WithReverse(ctx context.Context, c ReverseCaller) context.Context {
	return context.WithValue(ctx, reverseKey{}, c)
}

// ReverseFrom returns the caller for reverse calls scoped to the current call,
// or nil when the host offers none.
func ReverseFrom(ctx context.Context) ReverseCaller {
	c, _ := ctx.Value(reverseKey{}).(ReverseCaller)
	return c
}

// WithSessionReverse attaches the caller for session-scoped reverse calls.
func WithSessionReverse(ctx context.Context, c ReverseCaller) context.Context {
	return context.WithValue(ctx, sessionReverseKey{}, c)
}

// SessionReverseFrom returns the caller for session-scoped reverse calls, valid
// until the connection closes, so background work can keep it after the call
// that obtained it returns. It is nil when the host offers none.
func SessionReverseFrom(ctx context.Context) ReverseCaller {
	c, _ := ctx.Value(sessionReverseKey{}).(ReverseCaller)
	return c
}

// ReverseRequestFrame is a call from a plugin to its host.
type ReverseRequestFrame struct {
	Frame      string          `json:"frame"`
	APIVersion string          `json:"apiVersion"`
	ID         string          `json:"id"`
	Parent     string          `json:"parent,omitempty"`
	Contract   ContractRef     `json:"contract"`
	Operation  string          `json:"operation"`
	Deadline   time.Time       `json:"deadline"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// Validate checks the frame's shape. A failure is a protocol violation.
func (f ReverseRequestFrame) Validate() error {
	if f.Frame != FrameReverseRequest || f.APIVersion != APIVersion || !revision.MatchString(f.ID) || f.Deadline.IsZero() ||
		(f.Parent != "" && !revision.MatchString(f.Parent)) || f.Contract.Validate() != nil || !identifier.MatchString(f.Operation) {
		return fmt.Errorf("%w: malformed reverse request", ErrProtocol)
	}
	if len(f.Payload) != 0 {
		var raw json.RawMessage
		if err := Decode(f.Payload, &raw); err != nil {
			return fmt.Errorf("%w: malformed reverse payload", ErrProtocol)
		}
	}
	return nil
}

// ReverseResponseFrame answers one ReverseRequestFrame.
type ReverseResponseFrame struct {
	Frame      string          `json:"frame"`
	APIVersion string          `json:"apiVersion"`
	ID         string          `json:"id"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      *RemoteError    `json:"error,omitempty"`
}

// Validate checks the frame against the request id it must answer.
func (f ReverseResponseFrame) Validate(id string) error {
	if f.Frame != FrameReverseResponse {
		return fmt.Errorf("%w: malformed reverse response", ErrProtocol)
	}
	if err := (Response{APIVersion: f.APIVersion, ID: f.ID, Payload: f.Payload, Error: f.Error}).Validate(id); err != nil {
		return fmt.Errorf("%w: malformed reverse response", ErrProtocol)
	}
	return nil
}

// Progress is one progress or log event of a call.
type Progress struct {
	// Kind is "progress" or "log".
	Kind string `json:"kind"`
	// Stream is "", "out" or "err" and tells a program's standard streams apart
	// for log events.
	Stream    string `json:"stream,omitempty"`
	Text      string `json:"text,omitempty"`
	Completed uint64 `json:"completed,omitempty"`
	Total     uint64 `json:"total,omitempty"`
	// Dropped counts events lost to the rate limit since the previous delivered
	// event.
	Dropped uint64 `json:"dropped,omitempty"`
}

// Validate checks an event's shape.
func (p Progress) Validate() error {
	if (p.Kind != "progress" && p.Kind != "log") || (p.Stream != "" && p.Stream != "out" && p.Stream != "err") ||
		(p.Stream != "" && p.Kind != "log") || len(p.Text) > MaxProgressTextBytes || !utf8.ValidString(p.Text) ||
		(p.Total != 0 && p.Completed > p.Total) {
		return fmt.Errorf("%w: malformed progress event", ErrProtocol)
	}
	return nil
}

// SplitProgressText cuts text into pieces that each fit one event: at most
// MaxProgressTextBytes bytes and valid UTF-8. A piece never ends inside a
// multi-byte character, and bytes that are not valid UTF-8 are replaced with
// U+FFFD, because an event carrying either is a protocol violation. Splitting a
// longer line into events, and where, is the reporter's job; this only keeps each
// piece legal. It returns nil for empty text.
func SplitProgressText(text string) []string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	var pieces []string
	for len(text) > MaxProgressTextBytes {
		cut := MaxProgressTextBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		pieces = append(pieces, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		pieces = append(pieces, text)
	}
	return pieces
}

// ProgressFrame carries one event of the call named by Parent.
type ProgressFrame struct {
	Frame      string `json:"frame"`
	APIVersion string `json:"apiVersion"`
	Parent     string `json:"parent"`
	Progress
}

// Validate checks the frame's shape.
func (f ProgressFrame) Validate() error {
	if f.Frame != FrameProgress || f.APIVersion != APIVersion || !revision.MatchString(f.Parent) {
		return fmt.Errorf("%w: malformed progress frame", ErrProtocol)
	}
	return f.Progress.Validate()
}

// ProgressLimits is the rate at which a call may report. Zero fields use the
// defaults. It travels in the request so both ends apply the same limits.
type ProgressLimits struct {
	EventsPerSecond int `json:"eventsPerSecond,omitempty"`
	BytesPerSecond  int `json:"bytesPerSecond,omitempty"`
}

// Resolved fills in defaults.
func (l ProgressLimits) Resolved() ProgressLimits {
	if l.EventsPerSecond <= 0 {
		l.EventsPerSecond = DefaultProgressEvents
	}
	if l.BytesPerSecond <= 0 {
		l.BytesPerSecond = DefaultProgressBytes
	}
	return l
}

// Validate bounds explicit limits.
func (l ProgressLimits) Validate() error {
	if l.EventsPerSecond < 0 || l.BytesPerSecond < 0 || l.EventsPerSecond > 1<<20 || l.BytesPerSecond > 1<<30 {
		return fmt.Errorf("%w: progress limits out of range", ErrInvalid)
	}
	return nil
}

// ProgressReporter sends events from inside a call.
type ProgressReporter interface {
	// Report sends one event. What each error means for the call:
	//
	//	nil                  sent
	//	ErrProgressDropped   the rate limit dropped it and counted it; the call
	//	                     is fine, so treat it as sent (or slow down)
	//	ErrUnsupported       the operation did not declare progress, so no event
	//	                     will ever flow: stop reporting and return the text in
	//	                     the result instead
	//	ErrInvalid           the event breaks the rules (kind, stream, text bound
	//	                     or UTF-8); the caller's bug, nothing was sent
	//	ErrClosed            the call is over, or the connection closed
	//	any other error      the connection is failing; the call will fail
	//
	// Only ErrProgressDropped is routine. An operation that declared progress
	// is always given a working reporter, whether or not the host asked for the
	// events: a host that calls it with plain Call simply discards them.
	Report(Progress) error
}

type limitsKey struct{}

// WithProgressLimits attaches the resolved event rate of the call being
// handled; pass nil for a call that has none.
func WithProgressLimits(ctx context.Context, limits *ProgressLimits) context.Context {
	return context.WithValue(ctx, limitsKey{}, limits)
}

// ProgressLimitsFrom returns the event rate the host set for the call being
// handled, with defaults filled in. A handler that streams a long output can
// coalesce lines to stay inside it instead of letting the rate limit drop
// events. ok is false when the operation did not declare progress.
func ProgressLimitsFrom(ctx context.Context) (limits ProgressLimits, ok bool) {
	l, _ := ctx.Value(limitsKey{}).(*ProgressLimits)
	if l == nil {
		return ProgressLimits{}, false
	}
	return l.Resolved(), true
}

type noReporter struct{}

func (noReporter) Report(Progress) error { return ErrUnsupported }

// WithProgressReporter attaches the reporter for the call being handled.
func WithProgressReporter(ctx context.Context, r ProgressReporter) context.Context {
	return context.WithValue(ctx, reporterKey{}, r)
}

// ProgressFrom returns the reporter for the call being handled. It never
// returns nil: without one, Report returns ErrUnsupported.
func ProgressFrom(ctx context.Context) ProgressReporter {
	if r, ok := ctx.Value(reporterKey{}).(ProgressReporter); ok && r != nil {
		return r
	}
	return noReporter{}
}

// ProgressSink receives the events of one call on the host side.
type ProgressSink interface {
	// Deliver passes one event on. delivered is false when the rate limit
	// dropped it. An error is a protocol violation.
	Deliver(Progress) (delivered bool, err error)
}

// WithProgressSink attaches the sink for the call being made.
func WithProgressSink(ctx context.Context, s ProgressSink) context.Context {
	return context.WithValue(ctx, sinkKey{}, s)
}

// ProgressSinkFrom returns the sink for the call being made, or nil.
func ProgressSinkFrom(ctx context.Context) ProgressSink {
	s, _ := ctx.Value(sinkKey{}).(ProgressSink)
	return s
}

// bucket is a token bucket counted in events and bytes with a burst of twice
// the per-second rate.
type bucket struct {
	events, bytes       float64
	eventRate, byteRate float64
	last                time.Time
}

func newBucket(l ProgressLimits, now time.Time) *bucket {
	l = l.Resolved()
	return &bucket{events: float64(2 * l.EventsPerSecond), bytes: float64(2 * l.BytesPerSecond), eventRate: float64(l.EventsPerSecond), byteRate: float64(l.BytesPerSecond), last: now}
}

func (b *bucket) allow(n int, now time.Time) bool {
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.events = minFloat(b.events+elapsed*b.eventRate, 2*b.eventRate)
		b.bytes = minFloat(b.bytes+elapsed*b.byteRate, 2*b.byteRate)
		b.last = now
	}
	cost := float64(n)
	if b.events < 1 || b.bytes < cost {
		return false
	}
	b.events--
	b.bytes -= cost
	return true
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Limiter rate-limits events for one call and counts what it drops. It is safe
// for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	b       *bucket
	dropped uint64
	now     func() time.Time
}

// NewLimiter returns a limiter for the limits. now may be nil.
func NewLimiter(l ProgressLimits, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{b: newBucket(l, now()), now: now}
}

// Admit reports whether the event may pass. When it passes, the returned count
// is the number of events dropped since the previous passing one, which the
// caller attaches to the event. When it does not, the drop is counted.
func (l *Limiter) Admit(p Progress) (dropped uint64, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.b.allow(len(p.Text), l.now()) {
		l.dropped++
		return 0, false
	}
	dropped, l.dropped = l.dropped, 0
	return dropped, true
}

// Carry adds drops that a peer already counted, so they are reported in the
// next event instead of being lost with an event this limiter drops.
func (l *Limiter) Carry(n uint64) {
	l.mu.Lock()
	l.dropped += n
	l.mu.Unlock()
}

// Pending returns the drops not yet reported and resets the count.
func (l *Limiter) Pending() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.dropped
	l.dropped = 0
	return n
}

// callbackSink applies the host-side limits as a backstop and calls fn. It is
// the sink a Session installs for CallProgress.
type callbackSink struct {
	limiter *Limiter
	fn      func(Progress)
	mu      sync.Mutex
}

func newCallbackSink(l ProgressLimits, fn func(Progress)) *callbackSink {
	return &callbackSink{limiter: NewLimiter(l, nil), fn: fn}
}

func (s *callbackSink) Deliver(p Progress) (bool, error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	// A guest reports its own drops; the count it sends is added to ours.
	sent := p.Dropped
	if p.Kind == "log" && p.Text == "" && p.Stream == "" && sent > 0 {
		// A marker carries only a count. It is folded into the total reported by
		// the next event or the final marker, and is not itself rate-limited.
		s.limiter.Carry(sent)
		return true, nil
	}
	dropped, ok := s.limiter.Admit(p)
	if !ok {
		s.limiter.Carry(sent) // the dropped event's own count must not vanish with it
		return false, nil
	}
	p.Dropped = sent + dropped
	s.call(p)
	return true, nil
}

// finish delivers a final marker for events never reported.
func (s *callbackSink) finish() {
	if n := s.limiter.Pending(); n > 0 {
		s.call(Progress{Kind: "log", Dropped: n})
	}
}

func (s *callbackSink) call(p Progress) {
	if s.fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fn(p)
}

// ReverseOptions configures the services a host offers to its plugin.
type ReverseOptions struct {
	// Handle serves a reverse call that Authorize allowed. The context derives
	// from the context of the call in flight (or, for a session-scoped call, from
	// the context given to Open without its cancellation), so values such as
	// ReverseFrom, the deadline and cancellation carry through.
	Handle Handler
	// Authorize is required. It runs for every reverse call and receives copies.
	Authorize func(context.Context, Request) error
	// Timeout bounds a reverse call without an earlier deadline; zero uses
	// DefaultTimeout.
	Timeout time.Duration
	// MaxPayloadBytes bounds a reverse payload; zero uses
	// DefaultReversePayloadBytes. It cannot exceed MaxFrameBytes.
	MaxPayloadBytes int
	// MaxInFlight bounds concurrent reverse calls; zero uses
	// DefaultReverseInFlight.
	MaxInFlight int
}

func (o ReverseOptions) validate() error {
	if o.Handle == nil || o.Authorize == nil {
		return ErrDenied
	}
	if o.Timeout < 0 || o.MaxPayloadBytes < 0 || o.MaxPayloadBytes > MaxFrameBytes || o.MaxInFlight < 0 || o.MaxInFlight > 1024 {
		return ErrInvalid
	}
	return nil
}

// ReverseResult is the host's answer to a reverse request.
type ReverseResult struct {
	Payload json.RawMessage
	Error   *RemoteError
}

// ReverseHost is implemented by Session. A backend that carries reverse frames
// (see ReverseBackend) hands each reverse request to it.
type ReverseHost interface {
	// AdmitReverse performs the cheap checks and reserves a slot. It returns
	// either an immediate result, or run, which executes the handler and may be
	// called on another goroutine; run must be called exactly once. A non-nil
	// error is a protocol violation the backend must treat as fatal.
	AdmitReverse(ReverseRequestFrame) (run func() ReverseResult, immediate *ReverseResult, err error)
}

// ReverseBackend is implemented by a backend that can carry reverse frames.
// Open calls SetReverse before the handshake when the host configured Reverse.
type ReverseBackend interface {
	Backend
	SetReverse(ReverseHost)
}

// CallOptions tunes one CallProgress.
type CallOptions struct {
	// Progress sets the event rate for the call; zero fields use the defaults.
	Progress ProgressLimits
}
