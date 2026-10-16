// Package graph provides a generic, namespaced, directed graph store.
//
// Kinds and relationship types are opaque consumer vocabulary. A store never
// assigns product meaning or authority to graph records.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotFound      = errors.New("graph record not found")
	ErrConflict      = errors.New("graph revision conflict")
	ErrIdempotency   = errors.New("idempotency key reused with different transaction")
	ErrDanglingEdge  = errors.New("edge endpoint does not exist")
	ErrIncidentEdges = errors.New("vertex has incident edges")
	ErrLimitRequired = errors.New("query limit must be positive")
	ErrSecretValue   = errors.New("secret-like values are not allowed in graph attributes")
	ErrInvalidName   = errors.New("invalid namespace, kind, or relationship name")
	ErrCursorExpired = errors.New("graph change cursor has expired; take a new snapshot")
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,254}$`)
var secretKeyPattern = regexp.MustCompile(`(?i)(secret|password|passwd|token|credential|private.?key|api.?key)`)

// Vertex is a generic typed graph record. ID is supplied by the consumer and
// should remain stable for the lifetime of the logical object.
type Vertex struct {
	Namespace  string            `json:"namespace"`
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Revision   uint64            `json:"revision"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Labels     map[string]string `json:"labels,omitempty"`
	Attributes map[string]any    `json:"attributes,omitempty"`
	Provenance Provenance        `json:"provenance,omitempty"`
}

// Edge is a directed relationship from From to To.
type Edge struct {
	Namespace  string            `json:"namespace"`
	ID         string            `json:"id"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Type       string            `json:"type"`
	Revision   uint64            `json:"revision"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Labels     map[string]string `json:"labels,omitempty"`
	Attributes map[string]any    `json:"attributes,omitempty"`
	Provenance Provenance        `json:"provenance,omitempty"`
}

// Provenance identifies the system or operation that produced a fact. It is
// descriptive metadata and grants no authority.
type Provenance struct {
	Source    string    `json:"source,omitempty"`
	Operation string    `json:"operation,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

type VertexRef struct{ Namespace, ID string }
type EdgeRef struct{ Namespace, ID string }

// Transaction is applied atomically within one namespace. A non-nil
// ExpectedRevision enables optimistic concurrency. DeleteVertices require
// DeleteIncidentEdges to explicitly detach incident edges.
type Transaction struct {
	Namespace           string
	IdempotencyKey      string
	ExpectedRevision    *uint64
	Vertices            []Vertex
	Edges               []Edge
	DeleteVertices      []VertexRef
	DeleteEdges         []EdgeRef
	DeleteIncidentEdges bool
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

type Schema struct {
	Namespace string
	Version   string
	Validate  Validator
}

type Validator func(View, Transaction) error

// View is a transaction-scoped read-only view supplied to validators.
type View interface {
	Vertex(namespace, id string) (Vertex, bool)
	Edge(namespace, id string) (Edge, bool)
	Vertices(namespace string, limit int) ([]Vertex, error)
	Edges(namespace string, limit int) ([]Edge, error)
}

type VertexQuery struct {
	Namespace, Kind string
	Limit           int
}
type EdgeQuery struct {
	Namespace, Type, From, To string
	Limit                     int
}

type Path struct {
	Vertices []Vertex
	Edges    []Edge
}

type Store interface {
	Register(Schema) error
	Apply(context.Context, Transaction) (Commit, error)
	GetVertex(context.Context, string, string) (Vertex, error)
	GetEdge(context.Context, string, string) (Edge, error)
	QueryVertices(context.Context, VertexQuery) ([]Vertex, error)
	QueryEdges(context.Context, EdgeQuery) ([]Edge, error)
	Traverse(context.Context, string, string, int, int) ([]Path, error)
	Snapshot(context.Context, string) (Snapshot, error)
	Changes(context.Context, string, uint64, int) ([]Change, error)
	Watch(context.Context, string, uint64, int) (<-chan Change, error)
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

func validateName(value string) error {
	if !namePattern.MatchString(value) || strings.Contains(value, "//") {
		return fmt.Errorf("%w: %q", ErrInvalidName, value)
	}
	return nil
}

func validateAttributes(attrs map[string]any) error {
	for key, value := range attrs {
		if secretKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: attribute %q", ErrSecretValue, key)
		}
		if err := inspectValue(value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAttributes checks ordinary graph metadata before projection. It
// applies the same secret-marker and size rules as Store.Apply.
func ValidateAttributes(attrs map[string]any) error { return validateAttributes(attrs) }

func validateLabels(labels map[string]string) error {
	for key, value := range labels {
		if secretKeyPattern.MatchString(key) || isSecretMarker(value) {
			return fmt.Errorf("%w: label %q", ErrSecretValue, key)
		}
	}
	return nil
}

func validateProvenance(p Provenance) error {
	for _, value := range []string{p.Source, p.Operation} {
		if isSecretMarker(value) {
			return ErrSecretValue
		}
	}
	return nil
}

func isSecretMarker(value string) bool {
	value = strings.ToLower(value)
	return strings.HasPrefix(value, "secret-value:") || strings.HasPrefix(value, "private-key:")
}

func inspectValue(value any) error {
	switch v := value.(type) {
	case map[string]any:
		return validateAttributes(v)
	case []any:
		for _, item := range v {
			if err := inspectValue(item); err != nil {
				return err
			}
		}
	case string:
		// Secret bytes cannot be recognized reliably. The public record model
		// rejects common credential forms; consumers should store only scoped,
		// opaque references (for example "secret-ref:..."), never secret bytes.
		if isSecretMarker(v) {
			return ErrSecretValue
		}
		if len(v) > 4096 {
			return errors.New("graph string attribute exceeds 4096 bytes")
		}
	default:
		if value != nil {
			encoded, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("graph attribute is not JSON serializable: %w", err)
			}
			var decoded any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				return err
			}
			switch decoded.(type) {
			case map[string]any, []any:
				if err := inspectValue(decoded); err != nil {
					return err
				}
			case string:
				if err := inspectValue(decoded); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
