package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Reader is the committed state of one namespace, as Prepare needs it. A
// backend implements it over its storage inside the transaction it commits in.
type Reader interface {
	Vertex(ctx context.Context, id string) (Vertex, bool, error)
	Edge(ctx context.Context, id string) (Edge, bool, error)
	// IncidentEdges returns the edges whose From or To is id, at most limit of them.
	IncidentEdges(ctx context.Context, id string, limit int) ([]Edge, error)
}

// CheckTransaction applies the rules that need no stored state: the
// transaction's names, its size bound, and the naming, secret and size rules of
// every record. Backends call it before taking a write lock.
func CheckTransaction(tx Transaction, o Options) error {
	o = o.WithDefaults()
	if err := validateName(tx.Namespace); err != nil {
		return err
	}
	if len(tx.IdempotencyKey) > 255 {
		return fmt.Errorf("%w: idempotency key exceeds 255 bytes", ErrTooLarge)
	}
	if n := len(tx.Vertices) + len(tx.Edges) + len(tx.DeleteVertices) + len(tx.DeleteEdges); n > o.MaxTransactionRecords {
		return fmt.Errorf("%w: the transaction holds more than %d records", ErrTooLarge, o.MaxTransactionRecords)
	}
	for _, ref := range tx.DeleteVertices {
		if (ref.Namespace != "" && ref.Namespace != tx.Namespace) || ref.ID == "" {
			return fmt.Errorf("%w: a vertex deletion is outside the transaction namespace", ErrInvalid)
		}
	}
	for _, ref := range tx.DeleteEdges {
		if (ref.Namespace != "" && ref.Namespace != tx.Namespace) || ref.ID == "" {
			return fmt.Errorf("%w: an edge deletion is outside the transaction namespace", ErrInvalid)
		}
	}
	for _, v := range tx.Vertices {
		if v.Namespace != "" && v.Namespace != tx.Namespace {
			return fmt.Errorf("%w: vertex namespace does not match transaction", ErrInvalid)
		}
		if v.ID == "" || len(v.ID) > 1024 {
			return fmt.Errorf("%w: vertex requires an ID of at most 1024 bytes", ErrInvalid)
		}
		if !qualified(tx.Namespace, v.Kind) {
			return fmt.Errorf("%w: a vertex kind is not under the transaction namespace", ErrInvalidName)
		}
		if err := checkRecord(v.Labels, v.Attributes, v.Provenance, o); err != nil {
			return err
		}
	}
	for _, e := range tx.Edges {
		if e.Namespace != "" && e.Namespace != tx.Namespace {
			return fmt.Errorf("%w: edge namespace does not match transaction", ErrInvalid)
		}
		if e.ID == "" || e.From == "" || e.To == "" || len(e.ID) > 1024 {
			return fmt.Errorf("%w: edge requires an ID and both endpoints", ErrInvalid)
		}
		if !qualified(tx.Namespace, e.Type) {
			return fmt.Errorf("%w: a relationship is not under the transaction namespace", ErrInvalidName)
		}
		if err := checkRecord(e.Labels, e.Attributes, e.Provenance, o); err != nil {
			return err
		}
	}
	return nil
}

func checkRecord(labels map[string]string, attrs map[string]any, p Provenance, o Options) error {
	if err := validateAttributes(attrs); err != nil {
		return err
	}
	if err := validateLabels(labels); err != nil {
		return err
	}
	if err := validateProvenance(p); err != nil {
		return err
	}
	size := 0
	for _, part := range []any{labels, attrs} {
		encoded, err := json.Marshal(part)
		if err != nil {
			return fmt.Errorf("%w: record is not JSON serializable", ErrInvalid)
		}
		size += len(encoded)
	}
	if size > o.MaxRecordBytes {
		return fmt.Errorf("%w: record labels and attributes exceed %d bytes", ErrTooLarge, o.MaxRecordBytes)
	}
	return nil
}

