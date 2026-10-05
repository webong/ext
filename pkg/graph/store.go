package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type diskState struct {
	FormatVersion int                   `json:"format_version"`
	Vertices      map[string]Vertex     `json:"vertices"`
	Edges         map[string]Edge       `json:"edges"`
	Revisions     map[string]uint64     `json:"revisions"`
	Schemas       map[string]string     `json:"schemas"`
	Idempotency   map[string]idemRecord `json:"idempotency"`
	Changes       []Change              `json:"changes"`
	Cursor        uint64                `json:"cursor"`
	HistoryFloor  uint64                `json:"history_floor,omitempty"`
}

// Options controls how much replay history a store retains. Zero values use
// defaults of 4096 commits and 16 MiB of serialized changes. The latest commit
// is always kept, even when it alone exceeds MaxChangeBytes. Idempotency
// records older than the floor expire with their commit.
type Options struct {
	MaxChanges     int
	MaxChangeBytes int
}

const defaultMaxChanges = 4096
const defaultMaxChangeBytes = 16 << 20

type idemRecord struct {
	Fingerprint string `json:"fingerprint"`
	Commit      Commit `json:"commit"`
}

type memoryStore struct {
	mu             sync.RWMutex
	path           string
	state          diskState
	schemas        map[string]Schema
	watchers       map[uint64]*watcher
	nextWatcher    uint64
	maxChanges     int
	maxChangeBytes int
	closed         bool
}
type watcher struct {
	namespace string
	cursor    uint64
	ch        chan Change
	dropped   bool
}

// NewMemory creates an in-memory graph store.
func NewMemory() Store { return NewMemoryWithOptions(Options{}) }

func NewMemoryWithOptions(options Options) Store { return newStore("", options) }

// OpenFile opens or creates a durable JSON snapshot store. Commits are written
// through a temporary file and atomically renamed before they become visible.
func OpenFile(path string) (Store, error) {
	return OpenFileWithOptions(path, Options{})
}

