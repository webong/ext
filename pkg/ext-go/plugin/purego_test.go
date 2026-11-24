package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
	"github.com/webong/ext/pkg/ext-go/plugin"
)

// pureGoGuestEnv makes the test binary the guest of the purego tests.
const pureGoGuestEnv = "EXT_PLUGIN_PUREGO_TEST_GUEST"

var (
	pureGoContract = engine.ContractRef{Name: "purego.test", Version: "v1"}
	pureGoHost     = engine.ContractRef{Name: "host.svc", Version: "v1"}
)

func pureGoDescriptor() engine.Descriptor {
	return engine.Descriptor{
		APIVersion: engine.APIVersion,
		Identity:   engine.Identity{ID: "purego/guest", Revision: "r1"},
		Contracts: []engine.Contract{{ContractRef: pureGoContract, Operations: []engine.Operation{
			{Name: "run", Progress: true}, {Name: "plain"},
		}}},
		Reverse: []engine.Contract{{ContractRef: pureGoHost, Operations: []engine.Operation{{Name: "sign"}}}},
	}
}

// servePureGoGuest is the guest: run reports three progress events, asks the host to sign what it
// was given and returns the signature; plain echoes its payload.
func servePureGoGuest() int {
	guest, err := engine.NewGuest(pureGoDescriptor(), engine.GuestOptions{Handler: func(ctx context.Context, r engine.Request) (json.RawMessage, error) {
		if r.Operation == "plain" {
			return r.Payload, nil
		}
		for i := uint64(1); i <= 3; i++ {
			if err := engine.ProgressFrom(ctx).Report(engine.Progress{Kind: "progress", Completed: i, Total: 3}); err != nil {
				return nil, err
			}
		}
		host := engine.ReverseFrom(ctx)
		if host == nil {
			return nil, &engine.RemoteError{Code: "no_host", Message: "no reverse caller"}
		}
		signed, err := host.Call(ctx, pureGoHost, "sign", r.Payload)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]json.RawMessage{"signed": signed})
	}})
	if err != nil {
		return 2
	}
	fmt.Fprintln(os.Stderr, "purego guest ready")
	if err := jsonline.ServeStdio(context.Background(), guest); err != nil {
		return 1
	}
	return 0
}

func pureGoEngine(t *testing.T) plugin.Engine {
	t.Helper()
	e, err := plugin.Get(plugin.PureGo)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Available() {
		t.Skip("set EXT_PLUGIN_LIBRARY to a built lib/plugin shared library")
	}
	return e
}

func pureGoLaunch(t *testing.T) plugin.Launch {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return plugin.Launch{Executable: exe, Environment: []string{pureGoGuestEnv + "=1"}}
}

