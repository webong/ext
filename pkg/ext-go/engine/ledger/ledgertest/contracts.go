// Shared contracts are defined in core; aliases preserve type identity.
package ledgertest

import shared "github.com/webong/ext/pkg/ext-go/core/ledger/ledgertest"

var RunBehaviour = shared.RunBehaviour

type Backend = shared.Backend

var Dir = shared.Dir
var Load = shared.Load
var Run = shared.Run
var Match = shared.Match
var RunFingerprints = shared.RunFingerprints
