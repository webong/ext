// Shared contracts are defined in core; aliases preserve type identity.
package plugin

import shared "github.com/webong/ext/pkg/ext-go/core/plugin"

type Endpoint = shared.Endpoint
type Backend = shared.Backend

var NegotiateProtocol = shared.NegotiateProtocol

const BoundaryInProcess = shared.BoundaryInProcess
const BoundaryProcess = shared.BoundaryProcess
const BoundaryConfined = shared.BoundaryConfined
const BoundaryRuntime = shared.BoundaryRuntime
const BoundaryContainer = shared.BoundaryContainer
const BoundaryKernel = shared.BoundaryKernel

var Boundaries = shared.Boundaries
var ValidBoundary = shared.ValidBoundary

type BackendProfile = shared.BackendProfile

var CheckRequirements = shared.CheckRequirements
var Method = shared.Method

const APIVersion = shared.APIVersion
const MaxFrameBytes = shared.MaxFrameBytes
const MaxRetryAfterMilliseconds = shared.MaxRetryAfterMilliseconds
const DefaultTimeout = shared.DefaultTimeout

var ErrInvalid = shared.ErrInvalid
var ErrUnsupported = shared.ErrUnsupported
var ErrNotFound = shared.ErrNotFound
var ErrAmbiguous = shared.ErrAmbiguous
var ErrDenied = shared.ErrDenied
var ErrMismatch = shared.ErrMismatch
var ErrClosed = shared.ErrClosed
var ErrDraining = shared.ErrDraining

type Identity = shared.Identity
type ContractRef = shared.ContractRef
type Operation = shared.Operation
type Contract = shared.Contract
type Descriptor = shared.Descriptor

var MatchHandshake = shared.MatchHandshake

type Requirement = shared.Requirement

var Select = shared.Select

type Request = shared.Request

var ValidateRequest = shared.ValidateRequest

type RemoteError = shared.RemoteError
type Response = shared.Response

var Decode = shared.Decode

type ProgressLimits = shared.ProgressLimits
