package plugingo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/clib"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest/enginetest"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/jsonline"
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

func TestMain(m *testing.M) {
	enginetest.MaybeServeGuest()
	if os.Getenv(pureGoGuestEnv) != "" {
		os.Exit(servePureGoGuest())
	}
	os.Exit(m.Run())
}

// pureGo is lib/plugin's library interface through purego: EXT_PLUGIN_LIBRARY names a built
// libext_plugin_host, which scripts/plugin-engine-test.sh sets.
func pureGo(t *testing.T) clib.Library {
	t.Helper()
	path := os.Getenv("EXT_PLUGIN_LIBRARY")
	if path == "" || !library.Supported() {
		t.Skip("set EXT_PLUGIN_LIBRARY to a built lib/plugin shared library")
	}
	lib, err := library.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ifc, err := library.BindInterface(lib, clib.Prefix, clib.ABIVersion)
	if err != nil {
		t.Fatal(err)
	}
	return ifc
}

// TestPureGoAgreesWithTheOracle holds the binding, on the C engine through purego, to the
// differential checks cgo and c2go are held to: the services and the process sessions.
func TestPureGoAgreesWithTheOracle(t *testing.T) {
	lib := pureGo(t)
	h := enginetest.Engine{
		Name:     "purego",
		Fixtures: "../../lib/plugin/testdata",
		Call:     func(op string, in json.RawMessage) (json.RawMessage, error) { return clib.Services(lib, op, in) },
		OpenProcess: func(ctx context.Context, d engine.Descriptor, l enginetest.Launch, o engine.Options) (enginetest.Session, error) {
			s, err := clib.OpenProcess(ctx, lib, d, clib.Launch{Executable: l.Executable, Environment: l.Environment}, o)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
	}
	for _, c := range enginetest.Checks() {
		t.Run(c.Name, func(t *testing.T) { c.Run(t, h) })
	}
}

func pureGoLaunch(t *testing.T) clib.Launch {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return clib.Launch{Executable: exe, Environment: []string{pureGoGuestEnv + "=1"}}
}

func TestPureGoRunsProgressAndReverseCalls(t *testing.T) {
	lib := pureGo(t)
	if out, err := clib.Services(lib, "descriptor.validate", mustJSON(t, pureGoDescriptor())); err != nil {
		t.Fatalf("descriptor.validate: %s, %v", out, err)
	}
	var mu sync.Mutex
	var verified, authorized, reverseAuthorized int
	var observed []string
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := clib.OpenProcess(ctx, lib, pureGoDescriptor(), pureGoLaunch(t), engine.Options{
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
	if s.State() != engine.StateReady {
		t.Fatalf("session in state %s", s.State())
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

	// Calls with progress at once: each gets its own events, which name their call.
	var pwg sync.WaitGroup
	for i := range 4 {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			var mine []engine.Progress
			payload := json.RawMessage(fmt.Sprint(i))
			out, err := s.CallProgress(ctx, pureGoContract, "run", payload, func(p engine.Progress) { mine = append(mine, p) })
			if err != nil || string(out) != fmt.Sprintf(`{"signed":"signed:%d"}`, i) || len(mine) != 3 {
				t.Errorf("concurrent run %d: %s, %v, %d events", i, out, err, len(mine))
			}
		}()
	}
	pwg.Wait()

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
	if verified != 1 || authorized != 15 || reverseAuthorized != 5 {
		t.Errorf("policies asked: verify %d, authorize %d, reverse authorize %d", verified, authorized, reverseAuthorized)
	}
	if len(observed) == 0 || observed[0] != "verify:ok" {
		t.Errorf("observed: %v", observed)
	}
}

func TestPureGoReturnsTheVerifyPolicysError(t *testing.T) {
	lib := pureGo(t)
	refused := errors.New("not reviewed")
	_, err := clib.OpenProcess(context.Background(), lib, pureGoDescriptor(), pureGoLaunch(t), engine.Options{
		Verify:    func(context.Context, engine.Descriptor) error { return refused },
		Authorize: func(context.Context, engine.Request) error { return nil },
	})
	if !errors.Is(err, refused) {
		t.Fatalf("open with a refusing verify policy: %v", err)
	}
}

func TestPureGoRefusesASessionWithoutPolicies(t *testing.T) {
	lib := pureGo(t)
	if _, err := clib.OpenProcess(context.Background(), lib, pureGoDescriptor(), pureGoLaunch(t), engine.Options{}); !errors.Is(err, engine.ErrDenied) {
		t.Errorf("OpenProcess without policies: %v", err)
	}
	refused := []engine.Requirement{{Contract: engine.ContractRef{Name: "other.contract", Version: "v1"}, Operation: "run"}}
	_, err := clib.OpenProcess(context.Background(), lib, pureGoDescriptor(), pureGoLaunch(t), engine.Options{
		Verify:       func(context.Context, engine.Descriptor) error { return nil },
		Authorize:    func(context.Context, engine.Request) error { return nil },
		Requirements: refused,
	})
	if !errors.Is(err, engine.ErrNotFound) {
		t.Errorf("a descriptor that does not meet the requirements: %v", err)
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
