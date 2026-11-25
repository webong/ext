// Shared contracts are defined in core; aliases preserve type identity.
package ledger

import shared "github.com/webong/ext/pkg/ext-go/core/ledger"

type Traversal = shared.Traversal

var Traverse = shared.Traverse
var ErrInvalid = shared.ErrInvalid
var ErrInvalidName = shared.ErrInvalidName
var ErrLimitRequired = shared.ErrLimitRequired
var ErrSchemaViolation = shared.ErrSchemaViolation
var ErrIncidentEdges = shared.ErrIncidentEdges
var ErrNotFound = shared.ErrNotFound
var ErrNotRegistered = shared.ErrNotRegistered
var ErrDanglingEdge = shared.ErrDanglingEdge
var ErrConflict = shared.ErrConflict
var ErrIdempotency = shared.ErrIdempotency
var ErrSchemaMismatch = shared.ErrSchemaMismatch
var ErrSecretValue = shared.ErrSecretValue
var ErrTooLarge = shared.ErrTooLarge
var ErrCursorExpired = shared.ErrCursorExpired
var ErrUnsupported = shared.ErrUnsupported
var ErrClosed = shared.ErrClosed
var ErrBusy = shared.ErrBusy
var Code = shared.Code

type Vertex = shared.Vertex
type Edge = shared.Edge
type Provenance = shared.Provenance
type VertexRef = shared.VertexRef
type EdgeRef = shared.EdgeRef
type Transaction = shared.Transaction
type Commit = shared.Commit
type Change = shared.Change
type Schema = shared.Schema
type Relationship = shared.Relationship
type VertexQuery = shared.VertexQuery
type EdgeQuery = shared.EdgeQuery
type Path = shared.Path
type Store = shared.Store
type Snapshot = shared.Snapshot
type Options = shared.Options

const DefaultMaxChanges = shared.DefaultMaxChanges
const DefaultMaxChangeBytes = shared.DefaultMaxChangeBytes
const DefaultMaxTransactionRecords = shared.DefaultMaxTransactionRecords
const DefaultMaxRecordBytes = shared.DefaultMaxRecordBytes
const DefaultMaxResults = shared.DefaultMaxResults
const DefaultMaxSnapshotRecords = shared.DefaultMaxSnapshotRecords
const MaxStringBytes = shared.MaxStringBytes

var ValidateAttributes = shared.ValidateAttributes
var ValidateSchema = shared.ValidateSchema
var NormalizeSchema = shared.NormalizeSchema
var SameSchema = shared.SameSchema
var Canonical = shared.Canonical
var Fingerprint = shared.Fingerprint

type Reader = shared.Reader

var CheckTransaction = shared.CheckTransaction
var RecordBytes = shared.RecordBytes
var Prepare = shared.Prepare