func TestPureGoRunsProgressAndReverseCalls(t *testing.T) {
	e := pureGoEngine(t)
	if !e.Features().Has(plugin.Services | plugin.Sessions | plugin.Progress | plugin.Reverse) {
		t.Fatalf("purego features: %s", e.Features())
	}
	if e.Features().Has(plugin.Backends) {
		t.Fatalf("purego cannot run a Go backend: %s", e.Features())
	}
	if out, err := e.Call("descriptor.validate", mustJSON(t, pureGoDescriptor())); err != nil {
		t.Fatalf("descriptor.validate: %s, %v", out, err)
	}
	var mu sync.Mutex
	var verified, authorized, reverseAuthorized int
	var observed []string
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := e.OpenProcess(ctx, pureGoDescriptor(), pureGoLaunch(t), engine.Options{
		Verify: func(_ context.Context, d engine.Descriptor) error {
			mu.Lock()
			defer mu.Unlock()
			verified++
			if d.Identity.ID != "purego/guest" {
				return errors.New("another plugin")
			}
			return nil
		},
		Authorize: func(_ context.Context, r engine.Request) error {
			mu.Lock()
			defer mu.Unlock()
			authorized++
			if r.Operation == "plain" && string(r.Payload) == `"deny"` {
				return engine.ErrDenied
			}
			return nil
		},
		Observer: func(_ context.Context, ev engine.Event) {
			mu.Lock()
			defer mu.Unlock()
			observed = append(observed, ev.Stage+":"+ev.Code)
		},
		Reverse: &engine.ReverseOptions{
			Authorize: func(_ context.Context, r engine.Request) error {
				mu.Lock()
				defer mu.Unlock()
				reverseAuthorized++
				if r.Contract != pureGoHost || r.Operation != "sign" {
					return engine.ErrDenied
				}
				return nil
			},
			Handle: func(_ context.Context, r engine.Request) (json.RawMessage, error) {
				return json.Marshal("signed:" + string(r.Payload))
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Engine() != plugin.PureGo || s.State() != engine.StateReady {
		t.Fatalf("session on %s in state %s", s.Engine(), s.State())
	}

	var events []engine.Progress
	out, err := s.CallProgress(ctx, pureGoContract, "run", json.RawMessage(`7`), func(p engine.Progress) { events = append(events, p) })
	if err != nil {
		t.Fatal(err)
	}
	// Every event arrives before CallProgress returns, in order.
	if len(events) != 3 || events[0].Completed != 1 || events[2].Completed != 3 {
		t.Errorf("progress events: %+v", events)
	}
	if string(out) != `{"signed":"signed:7"}` {
		t.Errorf("run answered %s", out)
	}

	if out, err := s.Call(ctx, pureGoContract, "plain", json.RawMessage(`{"a":1}`)); err != nil || string(out) != `{"a":1}` {
		t.Errorf("plain: %s, %v", out, err)
	}
	if _, err := s.Call(ctx, pureGoContract, "plain", json.RawMessage(`"deny"`)); !errors.Is(err, engine.ErrDenied) {
		t.Errorf("a denied call: %v", err)
	}
	// Calls on many goroutines at once, each answered by its own policy event.
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprintf(`{"n":%d}`, i)
			if out, err := s.Call(ctx, pureGoContract, "plain", json.RawMessage(want)); err != nil || string(out) != want {
				t.Errorf("concurrent plain %d: %s, %v", i, out, err)
			}
		}()
	}
	wg.Wait()

	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	default:
		t.Error("Done is open after Close")
	}
	if s.State() != engine.StateClosed {
		t.Errorf("state after Close: %s", s.State())
	}
	if _, err := s.Call(ctx, pureGoContract, "plain", nil); err == nil {
		t.Error("a call after Close")
	}
	mu.Lock()
	defer mu.Unlock()
	if verified != 1 || authorized != 11 || reverseAuthorized != 1 {
		t.Errorf("policies asked: verify %d, authorize %d, reverse authorize %d", verified, authorized, reverseAuthorized)
	}
	if len(observed) == 0 || observed[0] != "verify:ok" {
		t.Errorf("observed: %v", observed)
	}
}

func TestPureGoReturnsTheVerifyPolicysError(t *testing.T) {
	e := pureGoEngine(t)
	refused := errors.New("not reviewed")
	_, err := e.OpenProcess(context.Background(), pureGoDescriptor(), pureGoLaunch(t), engine.Options{
		Verify:    func(context.Context, engine.Descriptor) error { return refused },
		Authorize: func(context.Context, engine.Request) error { return nil },
	})
	if !errors.Is(err, refused) {
		t.Fatalf("open with a refusing verify policy: %v", err)
	}
}

func TestPureGoRefusesWhatItCannotRun(t *testing.T) {
	e := pureGoEngine(t)
	if _, err := e.Open(context.Background(), pureGoDescriptor(), engine.Options{}); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("Open over a Go backend: %v", err)
	}
	if _, err := e.OpenProcess(context.Background(), pureGoDescriptor(), pureGoLaunch(t), engine.Options{}); !errors.Is(err, engine.ErrDenied) {
		t.Errorf("OpenProcess without policies: %v", err)
	}
	if _, err := plugin.Choose(plugin.PureGo, plugin.Backends); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("Choose(purego, backends): %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
