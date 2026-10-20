//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"encoding/json"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// The tagged frames must be judged the same by the Go contract and the C engine.
// goFrameValidates is how the JSON-line client treats a line: it reads the tag,
// decodes the frame strictly into its type and validates it.
func goFrameValidates(raw json.RawMessage) error {
	var probe struct {
		Frame *string `json:"frame"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Frame == nil {
		return plugin.ErrProtocol
	}
	switch *probe.Frame {
	case plugin.FrameProgress:
		var f plugin.ProgressFrame
		if err := plugin.Decode(raw, &f); err != nil {
			return err
		}
		return f.Validate()
	case plugin.FrameReverseRequest:
		var f plugin.ReverseRequestFrame
		if err := plugin.Decode(raw, &f); err != nil {
			return err
		}
		return f.Validate()
	}
	return plugin.ErrProtocol
}

func TestFrameValidationDifferential(t *testing.T) {
	for name, base := range map[string]map[string]any{
		"progress": {"frame": "progress", "apiVersion": "ext.plugin/v1", "parent": "1", "kind": "log", "stream": "out", "text": "hello", "completed": 1, "total": 3, "dropped": 2},
		"reverse request": {"frame": "reverse_request", "apiVersion": "ext.plugin/v1", "id": "g1", "parent": "1",
			"contract": map[string]any{"name": "host.svc", "version": "v1"}, "operation": "sign", "deadline": "2099-01-01T00:00:00Z", "payload": map[string]any{"n": 1}},
		"session-scoped reverse request": {"frame": "reverse_request", "apiVersion": "ext.plugin/v1", "id": "g2",
			"contract": map[string]any{"name": "host.svc", "version": "v1"}, "operation": "sign", "deadline": "2099-01-01T00:00:00.5+01:00"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := goFrameValidates(mustMarshal(t, base)); err != nil {
				t.Fatalf("the base frame is invalid: %v", err)
			}
			var accepted, total, diverged int
			for _, raw := range mutations(base) {
				total++
				want := goFrameValidates(raw)
				_, got := EngineCall("frame.validate", raw)
				if want == nil {
					accepted++
				}
				if (want == nil) != (got == nil) {
					diverged++
					if diverged <= 8 {
						t.Errorf("%s Go=%s C=%s", raw, errText(want), errText(got))
					}
				}
			}
			t.Logf("%s: %d mutations (%d accepted), %d divergences", name, total, accepted, diverged)
			if accepted == 0 || accepted == total {
				t.Fatalf("mutations are vacuous: accepted %d of %d", accepted, total)
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A retry hint is whole milliseconds from zero to one year: the C engine used to
// accept anything an int64 holds.
func TestRetryHintBoundMatchesGo(t *testing.T) {
	for _, hint := range []string{"0", "-0", "1", "31535999999", "31536000000", "31536000001", "9223372036854775807", "9223372036854775808", "-1", "1.5", "1e3", `"5"`, "null"} {
		raw := json.RawMessage(`{"apiVersion":"ext.plugin/v1","id":"1","error":{"code":"busy","message":"m","retryAfterMilliseconds":` + hint + `}}`)
		var r plugin.Response
		want := plugin.Decode(raw, &r)
		if want == nil {
			want = r.Validate("1")
		}
		_, got := service(t, "response.validate", map[string]any{"response": raw, "requestID": "1"})
		if (want == nil) != (got == nil) {
			t.Errorf("retry hint %s: Go=%s C=%s", hint, errText(want), errText(got))
		}
	}
}
