package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
)

// A library of lib/ describes its own interface: the describe operation of handle 0 answers a
// Description (lib/plugin/interface.json is lib/plugin's), and this file drives any library from
// it. Nothing here knows a library: which objects there are, what their operations take and
// return, which events they raise and how each is answered all come from the description, so a new
// library, or a new operation of one, needs no Go.

// ErrNotDescribed is an object kind, operation or event its library does not describe.
var ErrNotDescribed = errors.New("library: not in the library's description")

// A Description is a library's description of its interface.
type Description struct {
	Library    string                       `json:"library"`
	ABIVersion uint32                       `json:"abi_version"`
	About      string                       `json:"about,omitempty"`
	Codes      []string                     `json:"codes"`
	Objects    map[string]ObjectDescription `json:"objects"`
}

// An ObjectDescription is one kind of object. The object called "library" is handle 0, which is
// never opened or closed.
type ObjectDescription struct {
	About      string                          `json:"about,omitempty"`
	Config     json.RawMessage                 `json:"config,omitempty"`
	Operations map[string]OperationDescription `json:"operations"`
	Events     map[string]EventDescription     `json:"events,omitempty"`
}

// An OperationDescription is one operation: the JSON Schemas of its input and output, and the
// events it raises for the call itself (such as progress).
type OperationDescription struct {
	About  string          `json:"about,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
	Raises []string        `json:"raises,omitempty"`
}

// An EventDescription is one kind of event and how it is answered: "allow" (a policy: allowed or
// not), "result" (a payload or a public error) or "none" (it asks nothing). An event with Call
// belongs to the call whose tag it carries.
type EventDescription struct {
	About        string          `json:"about,omitempty"`
	Answer       string          `json:"answer"`
	Call         bool            `json:"call,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
	DefaultError json.RawMessage `json:"default_error,omitempty"`
}

// Described is a bound library with its description.
type Described struct {
	*Interface
	Description Description
}

// Describe reads the description of the library behind i and checks it is the library i binds.
func Describe(i *Interface) (*Described, error) {
	raw, err := i.Call(0, "describe", nil)
	if err != nil {
		return nil, fmt.Errorf("library: %s does not describe its interface: %w", i.Prefix(), err)
	}
	d := &Described{Interface: i}
	if err := json.Unmarshal(raw, &d.Description); err != nil {
		return nil, fmt.Errorf("library: %s's description: %w", i.Prefix(), err)
	}
	if d.Description.Library != i.Prefix() {
		return nil, fmt.Errorf("library: %s describes itself as %q", i.Prefix(), d.Description.Library)
	}
	if _, ok := d.Description.Objects["library"]; !ok {
		return nil, fmt.Errorf("library: %s's description has no library object", i.Prefix())
	}
	return d, nil
}

// A Handler serves one kind of event. For an event answered "allow", a nil error allows. For one
// answered "result", the value is the payload; an error is passed on as the public error when it
// marshals to an object with a code (such as {"code":..., "message":...}), and is the description's
// default error otherwise. For one that asks nothing, the result is ignored.
type Handler func(data json.RawMessage) (any, error)

// Handlers serve events by kind.
type Handlers map[string]Handler

// Object is an object of a described library: handle 0 (Library) or one Open made.
type Object struct {
	d        *Described
	kind     string
	about    ObjectDescription
	handle   uint64
	handlers Handlers
	events   *Events
	tags     atomic.Uint64

	mu    sync.Mutex
	calls map[string]Handlers // the handlers of the calls in flight, by tag
}

// Library is handle 0, the library itself.
func (d *Described) Library() *Object {
	return &Object{d: d, kind: "library", about: d.Description.Objects["library"]}
}

// Open opens an object of kind from config (any value that marshals to a JSON object; its "object"
// field is set to kind). handlers serve the object's events by kind; an event without a handler is
// denied (or, when it asks nothing, dropped).
func (d *Described) Open(kind string, config any, handlers Handlers) (*Object, error) {
	about, ok := d.Description.Objects[kind]
	if !ok || kind == "library" {
		return nil, fmt.Errorf("%w: object %s of %s", ErrNotDescribed, kind, d.Prefix())
	}
	for event := range handlers {
		if _, ok := about.Events[event]; !ok {
			return nil, fmt.Errorf("%w: event %s of %s", ErrNotDescribed, event, kind)
		}
	}
	fields, err := object(config)
	if err != nil {
		return nil, fmt.Errorf("library: the config of %s: %w", kind, err)
	}
	fields["object"], _ = json.Marshal(kind)
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	handle, err := d.Interface.Open(raw)
	if err != nil {
		return nil, err
	}
	o := &Object{d: d, kind: kind, about: about, handle: handle, handlers: handlers, calls: map[string]Handlers{}}
	if len(about.Events) > 0 {
		o.events = ServeEvents(d.Interface, handle, o.answer, o.notify)
	}
	return o, nil
}

