//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"context"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest/enginetest"
)

// The differential checks that hold the engine against the pure-Go contract live in
// enginetest, so that any build of the engine can run them; these are the C engine's runs, under
// the names the checks have always had.
var cgoEngine = enginetest.Engine{
	Name: "C engine", Call: EngineCall, Fixtures: "../../../lib/plugin/testdata",
	NewInstances: func(opts instance.Options[string]) (enginetest.Instances, error) {
		m, err := NewInstances(opts)
		if err != nil {
			return nil, err
		}
		return engineInstances{m}, nil
	},
	Open: func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (enginetest.Session, error) {
		s, err := Open(ctx, d, o)
		if err != nil {
			return nil, err
		}
		return s, nil
	},
	OpenProcess: func(ctx context.Context, d plugin.Descriptor, l enginetest.Launch, o plugin.Options) (enginetest.Session, error) {
		s, err := OpenProcess(ctx, d, LaunchOptions{ProcessOptions: ProcessOptions{Executable: l.Executable, Environment: l.Environment}}, o)
		if err != nil {
			return nil, err
		}
		return s, nil
	},
}

// engineInstances gives the C-backed manager the shape the harness compares.
type engineInstances struct{ *Instances[string] }

func (e engineInstances) Acquire(key string) (string, enginetest.Lease, error) {
	l, err := e.Instances.Acquire(key)
	if err != nil {
		return "", nil, err
	}
	return l.Value, l, nil
}

func TestContractMutationDifferential(t *testing.T) {
	enginetest.ContractMutationDifferential(t, cgoEngine)
}

func TestPairwiseMutationDifferential(t *testing.T) {
	enginetest.PairwiseMutationDifferential(t, cgoEngine)
}

func TestMultiFieldMutationDifferential(t *testing.T) {
	enginetest.MultiFieldMutationDifferential(t, cgoEngine)
}

func TestSchemaMatrixDifferential(t *testing.T) { enginetest.SchemaMatrixDifferential(t, cgoEngine) }

func TestSchemaGeneratedDifferential(t *testing.T) {
	enginetest.SchemaGeneratedDifferential(t, cgoEngine)
}

func TestFramesDescriptorDifferential(t *testing.T) {
	enginetest.FramesDescriptorDifferential(t, cgoEngine)
}

func TestFramesRequestDifferential(t *testing.T) { enginetest.FramesRequestDifferential(t, cgoEngine) }

func TestFramesMatchDifferential(t *testing.T) { enginetest.FramesMatchDifferential(t, cgoEngine) }

func TestFrameValidationDifferential(t *testing.T) {
	enginetest.FrameValidationDifferential(t, cgoEngine)
}

func TestRetryHintBoundMatchesGo(t *testing.T) { enginetest.RetryHintBoundMatchesGo(t, cgoEngine) }

func TestEndpointFixture(t *testing.T) { enginetest.EndpointFixture(t, cgoEngine) }

func TestEndpointMutationDifferential(t *testing.T) {
	enginetest.EndpointMutationDifferential(t, cgoEngine)
}

func TestEndpointAddressDifferential(t *testing.T) {
	enginetest.EndpointAddressDifferential(t, cgoEngine)
}

func TestEndpointTimeDifferential(t *testing.T) { enginetest.EndpointTimeDifferential(t, cgoEngine) }

func TestEndpointDefaultClock(t *testing.T) { enginetest.EndpointDefaultClock(t, cgoEngine) }

func TestEndpointErrorsCarryNoValue(t *testing.T) {
	enginetest.EndpointErrorsCarryNoValue(t, cgoEngine)
}

func TestEndpointTimeSpellingsAreStricterInTheEngine(t *testing.T) {
	enginetest.EndpointTimeSpellingsAreStricterInTheEngine(t, cgoEngine)
}

func TestZeroTimeInAnyZoneMatchesGo(t *testing.T) {
	enginetest.ZeroTimeInAnyZoneMatchesGo(t, cgoEngine)
}

func TestCaseFoldingMatchesGoForEveryRune(t *testing.T) {
	enginetest.CaseFoldingMatchesGoForEveryRune(t, cgoEngine)
}

func TestInstanceErrorCausesMatchGo(t *testing.T) {
	enginetest.InstanceErrorCausesMatchGo(t, cgoEngine)
}

func TestInstanceObserverEventsMatchGo(t *testing.T) {
	enginetest.InstanceObserverEventsMatchGo(t, cgoEngine)
}

func TestSessionErrorCausesMatchGo(t *testing.T) { enginetest.SessionErrorCausesMatchGo(t, cgoEngine) }

func TestOpenErrorCausesMatchGo(t *testing.T) { enginetest.OpenErrorCausesMatchGo(t, cgoEngine) }

func TestDrainErrorCausesMatchGo(t *testing.T) { enginetest.DrainErrorCausesMatchGo(t, cgoEngine) }

func TestSessionObserverEventsMatchGo(t *testing.T) {
	enginetest.SessionObserverEventsMatchGo(t, cgoEngine)
}

func TestOpenObserverEventsMatchGo(t *testing.T) { enginetest.OpenObserverEventsMatchGo(t, cgoEngine) }

func TestDrainObserverEventsMatchGo(t *testing.T) {
	enginetest.DrainObserverEventsMatchGo(t, cgoEngine)
}

func TestProcessObserverEventsMatchGo(t *testing.T) {
	enginetest.ProcessObserverEventsMatchGo(t, cgoEngine)
}
