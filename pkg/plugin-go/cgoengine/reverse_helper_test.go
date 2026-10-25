package cgoengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/transport/jsonline"
)

// The guest for the reverse-call comparison. Like the progress guest it is this
// test binary started again with guestEnv set: "rev-go" serves a Go guest that
// calls the host through plugin.ReverseFrom, and "rev-raw:<mode>" writes the
// reverse_request frame by hand so it can ask for what the Go guest cannot.

var (
	reverseRef = plugin.ContractRef{Name: "reverse.test", Version: "v1"}
	hostRef    = plugin.ContractRef{Name: "host.svc", Version: "v1"}
)

func reverseDescriptor() plugin.Descriptor {
	ops := func(names ...string) []plugin.Operation {
		var out []plugin.Operation
		for _, n := range names {
			op := plugin.Operation{Name: n}
			if n == "sign" {
				op.Surface = "signing"
			}
			out = append(out, op)
		}
		return out
	}
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "cgoengine/reverse", Revision: "r1"},
		Contracts:  []plugin.Contract{{ContractRef: reverseRef, Operations: ops("call", "plain", "idle", "collect", "burst", "linger", "sessionhang")}},
		Reverse: []plugin.Contract{{ContractRef: hostRef, Operations: ops("sign", "secret", "empty", "fail", "errok",
			"errcode", "errmsg", "errretry", "errretryok", "badjson", "big", "slow", "slowok", "nested", "hold", "hang")}},
	}
}

func backgroundReport(result json.RawMessage, err error) json.RawMessage {
	report := map[string]any{"result": json.RawMessage(nilToNull(result))}
	if err != nil {
		var remote *plugin.RemoteError
		if errors.As(err, &remote) {
			report["error"] = map[string]any{"code": remote.Code}
		} else {
			report["error"] = map[string]any{"other": true}
		}
	}
	raw, _ := json.Marshal(report)
	return raw
}

// newReverseGuest is the Go guest that calls the host through plugin.ReverseFrom.
// It serves a child process (runReverseGuest) or the host's own (in-process).
func newReverseGuest() (*plugin.Guest, error) {
	// outcomes of reverse calls the guest made in the background, for "collect".
	background := make(chan json.RawMessage, 16)
	return plugin.NewGuest(reverseDescriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		switch r.Operation {
		case "idle":
			// A reverse call made after this call has returned, on the session.
			session := plugin.SessionReverseFrom(ctx)
			go func() {
				time.Sleep(20 * time.Millisecond)
				callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				background <- backgroundReport(session.Call(callCtx, hostRef, "sign", json.RawMessage(`{"n":1}`)))
			}()
			return json.RawMessage(`{"started":true}`), nil
		case "sessionhang":
			session := plugin.SessionReverseFrom(ctx)
			go func() {
				callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				background <- backgroundReport(session.Call(callCtx, hostRef, "hang", nil))
			}()
			return json.RawMessage(`{"started":true}`), nil
		case "linger":
			// A call-scoped request that outlives the call that made it.
			caller := plugin.ReverseFrom(ctx)
			go func() {
				callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				background <- backgroundReport(caller.Call(callCtx, hostRef, "hang", nil))
			}()
			time.Sleep(200 * time.Millisecond) // let the host start serving it
			return json.RawMessage(`{"started":true}`), nil
		case "collect":
			select {
			case report := <-background:
				return report, nil
			case <-time.After(5 * time.Second):
				return json.RawMessage(`{"timeout":true}`), nil
			}
		case "burst":
			// Six requests at once; the host holds four.
			caller := plugin.ReverseFrom(ctx)
			var wg sync.WaitGroup
			var mu sync.Mutex
			counts := map[string]int{}
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := caller.Call(ctx, hostRef, "hold", nil)
					var remote *plugin.RemoteError
					key := "ok"
					if errors.As(err, &remote) {
						key = remote.Code
					} else if err != nil {
						key = "other"
					}
					mu.Lock()
					counts[key]++
					mu.Unlock()
				}()
			}
			wg.Wait()
			return json.Marshal(counts)
		}
		if r.Operation != "call" {
			return json.RawMessage(`{"done":true}`), nil
		}
		var in struct {
			Op       string          `json:"op"`
			Payload  json.RawMessage `json:"payload"`
			CancelMs int             `json:"cancelMs"`
		}
		if err := json.Unmarshal(r.Payload, &in); err != nil {
			return nil, err
		}
		caller := plugin.ReverseFrom(ctx)
		if caller == nil {
			return json.RawMessage(`{"caller":false}`), nil
		}
		callCtx := ctx
		if in.CancelMs > 0 {
			// The plugin's own context ends while the host is still serving.
			var cancel context.CancelFunc
			callCtx, cancel = context.WithCancel(ctx)
			defer cancel()
			time.AfterFunc(time.Duration(in.CancelMs)*time.Millisecond, cancel)
		}
		result, err := caller.Call(callCtx, hostRef, in.Op, in.Payload)
		report := map[string]any{"result": json.RawMessage(nilToNull(result))}
		if err != nil {
			var remote *plugin.RemoteError
			if errors.As(err, &remote) {
				report["error"] = map[string]any{"code": remote.Code, "message": remote.Message, "retry": remote.RetryAfterMilliseconds}
			} else {
				report["error"] = map[string]any{"other": err.Error()}
			}
		}
		return json.Marshal(report)
	}})
}