// Kind is the object's kind in the description.
func (o *Object) Kind() string { return o.kind }

// Handle is the object's handle, as another object's config names it (a ledger watch names its
// store). Handle 0 is the library.
func (o *Object) Handle() uint64 { return o.handle }

// Call runs op with in (any value that marshals to JSON; nil is {}) and unmarshals what it answers
// into out, unless out is nil.
func (o *Object) Call(op string, in, out any) error { return o.CallWith(op, in, out, nil) }

// CallWith is Call with handlers for the events op raises for this call (its "raises", such as
// progress): the call is tagged, and those events reach these handlers, before CallWith returns.
func (o *Object) CallWith(op string, in, out any, handlers Handlers) error {
	about, ok := o.about.Operations[op]
	if !ok {
		return fmt.Errorf("%w: operation %s of %s", ErrNotDescribed, op, o.kind)
	}
	for event := range handlers {
		if !contains(about.Raises, event) {
			return fmt.Errorf("%w: %s does not raise %s", ErrNotDescribed, op, event)
		}
	}
	var raw []byte
	var err error
	if handlers != nil {
		fields, ferr := object(in)
		if ferr != nil {
			return fmt.Errorf("library: the input of %s: %w", op, ferr)
		}
		tag := "call-" + strconv.FormatUint(o.tags.Add(1), 10)
		fields["tag"], _ = json.Marshal(tag)
		o.mu.Lock()
		o.calls[tag] = handlers
		o.mu.Unlock()
		defer func() {
			o.mu.Lock()
			delete(o.calls, tag)
			o.mu.Unlock()
		}()
		raw, err = json.Marshal(fields)
	} else if in != nil {
		raw, err = json.Marshal(in)
	}
	if err != nil {
		return err
	}
	answer, err := o.d.Interface.Call(o.handle, op, raw)
	// Every event the call raised is queued by now: deliver it before returning.
	if o.events != nil && (handlers != nil || o.notifies()) {
		o.events.Fence()
	}
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(answer, out)
	}
	return nil
}

// Call is Object.Call with the answer as an Out.
func Call[Out any](o *Object, op string, in any) (Out, error) {
	var out Out
	err := o.Call(op, in, &out)
	return out, err
}

// Close closes the object and, for one with events, waits for its loop to end. Handle 0 is never
// closed.
func (o *Object) Close() error {
	if o.handle == 0 {
		return nil
	}
	err := o.d.Interface.Close(o.handle)
	if o.events != nil {
		<-o.events.Done()
	}
	return err
}

// notifies reports whether any handler serves an event that asks nothing.
func (o *Object) notifies() bool {
	for kind := range o.handlers {
		if o.about.Events[kind].Answer == "none" {
			return true
		}
	}
	return false
}

func (o *Object) handler(e Event) Handler {
	if o.about.Events[e.Kind].Call && e.Tag != "" {
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.calls[e.Tag][e.Kind]
	}
	return o.handlers[e.Kind]
}

func (o *Object) notify(e Event) {
	if h := o.handler(e); h != nil {
		_, _ = h(e.Data)
	}
}

func (o *Object) answer(e Event) map[string]any {
	about, ok := o.about.Events[e.Kind]
	h := o.handler(e)
	if !ok || h == nil {
		if about.Answer == "result" {
			return map[string]any{"error": defaultError(about)}
		}
		return nil // a denial
	}
	value, err := h(e.Data)
	switch about.Answer {
	case "allow":
		return map[string]any{"allow": err == nil}
	case "result":
		if err == nil {
			payload, merr := json.Marshal(value)
			if merr != nil {
				return map[string]any{"error": defaultError(about)}
			}
			return map[string]any{"payload": json.RawMessage(payload)}
		}
		return map[string]any{"error": publicError(err, about)}
	}
	return nil
}

// publicError is err as the library's public error: the first error in its chain that marshals to
// an object with a code, or the description's default.
func publicError(err error, about EventDescription) json.RawMessage {
	for e := err; e != nil; e = errors.Unwrap(e) {
		raw, merr := json.Marshal(e)
		if merr != nil {
			continue
		}
		var fields struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(raw, &fields) == nil && fields.Code != "" {
			return raw
		}
	}
	return defaultError(about)
}

func defaultError(about EventDescription) json.RawMessage {
	if len(about.DefaultError) > 0 {
		return about.DefaultError
	}
	return json.RawMessage(`{"code":"failed","message":"the handler failed"}`)
}

// object is v as the fields of a JSON object; nil is {}.
func object(v any) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if v == nil {
		return fields, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("not a JSON object")
	}
	return fields, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
