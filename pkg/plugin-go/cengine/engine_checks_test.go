//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

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
var cengine = enginetest.Engine{
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
	enginetest.ContractMutationDifferential(t, cengine)
}

func TestPairwiseMutationDifferential(t *testing.T) {
	enginetest.PairwiseMutationDifferential(t, cengine)
}

func TestMultiFieldMutationDifferential(t *testing.T) {
	enginetest.MultiFieldMutationDifferential(t, cengine)
}

func TestSchemaMatrixDifferential(t *testing.T) { enginetest.SchemaMatrixDifferential(t, cengine) }

func TestSchemaGeneratedDifferential(t *testing.T) {
	enginetest.SchemaGeneratedDifferential(t, cengine)
}

func TestFramesDescriptorDifferential(t *testing.T) {
	enginetest.FramesDescriptorDifferential(t, cengine)
}

func TestFramesRequestDifferential(t *testing.T) { enginetest.FramesRequestDifferential(t, cengine) }

func TestFramesMatchDifferential(t *testing.T) { enginetest.FramesMatchDifferential(t, cengine) }

func TestFrameValidationDifferential(t *testing.T) {
	enginetest.FrameValidationDifferential(t, cengine)
}

func TestRetryHintBoundMatchesGo(t *testing.T) { enginetest.RetryHintBoundMatchesGo(t, cengine) }

func TestEndpointFixture(t *testing.T) { enginetest.EndpointFixture(t, cengine) }

func TestEndpointMutationDifferential(t *testing.T) {
	enginetest.EndpointMutationDifferential(t, cengine)
}

func TestEndpointAddressDifferential(t *testing.T) {
	enginetest.EndpointAddressDifferential(t, cengine)
}

func TestEndpointTimeDifferential(t *testing.T) { enginetest.EndpointTimeDifferential(t, cengine) }

func TestEndpointDefaultClock(t *testing.T) { enginetest.EndpointDefaultClock(t, cengine) }

func TestEndpointErrorsCarryNoValue(t *testing.T) { enginetest.EndpointErrorsCarryNoValue(t, cengine) }

func TestEndpointTimeSpellingsAreStricterInTheEngine(t *testing.T) {
	enginetest.EndpointTimeSpellingsAreStricterInTheEngine(t, cengine)
}

func TestZeroTimeInAnyZoneMatchesGo(t *testing.T) { enginetest.ZeroTimeInAnyZoneMatchesGo(t, cengine) }

func TestCaseFoldingMatchesGoForEveryRune(t *testing.T) {
	enginetest.CaseFoldingMatchesGoForEveryRune(t, cengine)
}

func TestInstanceErrorCausesMatchGo(t *testing.T) { enginetest.InstanceErrorCausesMatchGo(t, cengine) }

func TestInstanceObserverEventsMatchGo(t *testing.T) {
	enginetest.InstanceObserverEventsMatchGo(t, cengine)
}

func TestSessionErrorCausesMatchGo(t *testing.T) { enginetest.SessionErrorCausesMatchGo(t, cengine) }

func TestOpenErrorCausesMatchGo(t *testing.T) { enginetest.OpenErrorCausesMatchGo(t, cengine) }

func TestDrainErrorCausesMatchGo(t *testing.T) { enginetest.DrainErrorCausesMatchGo(t, cengine) }

func TestSessionObserverEventsMatchGo(t *testing.T) {
	enginetest.SessionObserverEventsMatchGo(t, cengine)
}

func TestOpenObserverEventsMatchGo(t *testing.T) { enginetest.OpenObserverEventsMatchGo(t, cengine) }

func TestDrainObserverEventsMatchGo(t *testing.T) { enginetest.DrainObserverEventsMatchGo(t, cengine) }
