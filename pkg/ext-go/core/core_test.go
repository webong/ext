package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/webong/ext/pkg/ext-go/core"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/capability"
	"github.com/webong/ext/pkg/ext-go/engine/guest/instance"
	"github.com/webong/ext/pkg/ext-go/engine/plugintest/enginetest"
)

func TestMain(m *testing.M) {
	enginetest.MaybeServeGuest()
	os.Exit(m.Run())
}

func names(es []core.Engine) (out []core.Name) {
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestEnginesAreListedInPreferenceOrder(t *testing.T) {
	got := names(core.Engines())
	want := []core.Name{core.CGo, core.C2Go, core.Go}
	if len(got) != len(want) {
		t.Fatalf("Engines() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Engines() = %v, want %v", got, want)
		}
	}
	last := core.Engines()[2]
	if !last.Available() || !last.Features().Has(core.Sessions|core.Progress|core.Reverse) {
		t.Fatalf("the go engine must be available with sessions, progress and reverse: %v", last.Features())
	}
	if last.Features().Has(core.Services) {
		t.Fatalf("the go engine has no services by name: %v", last.Features())
	}
}

func TestAnEngineThatIsNotAvailableHasNoFeatures(t *testing.T) {
	for _, e := range core.Engines() {
		if !e.Available() && e.Features() != 0 {
			t.Errorf("%s is not available but has %s", e.Name(), e.Features())
		}
		if e.Available() && e.Features() == 0 {
			t.Errorf("%s is available but has no features", e.Name())
		}
	}
}

// best is what Choose should return for need with no name: the first engine in the order that is
// available and has every feature.
func best(need core.Feature) core.Engine {
	for _, e := range core.Engines() {
		if e.Available() && e.Features().Has(need) {
			return e
		}
	}
	return nil
}

func TestChooseWithoutANamePicksTheBestAvailable(t *testing.T) {
	for _, name := range []core.Name{"", core.Auto} {
		for _, need := range []core.Feature{0, core.Services, core.Sessions, core.Progress, core.Reverse, core.Services | core.Sessions, core.Sessions | core.Reverse, core.Services | core.Reverse} {
			want := best(need)
			got, err := core.Choose(name, need)
			if want == nil {
				if !errors.Is(err, plugin.ErrUnsupported) {
					t.Errorf("Choose(%q, %s) = %v, %v; want ErrUnsupported", name, need, got, err)
				}
				continue
			}
			if err != nil || got.Name() != want.Name() {
				t.Errorf("Choose(%q, %s) = %v, %v; want %s", name, need, got, err, want.Name())
			}
		}
	}
}

// Zero requirements still require an available implementation. In a normal build
// cgo is first in preference order but unavailable.
func TestAutomaticSelectionWithoutRequirementsIsAvailable(t *testing.T) {
	for _, name := range []core.Name{"", core.Auto} {
		t.Run(string(name), func(t *testing.T) {
			e, err := core.Choose(name, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !e.Available() {
				t.Fatalf("Choose(%q, 0) selected unavailable engine %s", name, e.Name())
			}
		})
	}
	for _, value := range []string{"", "auto"} {
		t.Setenv(core.Environment, value)
		e, err := core.FromEnvironment(0)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Available() {
			t.Errorf("EXT_ENGINE=%q selected unavailable engine %s", value, e.Name())
		}
	}
}

func TestChooseByNameNeverFallsBack(t *testing.T) {
	if e, err := core.Choose(core.Go, core.Sessions|core.Progress|core.Reverse); err != nil || e.Name() != core.Go {
		t.Fatalf("Choose(go) = %v, %v", e, err)
	}
	// The go engine has no services; asking for them by name is an error, not another engine.
	if e, err := core.Choose(core.Go, core.Services); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("Choose(go, services) = %v, %v; want ErrUnsupported", e, err)
	}
	for _, e := range core.Engines() {
		got, err := core.Choose(e.Name(), 0)
		switch {
		case e.Available() && (err != nil || got.Name() != e.Name()):
			t.Errorf("Choose(%s) = %v, %v", e.Name(), got, err)
		case !e.Available() && !errors.Is(err, plugin.ErrUnsupported):
			t.Errorf("Choose(%s) on an engine that is not available = %v, %v; want ErrUnsupported", e.Name(), got, err)
		}
		// Whatever is missing is named in the error and nothing else is returned.
		for _, need := range []core.Feature{core.Progress, core.Reverse} {
			if e.Available() && !e.Features().Has(need) {
				if got, err := core.Choose(e.Name(), need); !errors.Is(err, plugin.ErrUnsupported) || got != nil || !strings.Contains(err.Error(), need.String()) {
					t.Errorf("Choose(%s, %s) = %v, %v; want an ErrUnsupported naming %s", e.Name(), need, got, err, need)
				}
			}
		}
	}
}