func OpenFileWithOptions(path string, options Options) (Store, error) {
	if path == "" {
		return nil, fmt.Errorf("graph file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	s := newStore(path, options)
	lock, err := acquireFileLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := s.readDiskLocked(); err != nil {
		return nil, err
	}
	oldFloor := s.state.HistoryFloor
	if err := s.compactHistory(&s.state); err != nil {
		return nil, err
	}
	if s.state.HistoryFloor != oldFloor {
		if err := s.persistWith(s.state); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func newStore(path string, options Options) *memoryStore {
	if options.MaxChanges <= 0 {
		options.MaxChanges = defaultMaxChanges
	}
	if options.MaxChangeBytes <= 0 {
		options.MaxChangeBytes = defaultMaxChangeBytes
	}
	return &memoryStore{path: path, maxChanges: options.MaxChanges, maxChangeBytes: options.MaxChangeBytes, state: diskState{FormatVersion: 1, Vertices: map[string]Vertex{}, Edges: map[string]Edge{}, Revisions: map[string]uint64{}, Schemas: map[string]string{}, Idempotency: map[string]idemRecord{}, Changes: []Change{}}, schemas: map[string]Schema{}, watchers: map[uint64]*watcher{}}
}
func (s *memoryStore) normalize() {
	if s.state.Vertices == nil {
		s.state.Vertices = map[string]Vertex{}
	}
	if s.state.Edges == nil {
		s.state.Edges = map[string]Edge{}
	}
	if s.state.Revisions == nil {
		s.state.Revisions = map[string]uint64{}
	}
	if s.state.Schemas == nil {
		s.state.Schemas = map[string]string{}
	}
	if s.state.Idempotency == nil {
		s.state.Idempotency = map[string]idemRecord{}
	}
	if s.state.Changes == nil {
		s.state.Changes = []Change{}
	}
}
func recordKey(ns, id string) string { return ns + "\x00" + id }

func (s *memoryStore) Register(schema Schema) error {
	if err := validateName(schema.Namespace); err != nil {
		return err
	}
	if schema.Version == "" {
		return fmt.Errorf("schema version is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("graph store is closed")
	}
	unlock, err := s.lockDiskLocked()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.refreshDiskLocked(true); err != nil {
		return err
	}
	if old, ok := s.state.Schemas[schema.Namespace]; ok && old != schema.Version {
		return fmt.Errorf("namespace %s already registered at schema version %s", schema.Namespace, old)
	}
	next := cloneState(s.state)
	next.Schemas[schema.Namespace] = schema.Version
	if err := s.persistWith(next); err != nil {
		return err
	}
	s.state = next
	s.schemas[schema.Namespace] = schema
	return nil
}

func (s *memoryStore) Apply(ctx context.Context, tx Transaction) (Commit, error) {
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	if err := validateName(tx.Namespace); err != nil {
		return Commit{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Commit{}, fmt.Errorf("graph store is closed")
	}
	unlock, err := s.lockDiskLocked()
	if err != nil {
		return Commit{}, err
	}
	defer unlock()
	if err := s.refreshDiskLocked(true); err != nil {
		return Commit{}, err
	}
	if _, ok := s.state.Schemas[tx.Namespace]; !ok {
		return Commit{}, fmt.Errorf("namespace %s is not registered", tx.Namespace)
	}
	if _, ok := s.schemas[tx.Namespace]; !ok {
		return Commit{}, fmt.Errorf("namespace %s schema callback is not registered in this process", tx.Namespace)
	}
	fingerprintBytes, err := json.Marshal(tx)
	if err != nil {
		return Commit{}, err
	}
	fingerprintSum := sha256.Sum256(fingerprintBytes)
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	idemKey := ""
	if tx.IdempotencyKey != "" {
		idemKey = tx.Namespace + "\x00" + tx.IdempotencyKey
		if previous, ok := s.state.Idempotency[idemKey]; ok {
			if previous.Fingerprint != fingerprint {
				return Commit{}, ErrIdempotency
			}
			c := previous.Commit
			c.Replayed = true
			return c, nil
		}
	}
	currentRevision := s.state.Revisions[tx.Namespace]
	if tx.ExpectedRevision != nil && *tx.ExpectedRevision != currentRevision {
		return Commit{}, fmt.Errorf("%w: namespace %s expected %d, current %d", ErrConflict, tx.Namespace, *tx.ExpectedRevision, currentRevision)
	}
	next := cloneState(s.state)
	revision := currentRevision + 1
	now := time.Now().UTC()
	change := Change{Namespace: tx.Namespace, Revision: revision, Committed: now}
	for _, ref := range tx.DeleteEdges {
		if ref.Namespace != tx.Namespace {
			return Commit{}, fmt.Errorf("cross-namespace deletion")
		}
		key := recordKey(ref.Namespace, ref.ID)
		if _, ok := next.Edges[key]; !ok {
			return Commit{}, ErrNotFound
		}
		delete(next.Edges, key)
		change.DeletedEdges = append(change.DeletedEdges, ref.ID)
	}
	for _, ref := range tx.DeleteVertices {
		if ref.Namespace != tx.Namespace {
			return Commit{}, fmt.Errorf("cross-namespace deletion")
		}
		key := recordKey(ref.Namespace, ref.ID)
		if _, ok := next.Vertices[key]; !ok {
			return Commit{}, ErrNotFound
		}
		for ek, edge := range next.Edges {
			if edge.Namespace == tx.Namespace && (edge.From == ref.ID || edge.To == ref.ID) {
				if !tx.DeleteIncidentEdges {
					return Commit{}, ErrIncidentEdges
				}
				delete(next.Edges, ek)
				change.DeletedEdges = append(change.DeletedEdges, edge.ID)
			}
		}
		delete(next.Vertices, key)
		change.DeletedVertices = append(change.DeletedVertices, ref.ID)
	}
	for _, vertex := range tx.Vertices {
		if vertex.Namespace != "" && vertex.Namespace != tx.Namespace {
			return Commit{}, fmt.Errorf("vertex namespace does not match transaction")
		}
		if vertex.ID == "" || validateName(vertex.Kind) != nil || !strings.HasPrefix(vertex.Kind, tx.Namespace+"/") {
			return Commit{}, fmt.Errorf("vertex requires ID and kind under namespace %s", tx.Namespace)
		}
		if err := validateAttributes(vertex.Attributes); err != nil {
			return Commit{}, err
		}
		if err := validateLabels(vertex.Labels); err != nil {
			return Commit{}, err
		}
		if err := validateProvenance(vertex.Provenance); err != nil {
			return Commit{}, err
		}
		key := recordKey(tx.Namespace, vertex.ID)
		old, exists := next.Vertices[key]
		if exists {
			vertex.CreatedAt = old.CreatedAt
		} else if vertex.CreatedAt.IsZero() {
			vertex.CreatedAt = now
		}
		vertex.Namespace = tx.Namespace
		vertex.Revision = revision
		vertex.UpdatedAt = now
		next.Vertices[key] = cloneVertex(vertex)
		change.Vertices = append(change.Vertices, cloneVertex(vertex))
	}
	for _, edge := range tx.Edges {
		if edge.Namespace != "" && edge.Namespace != tx.Namespace {
			return Commit{}, fmt.Errorf("edge namespace does not match transaction")
		}
		if edge.ID == "" || edge.From == "" || edge.To == "" || validateName(edge.Type) != nil || !strings.HasPrefix(edge.Type, tx.Namespace+"/") {
			return Commit{}, fmt.Errorf("edge requires ID, endpoints, and relationship under namespace %s", tx.Namespace)
		}
		if err := validateAttributes(edge.Attributes); err != nil {
			return Commit{}, err
		}
		if err := validateLabels(edge.Labels); err != nil {
			return Commit{}, err
		}
		if err := validateProvenance(edge.Provenance); err != nil {
			return Commit{}, err
		}
		if _, ok := next.Vertices[recordKey(tx.Namespace, edge.From)]; !ok {
			return Commit{}, fmt.Errorf("%w: %s", ErrDanglingEdge, edge.From)
		}
		if _, ok := next.Vertices[recordKey(tx.Namespace, edge.To)]; !ok {
			return Commit{}, fmt.Errorf("%w: %s", ErrDanglingEdge, edge.To)
		}
		key := recordKey(tx.Namespace, edge.ID)
		old, exists := next.Edges[key]
		if exists {
			edge.CreatedAt = old.CreatedAt
		} else if edge.CreatedAt.IsZero() {
			edge.CreatedAt = now
		}
		edge.Namespace = tx.Namespace
		edge.Revision = revision
		edge.UpdatedAt = now
		next.Edges[key] = cloneEdge(edge)
		change.Edges = append(change.Edges, cloneEdge(edge))
	}
	view := stateView{state: next}
	if schema, ok := s.schemas[tx.Namespace]; ok && schema.Validate != nil {
		if err := schema.Validate(view, tx); err != nil {
			return Commit{}, fmt.Errorf("schema %s validation: %w", tx.Namespace, err)
		}
	}
	next.Revisions[tx.Namespace] = revision
	next.Cursor++
	change.Cursor = next.Cursor
	commit := Commit{Namespace: tx.Namespace, Revision: revision, Cursor: next.Cursor, Committed: now}
	next.Changes = append(next.Changes, change)
	if idemKey != "" {
		next.Idempotency[idemKey] = idemRecord{Fingerprint: fingerprint, Commit: commit}
	}
	if err := s.compactHistory(&next); err != nil {
		return Commit{}, err
	}
	if err := s.persistWith(next); err != nil {
		return Commit{}, err
	}
	s.state = next
	s.publishLocked(change)
	return commit, nil
}

func (s *memoryStore) compactHistory(st *diskState) error {
	changeBytes := 0
	changeSizes := make([]int, len(st.Changes))
	for i, item := range st.Changes {
		encoded, err := json.Marshal(item)
		if err != nil {
			return err
		}
		changeSizes[i] = len(encoded)
		changeBytes += len(encoded)
	}
	removed := 0
	for len(st.Changes)-removed > 1 && (len(st.Changes)-removed > s.maxChanges || changeBytes > s.maxChangeBytes) {
		changeBytes -= changeSizes[removed]
		st.HistoryFloor = st.Changes[removed].Cursor
		removed++
	}
	if removed > 0 {
		st.Changes = append([]Change(nil), st.Changes[removed:]...)
		for key, record := range st.Idempotency {
			if record.Commit.Cursor <= st.HistoryFloor {
				delete(st.Idempotency, key)
			}
		}
	}
	return nil
}

func (s *memoryStore) publishLocked(change Change) {
	for id, w := range s.watchers {
		if w.cursor < s.state.HistoryFloor {
			close(w.ch)
			delete(s.watchers, id)
			continue
		}
		if w.namespace != "" && w.namespace != change.Namespace {
			continue
		}
		if change.Cursor <= w.cursor {
			continue
		}
		select {
		case w.ch <- cloneChange(change):
			w.cursor = change.Cursor
		default:
			w.dropped = true
			close(w.ch)
			delete(s.watchers, id)
		}
	}
}

func (s *memoryStore) GetVertex(ctx context.Context, ns, id string) (Vertex, error) {
	if err := ctx.Err(); err != nil {
		return Vertex{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(true); err != nil {
		return Vertex{}, err
	}
	v, ok := s.state.Vertices[recordKey(ns, id)]
	if !ok {
		return Vertex{}, ErrNotFound
	}
	return cloneVertex(v), nil
}
func (s *memoryStore) GetEdge(ctx context.Context, ns, id string) (Edge, error) {
	if err := ctx.Err(); err != nil {
		return Edge{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(true); err != nil {
		return Edge{}, err
	}
	v, ok := s.state.Edges[recordKey(ns, id)]
	if !ok {
		return Edge{}, ErrNotFound
	}
	return cloneEdge(v), nil
}
func (s *memoryStore) QueryVertices(ctx context.Context, q VertexQuery) ([]Vertex, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.Limit <= 0 {
		return nil, ErrLimitRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(true); err != nil {
		return nil, err
	}
	out := []Vertex{}
	for _, v := range s.state.Vertices {
		if v.Namespace == q.Namespace && (q.Kind == "" || v.Kind == q.Kind) {
			out = append(out, cloneVertex(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}
func (s *memoryStore) QueryEdges(ctx context.Context, q EdgeQuery) ([]Edge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.Limit <= 0 {
		return nil, ErrLimitRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(true); err != nil {
		return nil, err
	}
	out := []Edge{}
	for _, e := range s.state.Edges {
		if e.Namespace == q.Namespace && (q.Type == "" || e.Type == q.Type) && (q.From == "" || e.From == q.From) && (q.To == "" || e.To == q.To) {
			out = append(out, cloneEdge(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (s *memoryStore) Traverse(ctx context.Context, ns, start string, maxDepth, maxVertices int) ([]Path, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxDepth < 0 || maxVertices <= 0 {
		return nil, ErrLimitRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(true); err != nil {
		return nil, err
	}
	_, ok := s.state.Vertices[recordKey(ns, start)]
	if !ok {
		return nil, ErrNotFound
	}
	type state struct {
		ids   []string
		edges []Edge
	}
	queue := []state{{ids: []string{start}}}
	paths := []Path{}
	visited := map[string]bool{start: true}
	for len(queue) > 0 && len(visited) <= maxVertices {
		cur := queue[0]
		queue = queue[1:]
		last := cur.ids[len(cur.ids)-1]
		path := Path{Vertices: []Vertex{}, Edges: append([]Edge(nil), cur.edges...)}
		for _, id := range cur.ids {
			path.Vertices = append(path.Vertices, cloneVertex(s.state.Vertices[recordKey(ns, id)]))
		}
		if len(cur.ids) > 1 {
			paths = append(paths, path)
		}
		if len(cur.edges) >= maxDepth {
			continue
		}
		edges := []Edge{}
		for _, e := range s.state.Edges {
			if e.Namespace == ns && e.From == last {
				edges = append(edges, e)
			}
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
		for _, e := range edges {
			if len(visited) >= maxVertices {
				break
			}
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			ids := append(append([]string(nil), cur.ids...), e.To)
			es := append(append([]Edge(nil), cur.edges...), cloneEdge(e))
			queue = append(queue, state{ids: ids, edges: es})
		}
	}
	if len(visited) > maxVertices {
		return paths, nil
	}
	return paths, nil
}

func (s *memoryStore) Snapshot(ctx context.Context, ns string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(true); err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Namespace: ns, Revision: s.state.Revisions[ns], Cursor: s.state.Cursor, HistoryFloor: s.state.HistoryFloor, Vertices: []Vertex{}, Edges: []Edge{}}
	for _, v := range s.state.Vertices {
		if v.Namespace == ns {
			snap.Vertices = append(snap.Vertices, cloneVertex(v))
		}
	}
	for _, e := range s.state.Edges {
		if e.Namespace == ns {
			snap.Edges = append(snap.Edges, cloneEdge(e))
		}
	}
	sort.Slice(snap.Vertices, func(i, j int) bool { return snap.Vertices[i].ID < snap.Vertices[j].ID })
	sort.Slice(snap.Edges, func(i, j int) bool { return snap.Edges[i].ID < snap.Edges[j].ID })
	return snap, nil
}

// Changes returns at most limit retained commits newer than after. Cursors are
// global to the store even when the result is filtered by namespace.
func (s *memoryStore) Changes(ctx context.Context, ns string, after uint64, limit int) ([]Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, ErrLimitRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.refreshLocked(true); err != nil {
		return nil, err
	}
	if after < s.state.HistoryFloor {
		return nil, ErrCursorExpired
	}
	out := []Change{}
	for _, change := range s.state.Changes {
		if change.Cursor > after && (ns == "" || change.Namespace == ns) {
			out = append(out, cloneChange(change))
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (s *memoryStore) Watch(ctx context.Context, ns string, after uint64, buffer int) (<-chan Change, error) {
	if buffer <= 0 {
		return nil, ErrLimitRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("graph store is closed")
	}
	if err := s.refreshLocked(true); err != nil {
		return nil, err
	}
	if after < s.state.HistoryFloor {
		return nil, ErrCursorExpired
	}
	ch := make(chan Change, buffer)
	for _, change := range s.state.Changes {
		if change.Cursor > after && (ns == "" || change.Namespace == ns) {
			if len(ch) == cap(ch) {
				close(ch)
				return nil, fmt.Errorf("watch backlog exceeds buffer; resume from cursor")
			}
			ch <- cloneChange(change)
			after = change.Cursor
		}
	}
	s.nextWatcher++
	id := s.nextWatcher
	w := &watcher{namespace: ns, cursor: after, ch: ch}
	s.watchers[id] = w
	go func() {
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.mu.Lock()
				if current, ok := s.watchers[id]; ok && current == w {
					delete(s.watchers, id)
					close(ch)
				}
				s.mu.Unlock()
				return
			case <-ticker.C:
				s.mu.Lock()
				if _, ok := s.watchers[id]; ok {
					_ = s.refreshLocked(true)
				}
				s.mu.Unlock()
			}
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
	for id, w := range s.watchers {
		delete(s.watchers, id)
		close(w.ch)
	}
	return nil
}

type stateView struct{ state diskState }

func (v stateView) Vertex(ns, id string) (Vertex, bool) {
	x, ok := v.state.Vertices[recordKey(ns, id)]
	return cloneVertex(x), ok
}
func (v stateView) Edge(ns, id string) (Edge, bool) {
	x, ok := v.state.Edges[recordKey(ns, id)]
	return cloneEdge(x), ok
}
func (v stateView) Vertices(ns string, limit int) ([]Vertex, error) {
	return viewVertices(v.state, ns, limit)
}
func (v stateView) Edges(ns string, limit int) ([]Edge, error) { return viewEdges(v.state, ns, limit) }
func viewVertices(st diskState, ns string, limit int) ([]Vertex, error) {
	if limit <= 0 {
		return nil, ErrLimitRequired
	}
	out := []Vertex{}
	for _, v := range st.Vertices {
		if v.Namespace == ns {
			out = append(out, cloneVertex(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func viewEdges(st diskState, ns string, limit int) ([]Edge, error) {
	if limit <= 0 {
		return nil, ErrLimitRequired
	}
	out := []Edge{}
	for _, v := range st.Edges {
		if v.Namespace == ns {
			out = append(out, cloneEdge(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memoryStore) persistWith(st diskState) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if dir, openErr := os.Open(filepath.Dir(s.path)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *memoryStore) lockDiskLocked() (func(), error) {
	if s.path == "" {
		return func() {}, nil
	}
	lock, err := acquireFileLock(s.path + ".lock")
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.Close() }, nil
}

// refreshLocked serializes a disk refresh with other processes. Caller holds s.mu.
func (s *memoryStore) refreshLocked(publish bool) error {
	unlock, err := s.lockDiskLocked()
	if err != nil {
		return err
	}
	defer unlock()
	return s.refreshDiskLocked(publish)
}

// refreshDiskLocked assumes the cross-process lock is held. Caller holds s.mu.
func (s *memoryStore) refreshDiskLocked(publish bool) error {
	if s.path == "" {
		return nil
	}
	oldCursor := s.state.Cursor
	if err := s.readDiskLocked(); err != nil {
		return err
	}
	for id, w := range s.watchers {
		if w.cursor < s.state.HistoryFloor {
			close(w.ch)
			delete(s.watchers, id)
		}
	}
	if publish && s.state.Cursor > oldCursor {
		for _, change := range s.state.Changes {
			if change.Cursor > oldCursor {
				s.publishLocked(change)
			}
		}
	}
	return nil
}

// readDiskLocked reads a complete snapshot. Atomic rename means readers see
// either the previous or next committed graph.
func (s *memoryStore) readDiskLocked() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var loaded diskState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("read graph store: %w", err)
	}
	if loaded.FormatVersion != 1 {
		return fmt.Errorf("unsupported graph store format %d", loaded.FormatVersion)
	}
	s.state = loaded
	s.normalize()
	return nil
}
func cloneState(st diskState) diskState {
	data, _ := json.Marshal(st)
	var out diskState
	_ = json.Unmarshal(data, &out)
	if out.Vertices == nil || out.Edges == nil || out.Revisions == nil || out.Schemas == nil || out.Idempotency == nil {
		panic("graph state clone failed")
	}
	return out
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
