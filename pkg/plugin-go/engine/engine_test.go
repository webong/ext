package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/capability"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest/enginetest"
	"github.com/webong/ext/pkg/plugin-go/engine"
)

func TestMain(m *testing.M) {
	enginetest.MaybeServeGuest()
	os.Exit(m.Run())
}

func names(es []engine.Engine) (out []engine.Name) {
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestEnginesAreListedInPreferenceOrder(t *testing.T) {
	got := names(engine.Engines())
	want := []engine.Name{engine.CGo, engine.C2Go, engine.Go}
	if len(got) != len(want) {
		t.Fatalf("Engines() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Engines() = %v, want %v", got, want)
		}
	}
	last := engine.Engines()[2]
	if !last.Available() || !last.Features().Has(engine.Sessions|engine.Progress|engine.Reverse) {
		t.Fatalf("the go engine must be available with sessions, progress and reverse: %v", last.Features())
	}
	if last.Features().Has(engine.Services) {
		t.Fatalf("the go engine has no services by name: %v", last.Features())
	}
}

func TestAnEngineThatIsNotAvailableHasNoFeatures(t *testing.T) {
	for _, e := range engine.Engines() {
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
func best(need engine.Feature) engine.Engine {
	for _, e := range engine.Engines() {
		if e.Features().Has(need) {
			return e
		}
	}
	return nil
}

func TestChooseWithoutANamePicksTheBestAvailable(t *testing.T) {
	for _, name := range []engine.Name{"", engine.Auto} {
		for _, need := range []engine.Feature{0, engine.Services, engine.Sessions, engine.Progress, engine.Reverse, engine.Services | engine.Sessions, engine.Sessions | engine.Reverse, engine.Services | engine.Reverse} {
			want := best(need)
			got, err := engine.Choose(name, need)
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

func TestChooseByNameNeverFallsBack(t *testing.T) {
	if e, err := engine.Choose(engine.Go, engine.Sessions|engine.Progress|engine.Reverse); err != nil || e.Name() != engine.Go {
		t.Fatalf("Choose(go) = %v, %v", e, err)
	}
	// The go engine has no services; asking for them by name is an error, not another engine.
	if e, err := engine.Choose(engine.Go, engine.Services); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("Choose(go, services) = %v, %v; want ErrUnsupported", e, err)
	}
	for _, e := range engine.Engines() {
		got, err := engine.Choose(e.Name(), 0)
		switch {
		case e.Available() && (err != nil || got.Name() != e.Name()):
			t.Errorf("Choose(%s) = %v, %v", e.Name(), got, err)
		case !e.Available() && !errors.Is(err, plugin.ErrUnsupported):
			t.Errorf("Choose(%s) on an engine that is not available = %v, %v; want ErrUnsupported", e.Name(), got, err)
		}
		// Whatever is missing is named in the error and nothing else is returned.
		for _, need := range []engine.Feature{engine.Progress, engine.Reverse} {
			if e.Available() && !e.Features().Has(need) {
				if got, err := engine.Choose(e.Name(), need); !errors.Is(err, plugin.ErrUnsupported) || got != nil || !strings.Contains(err.Error(), need.String()) {
					t.Errorf("Choose(%s, %s) = %v, %v; want an ErrUnsupported naming %s", e.Name(), need, got, err, need)
				}
			}
		}
	}
}

func TestChooseRejectsAnUnknownName(t *testing.T) {
	if e, err := engine.Choose("rust", 0); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("Choose(rust) = %v, %v; want ErrInvalid", e, err)
	}
	if _, err := engine.Get("rust"); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("Get(rust) = %v; want ErrInvalid", err)
	}
}

func TestFromEnvironment(t *testing.T) {
	t.Setenv(engine.Environment, "")
	if e, err := engine.FromEnvironment(0); err != nil || e.Name() != best(0).Name() {
		t.Fatalf("unset: %v, %v; want %s", e, err, best(0).Name())
	}
	t.Setenv(engine.Environment, " go ")
	if e, err := engine.FromEnvironment(engine.Sessions); err != nil || e.Name() != engine.Go {
		t.Fatalf("go: %v, %v", e, err)
	}
	t.Setenv(engine.Environment, "auto")
	if e, err := engine.FromEnvironment(engine.Reverse); err != nil || !e.Features().Has(engine.Reverse) {
		t.Fatalf("auto: %v, %v", e, err)
	}
	t.Setenv(engine.Environment, "nonsense")
	if _, err := engine.FromEnvironment(0); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("nonsense: %v; want ErrInvalid", err)
	}
}

func TestFeatureStrings(t *testing.T) {
	if got := (engine.Services | engine.Reverse).String(); got != "services+reverse" {
		t.Fatalf("String() = %q", got)
	}
	if got := engine.Feature(0).String(); got != "none" {
		t.Fatalf("String() = %q", got)
	}
}

func TestAnEngineWithoutAFeatureRefusesItsCalls(t *testing.T) {
	for _, e := range engine.Engines() {
		if !e.Features().Has(engine.Services) {
			if _, err := e.Call("descriptor.validate", json.RawMessage(`{}`)); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.Call without services = %v; want ErrUnsupported", e.Name(), err)
			}
		}
		if !e.Features().Has(engine.Sessions) {
			if _, err := e.Open(context.Background(), plugin.Descriptor{}, plugin.Options{}); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.Open without sessions = %v; want ErrUnsupported", e.Name(), err)
			}
			if _, err := e.OpenProcess(context.Background(), plugin.Descriptor{}, engine.Launch{}, plugin.Options{}); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.OpenProcess without sessions = %v; want ErrUnsupported", e.Name(), err)
			}
			if _, err := engine.NewInstances(e, instance.Options[int]{}); !errors.Is(err, plugin.ErrUnsupported) {
				t.Errorf("%s.NewInstances without sessions = %v; want ErrUnsupported", e.Name(), err)
			}
		}
	}
}