func TestChooseRejectsAnUnknownName(t *testing.T) {
	if e, err := core.Choose("rust", 0); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("Choose(rust) = %v, %v; want ErrInvalid", e, err)
	}
	if _, err := core.Get("rust"); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("Get(rust) = %v; want ErrInvalid", err)
	}
}

func TestFromEnvironment(t *testing.T) {
	t.Setenv(core.Environment, "")
	if e, err := core.FromEnvironment(0); err != nil || e.Name() != best(0).Name() {
		t.Fatalf("unset: %v, %v; want %s", e, err, best(0).Name())
	}
	t.Setenv(core.Environment, " go ")
	if e, err := core.FromEnvironment(core.Sessions); err != nil || e.Name() != core.Go {
		t.Fatalf("go: %v, %v", e, err)
	}
	t.Setenv(core.Environment, "auto")
	if e, err := core.FromEnvironment(core.Reverse); err != nil || !e.Features().Has(core.Reverse) {
		t.Fatalf("auto: %v, %v", e, err)
	}
	t.Setenv(core.Environment, "nonsense")
	if _, err := core.FromEnvironment(0); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("nonsense: %v; want ErrInvalid", err)
	}
}

func TestFeatureStrings(t *testing.T) {
	if got := (core.Services | core.Reverse).String(); got != "services+reverse" {
		t.Fatalf("String() = %q", got)
	}
	if got := core.Feature(0).String(); got != "none" {
		t.Fatalf("String() = %q", got)
	}
}

func TestAnEngineWithoutAFeatureRefusesItsCalls(t *testing.T) {
	for _, e := range core.Engines() {
		if !e.Features().Has(core.Services) {
			if _, err := e.Call("descriptor.validate", json.RawMessage(`{}`)); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.Call without services = %v; want ErrUnsupported", e.Name(), err)
			}
		}
		if !e.Features().Has(core.Sessions) {
			if _, err := e.Open(context.Background(), plugin.Descriptor{}, plugin.Options{}); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.Open without sessions = %v; want ErrUnsupported", e.Name(), err)
			}
			if _, err := e.OpenProcess(context.Background(), plugin.Descriptor{}, core.Launch{}, plugin.Options{}); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.OpenProcess without sessions = %v; want ErrUnsupported", e.Name(), err)
			}
			if _, err := core.NewInstances(e, instance.Options[int]{}); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.NewInstances without sessions = %v; want ErrUnsupported", e.Name(), err)
			}
		}
	}
}

// harness gives an engine the shape enginetest compares, and checks on the way that every session
// reports the engine that was asked to run it.
func harness(t *testing.T, e core.Engine) enginetest.Engine {
	h := enginetest.Engine{Name: string(e.Name()), Fixtures: "../../../lib/plugin/testdata"}
	if e.Features().Has(core.Services) {
		h.Call = e.Call
	}
	if !e.Features().Has(core.Sessions) {
		return h
	}
	check := func(s core.Session, err error) (enginetest.Session, error) {
		if err != nil {
			return nil, err
		}
		if s.Engine() != e.Name() {
			t.Errorf("a session opened on %s reports engine %s", e.Name(), s.Engine())
		}
		return s, nil
	}
	h.Open = func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (enginetest.Session, error) {
		return check(e.Open(ctx, d, o))
	}
	h.OpenProcess = func(ctx context.Context, d plugin.Descriptor, l enginetest.Launch, o plugin.Options) (enginetest.Session, error) {
		return check(e.OpenProcess(ctx, d, core.Launch{Executable: l.Executable, Environment: l.Environment}, o))
	}
	h.NewInstances = func(opts instance.Options[string]) (enginetest.Instances, error) {
		m, err := core.NewInstances(e, opts)
		if err != nil {
			return nil, err
		}
		return harnessInstances{m}, nil
	}
	return h
}

