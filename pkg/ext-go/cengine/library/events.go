package library

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
	"time"
)

// A library of lib/ whose objects call back (a plugin host's policies, progress and reverse calls)
// queues those callbacks as events rather than calling a function pointer, which purego cannot rely
// on. An object with events answers three operations of the interface:
//
//	events {timeout_ms, max}       the queued events, oldest first, waiting up to timeout_ms
//	answer {ticket, ...fields}     the answer to an event that asks
//	sequence                       the seq of the last event queued
//
// and its events fail with "closed" or "not_found" once it is closed. ServeEvents runs that loop for
// any such object and knows nothing of what its events mean.

// A Caller runs one operation of a library's interface. *Interface is one.
type Caller interface {
	Call(handle uint64, op string, input []byte) ([]byte, error)
}

// An Event is one entry of an object's event queue.
type Event struct {
	// Seq counts the object's queued events from 1; an event the library made up itself (such as
	// a count of events it dropped) has 0.
	Seq uint64 `json:"seq"`
	// Ticket is not 0 for an event that waits for an answer.
	Ticket uint64 `json:"ticket"`
	Kind   string `json:"kind"`
	// Tag names the call the event belongs to, for an event its description marks as the call's.
	Tag  string          `json:"tag,omitempty"`
	Data json.RawMessage `json:"data"`
}

// Asks reports whether the event waits for an answer.
func (e Event) Asks() bool { return e.Ticket != 0 }

// Events is the loop ServeEvents started.
type Events struct {
	caller Caller
	handle uint64
	mu     sync.Mutex
	cond   sync.Cond
	seen   uint64 // the seq of the last event handled
	done   chan struct{}
	// answering counts answers being sent; once closed is set, no new one starts.
	answering int
	closed    bool
}

// ServeEvents takes handle's events until the object is closed. An event that asks goes to answer on a
// goroutine of its own, and the fields answer returns (such as {"allow": true} or {"payload": ...})
// are sent back with its ticket; an answer that panics, or returns nil, sends {"allow": false}. An
// event that asks nothing goes to notify on the serving goroutine, in the order queued. Either may be
// nil.
func ServeEvents(c Caller, handle uint64, answer func(Event) map[string]any, notify func(Event)) *Events {
	e := &Events{caller: c, handle: handle, done: make(chan struct{})}
	e.cond.L = &e.mu
	go e.serve(answer, notify)
	return e
}

func (e *Events) serve(answer func(Event) map[string]any, notify func(Event)) {
	defer func() {
		e.mu.Lock()
		e.seen = math.MaxUint64
		e.cond.Broadcast()
		e.mu.Unlock()
		close(e.done)
	}()
	for {
		out, err := e.caller.Call(e.handle, "events", []byte(`{"timeout_ms":250,"max":64}`))
		if err != nil {
			if closed(err) {
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var events []Event
		if err := json.Unmarshal(out, &events); err != nil {
			continue
		}
		for _, ev := range events {
			if ev.Asks() {
				go e.reply(ev, answer)
			} else if notify != nil {
				func() {
					defer func() { _ = recover() }()
					notify(ev)
				}()
			}
			if ev.Seq != 0 {
				e.mu.Lock()
				if ev.Seq > e.seen {
					e.seen = ev.Seq
				}
				e.cond.Broadcast()
				e.mu.Unlock()
			}
		}
	}
}

func (e *Events) reply(ev Event, answer func(Event) map[string]any) {
	var fields map[string]any
	func() {
		defer func() {
			if recover() != nil {
				fields = nil
			}
		}()
		if answer != nil {
			fields = answer(ev)
		}
	}()
	if fields == nil {
		fields = map[string]any{"allow": false}
	}
	fields["ticket"] = ev.Ticket
	raw, err := json.Marshal(fields)
	if err != nil {
		return
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return // the object is closed: there is no one to answer
	}
	e.answering++
	e.mu.Unlock()
	// The library may have stopped waiting (its time ran out, the object closed): nothing to do.
	_, _ = e.caller.Call(e.handle, "answer", raw)
	e.mu.Lock()
	e.answering--
	e.cond.Broadcast()
	e.mu.Unlock()
}

// settle stops new answers and waits for those being sent. The library frees a closed object only
// when its last call returns, so after settle nothing of the object outlives Close.
func (e *Events) settle() {
	e.mu.Lock()
	e.closed = true
	for e.answering > 0 {
		e.cond.Wait()
	}
	e.mu.Unlock()
}

// Fence returns once every event queued before it was called has been handled (an event that asks
// has been handed to answer; one that asks nothing has returned from notify), or the loop has ended.
func (e *Events) Fence() {
	out, err := e.caller.Call(e.handle, "sequence", nil)
	if err != nil {
		return
	}
	var last uint64
	if json.Unmarshal(out, &last) != nil {
		return
	}
	e.mu.Lock()
	for e.seen < last {
		e.cond.Wait()
	}
	e.mu.Unlock()
}

// Done is closed once the object is closed and the loop has ended.
func (e *Events) Done() <-chan struct{} { return e.done }

// closed reports whether err says the object is gone.
func closed(err error) bool {
	var code *CodeError
	return errors.As(err, &code) && (code.Name == "closed" || code.Name == "not_found")
}
