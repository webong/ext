package enginetest

import (
	"context"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/guest/instance"
)

func TestChecksAreNamedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Checks() {
		if c.Name == "" || c.Run == nil || seen[c.Name] {
			t.Errorf("check %q is unnamed, has no body or repeats a name", c.Name)
		}
		seen[c.Name] = true
	}
	if len(seen) != 28 {
		t.Errorf("%d checks, want 28", len(seen))
	}
}

// The oracle held against itself: the instance checks must pass for the pure-Go manager, every
// time. This exercises the checks' own mechanics (the rig, the Close marks, the ordering of
// events) without any engine, and fails if one of them is flaky.
func TestInstanceChecksPassForTheOracleItself(t *testing.T) {
	self := Engine{Name: "oracle", NewInstances: func(opts instance.Options[string]) (Instances, error) {
		m, err := instance.New(opts)
		if err != nil {
			return nil, err
		}
		return oracleInstances{m}, nil
	}}
	for run := 0; run < 25; run++ {
		t.Run("errors", func(t *testing.T) { InstanceErrorCausesMatchGo(t, self) })
		t.Run("events", func(t *testing.T) { InstanceObserverEventsMatchGo(t, self) })
	}
}

// The same for sessions: the oracle opened through the harness, against the oracle. The drain and
// timeout scenarios depend on timing, so this also shows they do not flake.
func TestSessionChecksPassForTheOracleItself(t *testing.T) {
	self := Engine{Name: "oracle", Open: func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error) {
		s, err := plugin.Open(ctx, d, o)
		if err != nil {
			return nil, err
		}
		return s, nil
	}}
	for run := 0; run < 10; run++ {
		for _, c := range []Check{{"SessionErrorCausesMatchGo", SessionErrorCausesMatchGo}, {"OpenErrorCausesMatchGo", OpenErrorCausesMatchGo}, {"DrainErrorCausesMatchGo", DrainErrorCausesMatchGo}, {"SessionObserverEventsMatchGo", SessionObserverEventsMatchGo}, {"OpenObserverEventsMatchGo", OpenObserverEventsMatchGo}, {"DrainObserverEventsMatchGo", DrainObserverEventsMatchGo}} {
			t.Run(c.Name, func(t *testing.T) { c.Run(t, self) })
		}
	}
}
