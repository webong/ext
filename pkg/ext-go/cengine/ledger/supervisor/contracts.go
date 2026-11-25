// Shared contracts are defined in core; aliases preserve type identity.
package supervisor

import shared "github.com/webong/ext/pkg/ext-go/core/ledger/supervisor"

type Artifact = shared.Artifact
type Endpoint = shared.Endpoint
type Connection = shared.Connection
type State = shared.State

const StateStarting = shared.StateStarting
const StateRunning = shared.StateRunning
const StateReady = shared.StateReady
const StateStopping = shared.StateStopping
const StateStopped = shared.StateStopped
const StateCrashed = shared.StateCrashed
const StateRestarting = shared.StateRestarting
const StateOrphaned = shared.StateOrphaned

type Instance = shared.Instance
type Orphan = shared.Orphan
