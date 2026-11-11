package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type namespaceState struct {
	schema   Schema
	revision uint64
}

type idemRecord struct {
	fingerprint string
	commit      Commit
}

type retained struct {
	change Change
	size   int
}

type memoryStore struct {
	mu          sync.Mutex
	options     Options
	namespaces  map[string]*namespaceState
	vertices    map[string]Vertex // namespace \x00 id
	edges       map[string]Edge
	idempotency map[string]idemRecord // namespace \x00 key
	changes     []retained
	changeBytes int
	cursor      uint64
	floor       uint64
	watchers    map[uint64]*watcher
	nextWatcher uint64
	closed      bool
	done        chan struct{}
}

type watcher struct {
	namespace string
	cursor    uint64
	ch        chan Change
}

// NewMemory creates an in-memory graph store with the default bounds.
func NewMemory() Store { return NewMemoryWithOptions(Options{}) }

// NewMemoryWithOptions creates an in-memory graph store with the given bounds.
func NewMemoryWithOptions(options Options) Store {
	return &memoryStore{
		options:     options.WithDefaults(),
		namespaces:  map[string]*namespaceState{},
		vertices:    map[string]Vertex{},
		edges:       map[string]Edge{},
		idempotency: map[string]idemRecord{},
		watchers:    map[uint64]*watcher{},
		done:        make(chan struct{}),
	}
}

func recordKey(ns, id string) string { return ns + "\x00" + id }

func (s *memoryStore) Register(ctx context.Context, schema Schema) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateSchema(schema); err != nil {
		return err
	}
	schema = NormalizeSchema(schema)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	state, ok := s.namespaces[schema.Namespace]
	if !ok {
		s.namespaces[schema.Namespace] = &namespaceState{schema: schema}
		return nil
	}
	if state.schema.Version == schema.Version && !SameSchema(state.schema, schema) {
		return ErrSchemaMismatch
	}
	state.schema = schema
	return nil
}

type memoryReader struct {
	s  *memoryStore
	ns string
}

