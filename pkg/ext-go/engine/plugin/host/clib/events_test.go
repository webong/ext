package clib_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/clib"
)

// queue is a library object with events, in Go: what ServeEvents expects of any clib.
type queue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	events  []clib.Event
	seq     uint64
	closed  bool
	answers map[uint64]string
}

func newQueue() *queue {
	q := &queue{answers: map[uint64]string{}}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// codeError is a library's code, as cengine/library's CodeError carries it.
type codeError struct{ name string }

func (e codeError) Error() string    { return "test: " + e.name }
func (e codeError) CodeName() string { return e.name }

func (q *queue) push(ticket uint64, kind string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	q.events = append(q.events, clib.Event{Seq: q.seq, Ticket: ticket, Kind: kind, Data: json.RawMessage(fmt.Sprint(q.seq))})
	q.cond.Broadcast()
}

func (q *queue) Call(_ uint64, op string, input []byte) ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch op {
	case "events":
		deadline := time.Now().Add(50 * time.Millisecond)
		for len(q.events) == 0 && !q.closed && time.Now().Before(deadline) {
			q.mu.Unlock()
			time.Sleep(time.Millisecond)
			q.mu.Lock()
		}
		if q.closed {
			return nil, codeError{"closed"}
		}
		out, _ := json.Marshal(q.events)
		q.events = nil
		return out, nil
	case "answer":
		var a map[string]any
		_ = json.Unmarshal(input, &a)
		q.answers[uint64(a["ticket"].(float64))] = string(input)
		return []byte("null"), nil
	case "sequence":
		return []byte(fmt.Sprint(q.seq)), nil
	}
	return nil, codeError{"unsupported"}
}

func TestServeEventsDeliversInOrderAnswersAndFences(t *testing.T) {
	q := newQueue()
	var mu sync.Mutex
	var notified []string
	events := clib.ServeEvents(q, 1, func(e clib.Event) map[string]any {
		switch e.Kind {
		case "allow":
			return map[string]any{"allow": true}
		case "panic":
			panic("a policy that panics denies")
		}
		return nil
	}, func(e clib.Event) {
		mu.Lock()
		defer mu.Unlock()
		notified = append(notified, fmt.Sprintf("%s#%d", e.Kind, e.Seq))
	})
	for i := 0; i < 50; i++ {
		q.push(0, "progress")
	}
	q.push(7, "allow")
	q.push(8, "deny")
	q.push(9, "panic")
	events.Fence()
	mu.Lock()
	if len(notified) != 50 || notified[0] != "progress#1" || notified[49] != "progress#50" {
		t.Errorf("notified %d events, first %v", len(notified), notified[:min(len(notified), 2)])
	}
	mu.Unlock()
	// Answers run on their own goroutines: wait for them.
	deadline := time.Now().Add(5 * time.Second)
	for {
		q.mu.Lock()
		n := len(q.answers)
		q.mu.Unlock()
		if n == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	q.mu.Lock()
	for ticket, want := range map[uint64]string{7: `{"allow":true,"ticket":7}`, 8: `{"allow":false,"ticket":8}`, 9: `{"allow":false,"ticket":9}`} {
		if q.answers[ticket] != want {
			t.Errorf("answer %d = %s, want %s", ticket, q.answers[ticket], want)
		}
	}
	q.closed = true
	q.mu.Unlock()
	select {
	case <-events.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not end once the object closed")
	}
	events.Fence() // returns at once after the loop ended
}