// Prepare computes the change a checked transaction makes to a namespace whose
// committed state r reads, at the next revision. It applies deletions, then
// vertex upserts, then edge upserts, checks that no edge dangles, and checks
// the namespace schema. The returned change has no cursor; the backend assigns
// it when it commits. Prepare writes nothing.
func Prepare(ctx context.Context, r Reader, schema Schema, revision uint64, now time.Time, tx Transaction) (Change, error) {
	change := Change{Namespace: tx.Namespace, Revision: revision, Committed: now}
	deletedVertices := map[string]bool{}
	deletedEdges := map[string]bool{}
	vertices := map[string]int{} // id -> index in change.Vertices
	edges := map[string]int{}

	vertexAt := func(id string) (Vertex, bool, error) {
		if i, ok := vertices[id]; ok {
			return change.Vertices[i], true, nil
		}
		if deletedVertices[id] {
			return Vertex{}, false, nil
		}
		return r.Vertex(ctx, id)
	}

	for _, ref := range tx.DeleteEdges {
		if err := ctx.Err(); err != nil {
			return Change{}, err
		}
		if deletedEdges[ref.ID] {
			return Change{}, fmt.Errorf("%w: an edge does not exist", ErrNotFound)
		}
		if _, ok, err := r.Edge(ctx, ref.ID); err != nil {
			return Change{}, err
		} else if !ok {
			return Change{}, fmt.Errorf("%w: an edge does not exist", ErrNotFound)
		}
		deletedEdges[ref.ID] = true
		change.DeletedEdges = append(change.DeletedEdges, ref.ID)
	}
	for _, ref := range tx.DeleteVertices {
		if err := ctx.Err(); err != nil {
			return Change{}, err
		}
		if deletedVertices[ref.ID] {
			return Change{}, fmt.Errorf("%w: a vertex does not exist", ErrNotFound)
		}
		if _, ok, err := r.Vertex(ctx, ref.ID); err != nil {
			return Change{}, err
		} else if !ok {
			return Change{}, fmt.Errorf("%w: a vertex does not exist", ErrNotFound)
		}
		incident, err := r.IncidentEdges(ctx, ref.ID, len(deletedEdges)+1)
		if err != nil {
			return Change{}, err
		}
		attached := false
		for _, e := range incident {
			if !deletedEdges[e.ID] {
				attached = true
				break
			}
		}
		if attached {
			if !tx.DeleteIncidentEdges {
				return Change{}, ErrIncidentEdges
			}
			// Detach every incident edge. The bound is the store's record bound,
			// which the backend enforces on what it returns.
			all, err := r.IncidentEdges(ctx, ref.ID, -1)
			if err != nil {
				return Change{}, err
			}
			for _, e := range all {
				if !deletedEdges[e.ID] {
					deletedEdges[e.ID] = true
					change.DeletedEdges = append(change.DeletedEdges, e.ID)
				}
			}
		}
		deletedVertices[ref.ID] = true
		change.DeletedVertices = append(change.DeletedVertices, ref.ID)
	}
	for _, v := range tx.Vertices {
		old, exists, err := vertexAt(v.ID)
		if err != nil {
			return Change{}, err
		}
		v = cloneVertex(v)
		if exists {
			v.CreatedAt = old.CreatedAt
		} else if v.CreatedAt.IsZero() {
			v.CreatedAt = now
		}
		v.CreatedAt = v.CreatedAt.UTC()
		v.Namespace, v.Revision, v.UpdatedAt = tx.Namespace, revision, now
		if i, ok := vertices[v.ID]; ok {
			change.Vertices[i] = v
		} else {
			vertices[v.ID] = len(change.Vertices)
			change.Vertices = append(change.Vertices, v)
		}
	}
	for _, e := range tx.Edges {
		for _, end := range []string{e.From, e.To} {
			if _, ok, err := vertexAt(end); err != nil {
				return Change{}, err
			} else if !ok {
				return Change{}, ErrDanglingEdge
			}
		}
		var old Edge
		exists := false
		if i, ok := edges[e.ID]; ok {
			old, exists = change.Edges[i], true
		} else if !deletedEdges[e.ID] {
			var err error
			if old, exists, err = r.Edge(ctx, e.ID); err != nil {
				return Change{}, err
			}
		}
		e = cloneEdge(e)
		if exists {
			e.CreatedAt = old.CreatedAt
		} else if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		e.CreatedAt = e.CreatedAt.UTC()
		e.Namespace, e.Revision, e.UpdatedAt = tx.Namespace, revision, now
		if i, ok := edges[e.ID]; ok {
			change.Edges[i] = e
		} else {
			edges[e.ID] = len(change.Edges)
			change.Edges = append(change.Edges, e)
		}
	}
	if err := checkSchema(ctx, schema, change, vertexAt); err != nil {
		return Change{}, err
	}
	return change, nil
}

func checkSchema(ctx context.Context, schema Schema, change Change, vertexAt func(string) (Vertex, bool, error)) error {
	if len(schema.Kinds) > 0 {
		allowed := map[string]bool{}
		for _, kind := range schema.Kinds {
			allowed[kind] = true
		}
		for _, v := range change.Vertices {
			if !allowed[v.Kind] {
				return fmt.Errorf("%w: a vertex kind is not declared by the schema", ErrSchemaViolation)
			}
		}
	}
	if len(schema.Relationships) == 0 {
		return nil
	}
	rules := map[string][]Relationship{}
	for _, rel := range schema.Relationships {
		rules[rel.Type] = append(rules[rel.Type], rel)
	}
	for _, e := range change.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidates, ok := rules[e.Type]
		if !ok {
			return fmt.Errorf("%w: a relationship is not declared by the schema", ErrSchemaViolation)
		}
		from, _, err := vertexAt(e.From)
		if err != nil {
			return err
		}
		to, _, err := vertexAt(e.To)
		if err != nil {
			return err
		}
		matched := false
		for _, rel := range candidates {
			if (rel.From == "" || rel.From == from.Kind) && (rel.To == "" || rel.To == to.Kind) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: an edge joins kinds its relationship does not allow", ErrSchemaViolation)
		}
	}
	return nil
}

func cloneVertex(v Vertex) Vertex { return cloneJSON(v) }
func cloneEdge(v Edge) Edge       { return cloneJSON(v) }
func cloneChange(v Change) Change { return cloneJSON(v) }
func cloneJSON[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}