func (r memoryReader) Vertex(_ context.Context, id string) (Vertex, bool, error) {
	v, ok := r.s.vertices[recordKey(r.ns, id)]
	return v, ok, nil
}
func (r memoryReader) Edge(_ context.Context, id string) (Edge, bool, error) {
	e, ok := r.s.edges[recordKey(r.ns, id)]
	return e, ok, nil
}
func (r memoryReader) IncidentEdges(_ context.Context, id string, limit int) ([]Edge, error) {
	out := []Edge{}
	for _, e := range r.s.edges {
		if e.Namespace == r.ns && (e.From == id || e.To == id) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memoryStore) Apply(ctx context.Context, tx Transaction) (Commit, error) {
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	if err := CheckTransaction(tx, s.options); err != nil {
		return Commit{}, err
	}
	fingerprint, err := Fingerprint(tx)
	if err != nil {
		return Commit{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Commit{}, ErrClosed
	}
	state, ok := s.namespaces[tx.Namespace]
	if !ok {
		return Commit{}, ErrNotRegistered
	}
	idemKey := ""
	if tx.IdempotencyKey != "" {
		idemKey = recordKey(tx.Namespace, tx.IdempotencyKey)
		if previous, ok := s.idempotency[idemKey]; ok {
			if previous.fingerprint != fingerprint {
				return Commit{}, ErrIdempotency
			}
			c := previous.commit
			c.Replayed = true
			return c, nil
		}
	}
	if tx.ExpectedRevision != nil && *tx.ExpectedRevision != state.revision {
		return Commit{}, fmt.Errorf("%w: the namespace is not at the expected revision", ErrConflict)
	}
	now := time.Now().UTC()
	change, err := Prepare(ctx, memoryReader{s: s, ns: tx.Namespace}, state.schema, state.revision+1, now, tx)
	if err != nil {
		return Commit{}, err
	}
	size, err := ChangeSize(change)
	if err != nil {
		return Commit{}, err
	}
	// Prepare checked everything; from here the commit cannot fail.
	for _, id := range change.DeletedEdges {
		delete(s.edges, recordKey(tx.Namespace, id))
	}
	for _, id := range change.DeletedVertices {
		delete(s.vertices, recordKey(tx.Namespace, id))
	}
	for _, v := range change.Vertices {
		s.vertices[recordKey(tx.Namespace, v.ID)] = v
	}
	for _, e := range change.Edges {
		s.edges[recordKey(tx.Namespace, e.ID)] = e
	}
	state.revision = change.Revision
	s.cursor++
	change.Cursor = s.cursor
	commit := Commit{Namespace: tx.Namespace, Revision: change.Revision, Cursor: change.Cursor, Committed: now}
	s.changes = append(s.changes, retained{change: change, size: size})
	s.changeBytes += size
	if idemKey != "" {
		s.idempotency[idemKey] = idemRecord{fingerprint: fingerprint, commit: commit}
	}
	s.compact()
	s.publish(change)
	return commit, nil
}

// ChangeSize is the size a change counts against MaxChangeBytes: the length of
// its canonical JSON.
func ChangeSize(change Change) (int, error) {
	encoded, err := Canonical(change)
	if err != nil {
		return 0, fmt.Errorf("%w: change is not JSON serializable", ErrInvalid)
	}
	return len(encoded), nil
}

func (s *memoryStore) compact() {
	removed := 0
	for len(s.changes)-removed > 1 && (len(s.changes)-removed > s.options.MaxChanges || s.changeBytes > s.options.MaxChangeBytes) {
		s.changeBytes -= s.changes[removed].size
		s.floor = s.changes[removed].change.Cursor
		removed++
	}
	if removed == 0 {
		return
	}
	s.changes = append([]retained(nil), s.changes[removed:]...)
	for key, record := range s.idempotency {
		if record.commit.Cursor <= s.floor {
			delete(s.idempotency, key)
		}
	}
}

func (s *memoryStore) publish(change Change) {
	for id, w := range s.watchers {
		if w.namespace != "" && w.namespace != change.Namespace {
			continue
		}
		select {
		case w.ch <- cloneChange(change):
			w.cursor = change.Cursor
		default:
			close(w.ch)
			delete(s.watchers, id)
		}
	}
}

func (s *memoryStore) begin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	return nil
}

func (s *memoryStore) checkLimit(limit int) error {
	if limit <= 0 {
		return ErrLimitRequired
	}
	if limit > s.options.MaxResults {
		return fmt.Errorf("%w: the limit exceeds the result bound of %d", ErrTooLarge, s.options.MaxResults)
	}
	return nil
}

func (s *memoryStore) GetVertex(ctx context.Context, ns, id string) (Vertex, error) {
	if err := s.begin(ctx); err != nil {
		return Vertex{}, err
	}
	defer s.mu.Unlock()
	v, ok := s.vertices[recordKey(ns, id)]
	if !ok {
		return Vertex{}, ErrNotFound
	}
	return cloneVertex(v), nil
}

func (s *memoryStore) GetEdge(ctx context.Context, ns, id string) (Edge, error) {
	if err := s.begin(ctx); err != nil {
		return Edge{}, err
	}
	defer s.mu.Unlock()
	e, ok := s.edges[recordKey(ns, id)]
	if !ok {
		return Edge{}, ErrNotFound
	}
	return cloneEdge(e), nil
}

func (s *memoryStore) QueryVertices(ctx context.Context, q VertexQuery) ([]Vertex, error) {
	if err := s.checkLimit(q.Limit); err != nil {
		return nil, err
	}
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	out := []Vertex{}
	for _, v := range s.vertices {
		if v.Namespace == q.Namespace && (q.Kind == "" || v.Kind == q.Kind) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	for i := range out {
		out[i] = cloneVertex(out[i])
	}
	return out, nil
}

func (s *memoryStore) QueryEdges(ctx context.Context, q EdgeQuery) ([]Edge, error) {
	if err := s.checkLimit(q.Limit); err != nil {
		return nil, err
	}
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	out := []Edge{}
	for _, e := range s.edges {
		if e.Namespace == q.Namespace && (q.Type == "" || e.Type == q.Type) && (q.From == "" || e.From == q.From) && (q.To == "" || e.To == q.To) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	for i := range out {
		out[i] = cloneEdge(out[i])
	}
	return out, nil
}

func (s *memoryStore) Traverse(ctx context.Context, ns, start string, maxDepth, maxVertices int) ([]Path, error) {
	if maxDepth < 0 {
		return nil, ErrLimitRequired
	}
	if err := s.checkLimit(maxVertices); err != nil {
		return nil, err
	}
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	return Traverse(ctx, memoryTraversal{s: s, ns: ns}, start, maxDepth, maxVertices)
}

type memoryTraversal struct {
	s  *memoryStore
	ns string
}

func (t memoryTraversal) Vertex(_ context.Context, id string) (Vertex, bool, error) {
	v, ok := t.s.vertices[recordKey(t.ns, id)]
	return v, ok, nil
}
func (t memoryTraversal) Outgoing(_ context.Context, id string) ([]Edge, error) {
	out := []Edge{}
	for _, e := range t.s.edges {
		if e.Namespace == t.ns && e.From == id {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *memoryStore) Snapshot(ctx context.Context, ns string) (Snapshot, error) {
	if err := s.begin(ctx); err != nil {
		return Snapshot{}, err
	}
	defer s.mu.Unlock()
	snap := Snapshot{Namespace: ns, Cursor: s.cursor, HistoryFloor: s.floor, Vertices: []Vertex{}, Edges: []Edge{}}
	if state, ok := s.namespaces[ns]; ok {
		snap.Revision = state.revision
	}
	for _, v := range s.vertices {
		if v.Namespace == ns {
			snap.Vertices = append(snap.Vertices, v)
		}
	}
	for _, e := range s.edges {
		if e.Namespace == ns {
			snap.Edges = append(snap.Edges, e)
		}
	}
	if n := len(snap.Vertices) + len(snap.Edges); n > s.options.MaxSnapshotRecords {
		return Snapshot{}, fmt.Errorf("%w: the namespace holds more records than the snapshot bound of %d", ErrTooLarge, s.options.MaxSnapshotRecords)
	}
	sort.Slice(snap.Vertices, func(i, j int) bool { return snap.Vertices[i].ID < snap.Vertices[j].ID })
	sort.Slice(snap.Edges, func(i, j int) bool { return snap.Edges[i].ID < snap.Edges[j].ID })
	for i := range snap.Vertices {
		snap.Vertices[i] = cloneVertex(snap.Vertices[i])
	}
	for i := range snap.Edges {
		snap.Edges[i] = cloneEdge(snap.Edges[i])
	}
	return snap, nil
}

// Changes returns at most limit retained commits newer than after. Cursors are
// global to the store even when the result is filtered by namespace.
func (s *memoryStore) Changes(ctx context.Context, ns string, after uint64, limit int) ([]Change, error) {
	if err := s.checkLimit(limit); err != nil {
		return nil, err
	}
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	return s.changesLocked(ns, after, limit)
}

func (s *memoryStore) changesLocked(ns string, after uint64, limit int) ([]Change, error) {
	if after < s.floor {
		return nil, ErrCursorExpired
	}
	out := []Change{}
	for _, item := range s.changes {
		if item.change.Cursor > after && (ns == "" || item.change.Namespace == ns) {
			out = append(out, cloneChange(item.change))
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (s *memoryStore) Watch(ctx context.Context, ns string, after uint64, buffer int) (<-chan Change, error) {
	if err := s.checkLimit(buffer); err != nil {
		return nil, err
	}
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	backlog, err := s.changesLocked(ns, after, buffer+1)
	if err != nil {
		return nil, err
	}
	if len(backlog) > buffer {
		return nil, fmt.Errorf("%w: watch backlog exceeds buffer; resume from a later cursor", ErrTooLarge)
	}
	ch := make(chan Change, buffer)
	for _, change := range backlog {
		ch <- change
		after = change.Cursor
	}
	s.nextWatcher++
	id := s.nextWatcher
	w := &watcher{namespace: ns, cursor: after, ch: ch}
	s.watchers[id] = w
	go func() {
		select {
		case <-ctx.Done():
		case <-s.done:
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, ok := s.watchers[id]; ok && current == w {
			delete(s.watchers, id)
			close(ch)
		}
	}()
	return ch, nil
}

func (s *memoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.done)
	for id, w := range s.watchers {
		delete(s.watchers, id)
		close(w.ch)
	}
	return nil
}
