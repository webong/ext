// Package store provides a generic, namespaced, directed graph store.
//
// Kinds and relationship types are opaque consumer vocabulary. A store never
// assigns product meaning or authority to graph records.
//
// The contract every implementation follows is written as data in
// res/graph/testdata/store/v1: the entities, the naming, secret and size rules,
// the error codes, the SQLite table layout and the operation cases. NewMemory is
// the in-memory implementation; res/graph/store/sqlite is the durable one.
package store

import (
	"context"
	"errors"
	"time"
)

// The sentinel errors of the store. Code maps each to its contract code.
var (
	ErrInvalid         = errors.New("invalid graph record or transaction")
	ErrInvalidName     = errors.New("invalid namespace, kind, or relationship name")
	ErrLimitRequired   = errors.New("query limit must be positive")
	ErrSchemaViolation = errors.New("transaction violates the namespace schema")
	ErrIncidentEdges   = errors.New("vertex has incident edges")
	ErrNotFound        = errors.New("graph record not found")
	ErrNotRegistered   = errors.New("namespace is not registered")
	ErrDanglingEdge    = errors.New("edge endpoint does not exist")
	ErrConflict        = errors.New("graph revision conflict")
	ErrIdempotency     = errors.New("idempotency key reused with different transaction")
	ErrSchemaMismatch  = errors.New("namespace is registered with different rules at this schema version")
	ErrSecretValue     = errors.New("secret-like values are not allowed in graph attributes")
	ErrTooLarge        = errors.New("graph request exceeds a store bound")
	ErrCursorExpired   = errors.New("graph change cursor has expired; take a new snapshot")
	ErrUnsupported     = errors.New("graph store layout is not supported")
	ErrClosed          = errors.New("graph store is closed")
	ErrBusy            = errors.New("graph store is locked by another writer")
)

// Code returns the contract code of err: one of the codes in
// res/system/testdata/codes.json, or "" for a nil error. An error the store does
// not recognize is "io".
func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrInvalidName), errors.Is(err, ErrLimitRequired),
		errors.Is(err, ErrSchemaViolation), errors.Is(err, ErrIncidentEdges):
		return "invalid"
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotRegistered), errors.Is(err, ErrDanglingEdge):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "changed"
	case errors.Is(err, ErrIdempotency), errors.Is(err, ErrSchemaMismatch):
		return "exists"
	case errors.Is(err, ErrSecretValue):
		return "denied"
	case errors.Is(err, ErrTooLarge):
		return "too_large"
	case errors.Is(err, ErrCursorExpired):
		return "expired"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrClosed):
		return "unavailable"
	case errors.Is(err, ErrBusy):
		return "busy"
	default:
		return "io"
	}
}

// Vertex is a generic typed graph record. ID is supplied by the consumer and
// should remain stable for the lifetime of the logical object.
type Vertex struct {
	Namespace  string            `json:"namespace,omitempty"`
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Revision   uint64            `json:"revision,omitempty"`
	CreatedAt  time.Time         `json:"created_at,omitzero"`
	UpdatedAt  time.Time         `json:"updated_at,omitzero"`
	Labels     map[string]string `json:"labels,omitempty"`
	Attributes map[string]any    `json:"attributes,omitempty"`
	Provenance Provenance        `json:"provenance,omitzero"`
}

