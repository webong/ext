//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/plugintest"
)

func guest(t *testing.T) string {
	t.Helper()
	p := os.Getenv("CTX_CENGINE_GUEST")
	if p == "" {
		t.Fatal("CTX_CENGINE_GUEST required")
	}
	return p
}
func allow([]byte) error { return nil }
func newHost(t *testing.T, d plugin.Descriptor, verify, authorize Policy) *Host {
	t.Helper()
	h, err := New(guest(t), d, verify, authorize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Destroy)
	return h
}
func TestConformance(t *testing.T) {
	plugintest.Run(t, func(context.Context) (plugin.Backend, error) {
		h, err := New(guest(t), plugintest.Descriptor(), allow, allow)
		if err == nil {
			t.Cleanup(h.Destroy)
		}
		return h, err
	})
}
func request(op string) []byte {
	d := plugintest.Descriptor()
	b, _ := json.Marshal(plugin.Request{APIVersion: plugin.APIVersion, ID: "1", Plugin: d.Identity, Contract: d.Contracts[0].ContractRef, Operation: op, Deadline: time.Now().Add(time.Second), Payload: json.RawMessage(`{"value":7}`)})
	return b
}
func TestAdmissionAndValidation(t *testing.T) {
	t.Run("verify-before-spawn", func(t *testing.T) {
		h, err := New("/does/not/exist", plugintest.Descriptor(), func([]byte) error { return plugin.ErrDenied }, allow)
		if err != nil {
			t.Fatal(err)
		}
		defer h.Destroy()
		if !errors.Is(h.Start(context.Background()), plugin.ErrDenied) {
			t.Fatal("verification did not precede spawn")
		}
	})
	t.Run("C-handshake-mismatch", func(t *testing.T) {
		d := plugintest.Descriptor()
		d.Identity.Revision = "other"
		h := newHost(t, d, allow, allow)
		if !errors.Is(h.Start(context.Background()), plugin.ErrMismatch) {
			t.Fatal("mismatch accepted")
		}
	})
	t.Run("C-policy-denial-retains-session", func(t *testing.T) {
		deny := true
		h := newHost(t, plugintest.Descriptor(), allow, func([]byte) error {
			if deny {
				return plugin.ErrDenied
			}
			return nil
		})
		if err := h.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.CallRaw(context.Background(), request("echo")); !errors.Is(err, plugin.ErrDenied) {
			t.Fatal(err)
		}
		deny = false
		if _, err := h.CallRaw(context.Background(), request("echo")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("malformed-local-request-retains-session", func(t *testing.T) {
		h := newHost(t, plugintest.Descriptor(), allow, allow)
		if err := h.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.CallRaw(context.Background(), []byte(`{"id":1,"id":2}`)); !errors.Is(err, plugin.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := h.CallRaw(context.Background(), request("echo")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancel-without-deadline", func(t *testing.T) {
		h := newHost(t, plugintest.Descriptor(), allow, allow)
		if err := h.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(10*time.Millisecond, cancel)
		start := time.Now()
		if _, err := h.CallRaw(ctx, request("wait")); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if time.Since(start) > time.Second {
			t.Fatal("slow cancellation")
		}
	})
	t.Run("destroy-joins-calls", func(t *testing.T) {
		h := newHost(t, plugintest.Descriptor(), allow, allow)
		if err := h.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, _ = h.CallRaw(context.Background(), request("wait")) }()
		}
		h.Destroy()
		wg.Wait()
	})
}
func TestSharedJSONFixtures(t *testing.T) {
	data, err := os.ReadFile("../../pkg/plugin/testdata/v1/invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	if err = json.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	values = append(values, string([]byte{255}), strings.Repeat("[", 66)+"0"+strings.Repeat("]", 66))
	for _, v := range values {
		if ValidateJSON([]byte(v)) == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	for _, v := range []string{`null`, `{"n":123456789012345678901234567890}`, `{"x":"\u0000","arr":[1,true]}`} {
		if err := ValidateJSON([]byte(v)); err != nil {
			t.Fatalf("rejected %s: %v", v, err)
		}
	}
}

func TestFaultyGuests(t *testing.T) {
	base := os.Getenv("CTX_CENGINE_FAULT_GUEST")
	if base == "" {
		t.Fatal("CTX_CENGINE_FAULT_GUEST required")
	}
	for _, mode := range []string{"bad-handshake", "stall", "exit", "duplicate", "wrong-id", "unknown", "oversize", "truncated", "negative-retry", "fragment"} {
		t.Run(mode, func(t *testing.T) {
			path := t.TempDir() + "/" + mode
			if err := os.Symlink(base, path); err != nil {
				t.Fatal(err)
			}
			h, err := New(path, plugintest.Descriptor(), allow, allow)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Destroy()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			err = h.Start(ctx)
			if mode == "bad-handshake" || mode == "stall" || mode == "exit" {
				if err == nil {
					t.Fatal("accepted broken handshake")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = h.CallRaw(context.Background(), request("echo"))
			if mode == "fragment" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted broken response")
			}
			if _, err = h.CallRaw(context.Background(), request("echo")); !errors.Is(err, plugin.ErrClosed) {
				t.Fatalf("failed response retained session: %v", err)
			}
		})
	}
}