// harness gives an engine the shape enginetest compares, and checks on the way that every session
// reports the engine that was asked to run it.
func harness(t *testing.T, e engine.Engine) enginetest.Engine {
	h := enginetest.Engine{Name: string(e.Name()), Fixtures: "../../../lib/plugin/testdata"}
	if e.Features().Has(engine.Services) {
		h.Call = e.Call
	}
	if !e.Features().Has(engine.Sessions) {
		return h
	}
	check := func(s engine.Session, err error) (enginetest.Session, error) {
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
		return check(e.OpenProcess(ctx, d, engine.Launch{Executable: l.Executable, Environment: l.Environment}, o))
	}
	h.NewInstances = func(opts instance.Options[string]) (enginetest.Instances, error) {
		m, err := engine.NewInstances(e, opts)
		if err != nil {
			return nil, err
		}
		return harnessInstances{m}, nil
	}
	return h
}

type harnessInstances struct{ engine.Instances[string] }

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
	for _, e := range engine.Engines() {
		if !e.Available() {
			t.Logf("%s is not available in this build", e.Name())
			continue
		}
		t.Run(string(e.Name()), func(t *testing.T) {
			h := harness(t, e)
			for _, c := range enginetest.Checks() {
				if e.Name() == engine.Go && !selfChecks[c.Name] {
					continue
				}
				t.Run(c.Name, func(t *testing.T) { c.Run(t, h) })
			}
		})
	}
}

func TestInstancesLeaseAndDisposeOnEveryEngine(t *testing.T) {
	type resource struct{ key, config string }
	for _, e := range engine.Engines() {
		if !e.Features().Has(engine.Sessions) {
			continue
		}
		t.Run(string(e.Name()), func(t *testing.T) {
			var disposed atomic.Int32
			m, err := engine.NewInstances(e, instance.Options[*resource]{
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
