package goengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
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
		Identity:   plugin.Identity{ID: "cengine/reverse", Revision: "r1"},
		Contracts:  []plugin.Contract{{ContractRef: reverseRef, Operations: ops("call", "plain")}},
		Reverse: []plugin.Contract{{ContractRef: hostRef, Operations: ops("sign", "secret", "empty", "fail", "errok",
			"errcode", "errmsg", "errretry", "errretryok", "badjson", "big", "slow", "slowok", "nested")}},
	}
}

func runReverseGuest(mode string) int {
	if strings.HasPrefix(mode, "rev-raw:") {
		return runRawReverse(strings.TrimPrefix(mode, "rev-raw:"))
	}
	guest, err := plugin.NewGuest(reverseDescriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		if r.Operation != "call" {
			return json.RawMessage(`{"done":true}`), nil
		}
		var in struct {
			Op      string          `json:"op"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(r.Payload, &in); err != nil {
			return nil, err
		}
		caller := plugin.ReverseFrom(ctx)
		if caller == nil {
			return json.RawMessage(`{"caller":false}`), nil
		}
		result, err := caller.Call(ctx, hostRef, in.Op, in.Payload)
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
	if mode == "pre-handshake" {
		ask(request(`"id":"g1"`, "hello", "sign", future, ""))
	}
	fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":"hello","payload":%s}`+"\n", descriptor)
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
		case "pre-handshake":
			frame = ""
		default:
			return 2
		}
		reply := ""
		if frame != "" {
			reply = ask(frame)
		}
		if reply == "" {
			reply = "null"
		}
		fmt.Printf(`{"apiVersion":"ext.plugin/v1","id":%q,"payload":{"reply":%s}}`+"\n", id, reply)
	}
}
