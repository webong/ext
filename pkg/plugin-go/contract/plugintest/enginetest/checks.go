package enginetest

import "testing"

// Check is one differential check: it holds the engine against the pure-Go contract.
type Check struct {
	Name string
	Run  func(*testing.T, Engine)
}

// Checks lists every check this package holds, in a fixed order.
func Checks() []Check {
	return []Check{
		{"ContractMutationDifferential", ContractMutationDifferential},
		{"PairwiseMutationDifferential", PairwiseMutationDifferential},
		{"MultiFieldMutationDifferential", MultiFieldMutationDifferential},
		{"SchemaMatrixDifferential", SchemaMatrixDifferential},
		{"SchemaGeneratedDifferential", SchemaGeneratedDifferential},
		{"FramesDescriptorDifferential", FramesDescriptorDifferential},
		{"FramesRequestDifferential", FramesRequestDifferential},
		{"FramesMatchDifferential", FramesMatchDifferential},
		{"FrameValidationDifferential", FrameValidationDifferential},
		{"RetryHintBoundMatchesGo", RetryHintBoundMatchesGo},
		{"EndpointFixture", EndpointFixture},
		{"EndpointMutationDifferential", EndpointMutationDifferential},
		{"EndpointAddressDifferential", EndpointAddressDifferential},
		{"EndpointTimeDifferential", EndpointTimeDifferential},
		{"EndpointDefaultClock", EndpointDefaultClock},
		{"EndpointErrorsCarryNoValue", EndpointErrorsCarryNoValue},
		{"EndpointTimeSpellingsAreStricterInTheEngine", EndpointTimeSpellingsAreStricterInTheEngine},
		{"ZeroTimeInAnyZoneMatchesGo", ZeroTimeInAnyZoneMatchesGo},
		{"CaseFoldingMatchesGoForEveryRune", CaseFoldingMatchesGoForEveryRune},
		{"InstanceErrorCausesMatchGo", InstanceErrorCausesMatchGo},
		{"InstanceObserverEventsMatchGo", InstanceObserverEventsMatchGo},
		{"SessionErrorCausesMatchGo", SessionErrorCausesMatchGo},
		{"OpenErrorCausesMatchGo", OpenErrorCausesMatchGo},
		{"DrainErrorCausesMatchGo", DrainErrorCausesMatchGo},
		{"SessionObserverEventsMatchGo", SessionObserverEventsMatchGo},
		{"OpenObserverEventsMatchGo", OpenObserverEventsMatchGo},
		{"DrainObserverEventsMatchGo", DrainObserverEventsMatchGo},
		{"ProcessObserverEventsMatchGo", ProcessObserverEventsMatchGo},
	}
}

// Run runs every check as a subtest. An engine that keeps the names of the checks as tests of its
// own (so that a failure names the check in its CI log) calls them one by one instead.
func Run(t *testing.T, e Engine) {
	for _, c := range Checks() {
		t.Run(c.Name, func(t *testing.T) { c.Run(t, e) })
	}
}