type harnessInstances struct{ core.Instances[string] }

func (h harnessInstances) Acquire(key string) (string, enginetest.Lease, error) {
	l, err := h.Instances.Acquire(key)
	if err != nil {
		return "", nil, err
	}
	return l.Value, l, nil
}

// selfChecks are the harness checks the oracle passes against itself (enginetest runs them the same
// way): the instance and session ones. The process check asserts the C engine's deliberate
// differences from Go, so it is for the engines that are not the oracle.
var selfChecks = map[string]bool{
	"InstanceErrorCausesMatchGo": true, "InstanceObserverEventsMatchGo": true, "SessionErrorCausesMatchGo": true,
	"OpenErrorCausesMatchGo": true, "DrainErrorCausesMatchGo": true, "SessionObserverEventsMatchGo": true,
	"OpenObserverEventsMatchGo": true, "DrainObserverEventsMatchGo": true,
}

// TestEveryAvailableEngineAgreesWithTheOracle holds each engine, reached through this package, to the
// differential checks of enginetest: all of them for the engines that are translations of the C, the
// ones the oracle passes against itself for the oracle.
func TestEveryAvailableEngineAgreesWithTheOracle(t *testing.T) {
	for _, e := range core.Engines() {
		if !e.Available() {
			t.Logf("%s is not available in this build", e.Name())
			continue
		}
		t.Run(string(e.Name()), func(t *testing.T) {
			h := harness(t, e)
			for _, c := range enginetest.Checks() {
				if e.Name() == core.Go && !selfChecks[c.Name] {
					continue
				}
				t.Run(c.Name, func(t *testing.T) { c.Run(t, h) })
			}
		})
	}
}

func TestInstancesLeaseAndDisposeOnEveryEngine(t *testing.T) {
	type resource struct{ key, config string }
	for _, e := range core.Engines() {
		if !e.Features().Has(core.Sessions) {
			continue
		}
		t.Run(string(e.Name()), func(t *testing.T) {
			var disposed atomic.Int32
			m, err := core.NewInstances(e, instance.Options[*resource]{
				Capacity: 2,
				Validate: func(c json.RawMessage) error {
					if !json.Valid(c) {
						return plugin.ErrInvalid
					}
					return nil
				},
				Create: func(_ context.Context, key string, config json.RawMessage) (*resource, func() error, error) {
					return &resource{key, string(config)}, func() error { disposed.Add(1); return nil }, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			// The manager configures like the one capability helpers register.
			var _ capability.Configurer = m
			ctx := context.Background()
			if err := m.Configure(ctx, "a", "r1", json.RawMessage(`{"x":1}`)); err != nil {
				t.Fatal(err)
			}
			l, err := m.Acquire("a")
			if err != nil {
				t.Fatal(err)
			}
			if l.Value.key != "a" || l.Value.config != `{"x":1}` || l.Revision != "r1" {
				t.Fatalf("lease = %+v revision %q", l.Value, l.Revision)
			}
			if err := m.Configure(ctx, "a", "r2", json.RawMessage(`{"x":2}`)); err != nil {
				t.Fatal(err)
			}
			if err := l.Release(); err != nil {
				t.Fatal(err)
			}
			if err := m.Configure(ctx, "a", "r1", json.RawMessage(`not json`)); !errors.Is(err, plugin.ErrInvalid) {
				t.Fatalf("Configure with invalid config = %v; want ErrInvalid", err)
			}
			if err := m.Remove("a"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Acquire("a"); err == nil {
				t.Fatal("Acquire after Remove succeeded")
			}
			if err := m.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if got := disposed.Load(); got != 2 {
				t.Fatalf("%d instances disposed, want 2 (the replaced one and the removed one)", got)
			}
		})
	}
}
