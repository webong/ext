//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/plugintest"
)

// outcome reduces a guest invocation to what a host can observe: whether the
// call failed outright, and the stable response error code if it did not.
func outcome(resp plugin.Response, err error) string {
	switch {
	case err != nil:
		return "call-error"
	case resp.Error != nil:
		return "error:" + resp.Error.Code
	case resp.Payload == nil:
		return "ok:empty"
	}
	return "ok:" + string(resp.Payload)
}

// TestGuestRequestMatrixDifferential invokes the C guest and the Go guest with
// the same requests, varying one field at a time, and requires identical
// observable outcomes: admission, defaults, cancellation, deadlines and payload
// handling.
func TestGuestRequestMatrixDifferential(t *testing.T) {
	d := plugintest.Descriptor()
	handler := func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return r.Payload, nil
	}
	c, err := NewGuest(d, plugin.GuestOptions{Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Destroy()
	ref, err := plugin.NewGuest(d, plugin.GuestOptions{Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	base := func() plugin.Request {
		return plugin.Request{APIVersion: plugin.APIVersion, ID: "1", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef,
			Operation: "echo", Deadline: time.Now().Add(5 * time.Second), Payload: json.RawMessage(`{"value":7}`)}
	}
	type mutation struct {
		name  string
		apply func(*plugin.Request)
	}
	mutations := []mutation{
		{"base", func(*plugin.Request) {}},
		{"api-version-empty", func(r *plugin.Request) { r.APIVersion = "" }},
		{"api-version-other", func(r *plugin.Request) { r.APIVersion = "ctx.plugin/v2" }},
		{"id-empty", func(r *plugin.Request) { r.ID = "" }},
		{"id-long", func(r *plugin.Request) { r.ID = strings.Repeat("i", 4096) }},
		{"plugin-id-other", func(r *plugin.Request) { r.Plugin.ID = "other" }},
		{"plugin-revision-other", func(r *plugin.Request) { r.Plugin.Revision = "other" }},
		{"contract-unknown", func(r *plugin.Request) { r.Contract.Name = "unknown" }},
		{"contract-version-other", func(r *plugin.Request) { r.Contract.Version = "v9" }},
		{"operation-empty", func(r *plugin.Request) { r.Operation = "" }},
		{"operation-unknown", func(r *plugin.Request) { r.Operation = "nope" }},
		{"operation-wait", func(r *plugin.Request) { r.Operation = "wait" }},
		{"operation-private-error", func(r *plugin.Request) { r.Operation = "private-error" }},
		{"operation-public-error", func(r *plugin.Request) { r.Operation = "public-error" }},
		{"deadline-zero", func(r *plugin.Request) { r.Deadline = time.Time{} }},
		{"deadline-past", func(r *plugin.Request) { r.Deadline = time.Now().Add(-time.Hour) }},
		{"deadline-now", func(r *plugin.Request) { r.Deadline = time.Now() }},
		{"deadline-far", func(r *plugin.Request) { r.Deadline = time.Now().Add(1000 * time.Hour) }},
		{"payload-nil", func(r *plugin.Request) { r.Payload = nil }},
		{"payload-null", func(r *plugin.Request) { r.Payload = json.RawMessage(`null`) }},
		{"payload-invalid", func(r *plugin.Request) { r.Payload = json.RawMessage(`{`) }},
		{"payload-dup-keys", func(r *plugin.Request) { r.Payload = json.RawMessage(`{"a":1,"a":2}`) }},
		{"payload-big-number", func(r *plugin.Request) { r.Payload = json.RawMessage(`{"n":9007199254740993}`) }},
		{"payload-unicode", func(r *plugin.Request) { r.Payload = json.RawMessage(`"é🙂"`) }},
		{"payload-lone-surrogate", func(r *plugin.Request) { r.Payload = json.RawMessage(`"\ud83d"`) }},
		{"payload-deep", func(r *plugin.Request) {
			r.Payload = json.RawMessage(strings.Repeat("[", 100) + strings.Repeat("]", 100))
		}},
		{"payload-large", func(r *plugin.Request) {
			r.Payload = json.RawMessage(`"` + strings.Repeat("a", plugin.MaxFrameBytes) + `"`)
		}},
	}
	accepted := 0
	for _, m := range mutations {
		for _, cancelled := range []bool{false, true} {
			r := base()
			m.apply(&r)
			ctx := context.Background()
			if cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			want := outcome(ref.Invoke(ctx, r))
			got := outcome(c.Invoke(ctx, r))
			if want != got {
				t.Errorf("%s (cancelled=%v): Go=%s C=%s", m.name, cancelled, want, got)
			}
			if strings.HasPrefix(want, "ok:") {
				accepted++
			}
		}
	}
	t.Logf("%d mutations x 2 contexts, %d accepted", len(mutations), accepted)
	if accepted == 0 || accepted == len(mutations)*2 {
		t.Fatal("matrix is vacuous")
	}
}
