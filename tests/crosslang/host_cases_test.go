package crosslang_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/cgoengine/cshared"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// hostCases is lib/plugin/testdata/v1/host-cases.json: what any host must observe when it runs the
// reference guest. Every language's host runs the same file; this runner holds the pure-Go host,
// the oracle, to it, which shows the cases are right.
type hostCases struct {
	Descriptor string             `json:"descriptor"`
	Contract   plugin.ContractRef `json:"contract"`
	Guests     []string           `json:"guests"`
	Cases      []hostCase         `json:"cases"`
}

type hostCase struct {
	Name      string     `json:"name"`
	Verify    string     `json:"verify"`
	Authorize string     `json:"authorize"`
	Revision  string     `json:"revision"`
	Start     string     `json:"start"`
	Steps     []hostStep `json:"steps"`
}

type hostStep struct {
	Call      string          `json:"call"`
	Payload   json.RawMessage `json:"payload"`
	TimeoutMs int             `json:"timeoutMs"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`
	Status    json.RawMessage `json:"status"`
	Fails     bool            `json:"fails"`
	State     string          `json:"state"`
	Close     bool            `json:"close"`
}

func loadHostCases(t *testing.T) (hostCases, plugin.Descriptor) {
	t.Helper()
	data, err := os.ReadFile("../../lib/plugin/testdata/v1/host-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases hostCases
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("../../lib/plugin/testdata/v1/" + cases.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	var d plugin.Descriptor
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return cases, d
}

// statusName is the name host-cases.json gives a host failure.
func statusName(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, plugin.ErrDenied):
		return "denied"
	case errors.Is(err, plugin.ErrMismatch):
		return "mismatch"
	case errors.Is(err, plugin.ErrClosed):
		return "closed"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, plugin.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, plugin.ErrNotFound):
		return "not_found"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	}
	return "unknown: " + err.Error()
}

func sameJSON(t *testing.T, got, want json.RawMessage) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		return false
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatalf("host-cases.json: %v", err)
	}
	return reflect.DeepEqual(a, b)
}

func statusAllowed(t *testing.T, raw json.RawMessage, got string) bool {
	t.Helper()
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == got
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		t.Fatalf("host-cases.json status: %s", raw)
	}
	for _, s := range many {
		if s == got {
			return true
		}
	}
	return false
}

func TestHostCases(t *testing.T) {
	cases, descriptor := loadHostCases(t)
	for _, guest := range cases.Guests {
		t.Run(guest, func(t *testing.T) {
			var connect func(context.Context) (plugin.Backend, error)
			switch guest {
			case "jsonline":
				path := artifact(t, "GO_GUEST")
				connect = func(context.Context) (plugin.Backend, error) { return process(path) }
			case "shared":
				path := artifact(t, "GO_SHARED")
				connect = func(ctx context.Context) (plugin.Backend, error) { return cshared.Open(ctx, path) }
			default:
				t.Fatalf("host-cases.json names an unknown guest %q", guest)
			}
			for _, c := range cases.Cases {
				t.Run(c.Name, func(t *testing.T) { runHostCase(t, cases.Contract, descriptor, c, connect) })
			}
		})
	}
}

func runHostCase(t *testing.T, ref plugin.ContractRef, d plugin.Descriptor, c hostCase, connect func(context.Context) (plugin.Backend, error)) {
	if c.Revision != "" {
		d.Identity.Revision = c.Revision
	}
	verify := func(context.Context, plugin.Descriptor) error { return nil }
	if c.Verify == "deny" {
		verify = func(context.Context, plugin.Descriptor) error { return plugin.ErrDenied }
	}
	authorize := func(context.Context, plugin.Request) error { return nil }
	if c.Authorize == "deny" {
		authorize = func(context.Context, plugin.Request) error { return plugin.ErrDenied }
	}
	s, err := plugin.Open(context.Background(), d, plugin.Options{
		Verify: verify, Authorize: authorize, Timeout: 5 * time.Second,
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) { return connect(ctx) },
	})
	want := c.Start
	if want == "" {
		want = "ok"
	}
	if got := statusName(err); got != want {
		t.Fatalf("start: got %s, want %s", got, want)
	}
	if err != nil {
		return
	}
	defer s.Abort()
	for i, step := range c.Steps {
		switch {
		case step.Close:
			if err := s.Close(context.Background()); err != nil {
				t.Fatalf("step %d: close: %v", i, err)
			}
		case step.State != "":
			if got := string(s.State()); got != step.State {
				t.Fatalf("step %d: state %s, want %s", i, got, step.State)
			}
		case step.Call != "":
			timeout := time.Duration(step.TimeoutMs) * time.Millisecond
			if timeout == 0 {
				timeout = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			var payload json.RawMessage
			if len(step.Payload) > 0 && string(step.Payload) != "null" {
				payload = step.Payload
			}
			result, err := s.Call(ctx, ref, step.Call, payload)
			cancel()
			var remote *plugin.RemoteError
			isRemote := errors.As(err, &remote)
			switch {
			case step.Fails:
				if err == nil {
					t.Fatalf("step %d: %s succeeded with %s", i, step.Call, result)
				}
			case step.Result != nil:
				if err != nil || !sameJSON(t, result, step.Result) {
					t.Fatalf("step %d: %s: got %s, %v; want %s", i, step.Call, result, err, step.Result)
				}
			case step.Error != nil:
				if !isRemote {
					t.Fatalf("step %d: %s: got %v, want remote error %s", i, step.Call, err, step.Error)
				}
				got, _ := json.Marshal(remote)
				if !sameJSON(t, got, step.Error) {
					t.Fatalf("step %d: %s: got %s, want %s", i, step.Call, got, step.Error)
				}
			case step.Status != nil:
				if isRemote || !statusAllowed(t, step.Status, statusName(err)) {
					t.Fatalf("step %d: %s: got %v (%s), want %s", i, step.Call, err, statusName(err), step.Status)
				}
			default:
				t.Fatalf("step %d: a call step needs result, error, status or fails", i)
			}
		default:
			t.Fatalf("step %d: unknown step", i)
		}
	}
}