func runReverseGuest(mode string) int {
	if strings.HasPrefix(mode, "rev-raw:") {
		return runRawReverse(strings.TrimPrefix(mode, "rev-raw:"))
	}
	guest, err := newReverseGuest()
	if err != nil {
		return 2
	}
	if err := jsonline.ServeStdio(context.Background(), guest); err != nil {
		return 1
	}
	return 0
}

func nilToNull(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// runRawReverse answers the handshake, then for every call sends one hand-made
// reverse request, reads the host's answer line and returns it in the result.
func runRawReverse(mode string) int {
	in := bufio.NewReader(os.Stdin)
	if _, err := in.ReadString('\n'); err != nil {
		return 0
	}
	request := func(id, parent, op, deadline, payload string) string {
		parentField := ""
		if parent != "" {
			parentField = fmt.Sprintf(`,"parent":%q`, parent)
		}
		payloadField := ""
		if payload != "" {
			payloadField = `,"payload":` + payload
		}
		return fmt.Sprintf(`{"frame":"reverse_request","apiVersion":"ext.plugin/v1",%s%s,"contract":{"name":"host.svc","version":"v1"},"operation":%q,"deadline":%s%s}`+"\n", id, parentField, op, deadline, payloadField)
	}
	future := `"2099-01-01T00:00:00Z"`
	ask := func(frame string) string {
		fmt.Print(frame)
		reply, err := in.ReadString('\n')
		if err != nil {
			return ""
		}
		return strings.TrimRight(reply, "\r\n")
	}
	descriptor, _ := json.Marshal(reverseDescriptor())
	early := ""
	switch mode {
	case "pre-handshake":
		ask(request(`"id":"g1"`, "hello", "sign", future, ""))
	case "pre-handshake-session":
		early = ask(request(`"id":"g1"`, "", "sign", future, ""))
	}
	fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":"hello","payload":%s}`+"\n", descriptor)
	switch mode {
	case "idle-bad":
		// A frame that breaks the rules, when no call is in flight.
		fmt.Print(request(`"id":"!!"`, "", "sign", future, ""))
		return 0
	case "idle-exit":
		return 0
	}
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return 0
		}
		var forward struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(line), &forward) != nil {
			return 1
		}
		id := forward.ID
		var frame string
		switch mode {
		case "undeclared":
			frame = request(`"id":"g1"`, id, "nothere", future, "")
		case "expired":
			frame = request(`"id":"g1"`, id, "sign", `"2000-01-01T00:00:00Z"`, "")
		case "bigrequest":
			frame = request(`"id":"g1"`, id, "sign", future, `"`+strings.Repeat("0", 100)+`"`)
		case "noparent":
			frame = request(`"id":"g1"`, "", "sign", future, "")
		case "wrongparent":
			frame = request(`"id":"g1"`, "999", "sign", future, "")
		case "bad-deadline":
			frame = request(`"id":"g1"`, id, "sign", `"soon"`, "")
		case "bad-id":
			frame = request(`"id":"!!"`, id, "sign", future, "")
		case "bad-extra":
			frame = request(`"id":"g1","extra":1`, id, "sign", future, "")
		case "bad-operation":
			frame = request(`"id":"g1"`, id, "Sign", future, "")
		case "pre-handshake", "pre-handshake-session":
			frame = ""
		default:
			return 2
		}
		reply := ""
		if frame != "" {
			reply = ask(frame)
		}
		if mode == "pre-handshake-session" {
			reply = early
		}
		if reply == "" {
			reply = "null"
		}
		fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":%q,"payload":{"reply":%s}}`+"\n", id, reply)
	}
}