// Edge is a directed relationship from From to To.
type Edge struct {
	Namespace  string            `json:"namespace,omitempty"`
	ID         string            `json:"id"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Type       string            `json:"type"`
	Revision   uint64            `json:"revision,omitempty"`
	CreatedAt  time.Time         `json:"created_at,omitzero"`
	UpdatedAt  time.Time         `json:"updated_at,omitzero"`
	Labels     map[string]string `json:"labels,omitempty"`
	Attributes map[string]any    `json:"attributes,omitempty"`
	Provenance Provenance        `json:"provenance,omitzero"`
}

// Provenance identifies the system or operation that produced a fact. It is
// descriptive metadata and grants no authority.
type Provenance struct {
	Source    string    `json:"source,omitempty"`
	Operation string    `json:"operation,omitempty"`
	At        time.Time `json:"at,omitzero"`
}

type VertexRef struct {
	Namespace string `json:"namespace,omitempty"`
	ID        string `json:"id"`
}
type EdgeRef struct {
	Namespace string `json:"namespace,omitempty"`
	ID        string `json:"id"`
}

// Transaction is applied atomically within one namespace. A non-nil
// ExpectedRevision enables optimistic concurrency. DeleteVertices require
// DeleteIncidentEdges to explicitly detach incident edges.
type Transaction struct {
	Namespace           string      `json:"namespace"`
	IdempotencyKey      string      `json:"idempotency_key,omitempty"`
	ExpectedRevision    *uint64     `json:"expected_revision,omitempty"`
	Vertices            []Vertex    `json:"vertices,omitempty"`
	Edges               []Edge      `json:"edges,omitempty"`
	DeleteVertices      []VertexRef `json:"delete_vertices,omitempty"`
	DeleteEdges         []EdgeRef   `json:"delete_edges,omitempty"`
	DeleteIncidentEdges bool        `json:"delete_incident_edges,omitempty"`
}

type Commit struct {
	Namespace string    `json:"namespace"`
	Revision  uint64    `json:"revision"`
	Cursor    uint64    `json:"cursor"`
	Committed time.Time `json:"committed_at"`
	Replayed  bool      `json:"replayed,omitempty"`
}

type Change struct {
	Cursor          uint64    `json:"cursor"`
	Namespace       string    `json:"namespace"`
	Revision        uint64    `json:"revision"`
	Committed       time.Time `json:"committed_at"`
	Vertices        []Vertex  `json:"vertices,omitempty"`
	Edges           []Edge    `json:"edges,omitempty"`
	DeletedVertices []string  `json:"deleted_vertices,omitempty"`
	DeletedEdges    []string  `json:"deleted_edges,omitempty"`
}

// Schema declares the rules of one namespace. It is data, so a store keeps it
// with the graph and any implementation can check it. An empty Kinds admits
// every kind under the namespace; an empty Relationships admits every
// relationship type under it. The rules apply to the records a transaction
// writes.
type Schema struct {
	Namespace     string         `json:"namespace"`
	Version       string         `json:"version"`
	Kinds         []string       `json:"kinds,omitempty"`
	Relationships []Relationship `json:"relationships,omitempty"`
}

// Relationship admits edges of Type. A non-empty From or To also requires the
// kind of that endpoint. Several rules may name one type; an edge must match one.
type Relationship struct {
	Type string `json:"type"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type VertexQuery struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind,omitempty"`
	Limit     int    `json:"limit"`
}
type EdgeQuery struct {
	Namespace string `json:"namespace"`
	Type      string `json:"type,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Limit     int    `json:"limit"`
}

type Path struct {
	Vertices []Vertex `json:"vertices"`
	Edges    []Edge   `json:"edges"`
}

type Store interface {
	// Register records a namespace schema. Registering the schema already
	// stored is a no-op; a new version replaces it; the same version with
	// different rules is ErrSchemaMismatch.
	Register(context.Context, Schema) error
	Apply(context.Context, Transaction) (Commit, error)
	GetVertex(ctx context.Context, namespace, id string) (Vertex, error)
	GetEdge(ctx context.Context, namespace, id string) (Edge, error)
	QueryVertices(context.Context, VertexQuery) ([]Vertex, error)
	QueryEdges(context.Context, EdgeQuery) ([]Edge, error)
	Traverse(ctx context.Context, namespace, start string, maxDepth, maxVertices int) ([]Path, error)
	Snapshot(ctx context.Context, namespace string) (Snapshot, error)
	Changes(ctx context.Context, namespace string, after uint64, limit int) ([]Change, error)
	Watch(ctx context.Context, namespace string, after uint64, buffer int) (<-chan Change, error)
	Close() error
}

type Snapshot struct {
	Namespace string `json:"namespace"`
	Revision  uint64 `json:"revision"`
	Cursor    uint64 `json:"cursor"`
	// HistoryFloor is the newest cursor that is no longer available from Changes or Watch.
	HistoryFloor uint64   `json:"history_floor,omitempty"`
	Vertices     []Vertex `json:"vertices"`
	Edges        []Edge   `json:"edges"`
}

// Options sets the bounds of a store. Zero values take the defaults in
// res/graph/testdata/store/v1/rules.json. The latest commit is always kept,
// even when it alone exceeds MaxChangeBytes; idempotency records expire with
// their commit.
type Options struct {
	MaxChanges            int `json:"max_changes,omitempty"`
	MaxChangeBytes        int `json:"max_change_bytes,omitempty"`
	MaxTransactionRecords int `json:"max_transaction_records,omitempty"`
	MaxRecordBytes        int `json:"max_record_bytes,omitempty"`
	MaxResults            int `json:"max_results,omitempty"`
	MaxSnapshotRecords    int `json:"max_snapshot_records,omitempty"`
}

const (
	DefaultMaxChanges            = 4096
	DefaultMaxChangeBytes        = 16 << 20
	DefaultMaxTransactionRecords = 65536
	DefaultMaxRecordBytes        = 1 << 20
	DefaultMaxResults            = 65536
	DefaultMaxSnapshotRecords    = 1 << 20
	// MaxStringBytes bounds one string attribute value.
	MaxStringBytes = 4096
)

// WithDefaults returns o with every zero bound set to its default.
func (o Options) WithDefaults() Options {
	set := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	set(&o.MaxChanges, DefaultMaxChanges)
	set(&o.MaxChangeBytes, DefaultMaxChangeBytes)
	set(&o.MaxTransactionRecords, DefaultMaxTransactionRecords)
	set(&o.MaxRecordBytes, DefaultMaxRecordBytes)
	set(&o.MaxResults, DefaultMaxResults)
	set(&o.MaxSnapshotRecords, DefaultMaxSnapshotRecords)
	return o
}
