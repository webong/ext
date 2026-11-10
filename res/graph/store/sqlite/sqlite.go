// Package sqlite is the durable store.Store: the res/graph store contract over
// one SQLite database, in pure Go (modernc.org/sqlite, no cgo).
//
// The tables are created from the declarative layout of the contract
// (res/graph/testdata/store/v1/tables.json) when the database is new. There are
// no migrations: a database with another layout is refused. Short-lived
// commands and long-lived consumers share a database through SQLite's own
// locking; watches poll the durable change cursor.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"modernc.org/sqlite"

	"github.com/webong/ext/res/graph/store"
)

// PollInterval is how often a watch reads the durable cursor for commits made
// by other handles or processes. Commits through the same handle wake it at once.
const PollInterval = 150 * time.Millisecond

type Store struct {
	options store.Options
	layout  layout
	writer  *sql.DB // one connection; transactions begin IMMEDIATE
	reader  *sql.DB

	mu     sync.RWMutex // held for reading by every operation, for writing by Close
	closed bool
	done   chan struct{}
	wg     sync.WaitGroup

	notifyMu sync.Mutex
	notify   chan struct{}
}

var _ store.Store = (*Store)(nil)

// Open opens or creates the store in the SQLite database at path. The file is
// created with mode 0600 and its directory with 0700.
func Open(ctx context.Context, path string, options store.Options) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("%w: graph database path is required", store.ErrInvalid)
	}
	l, err := loadLayout()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err != nil {
		return nil, err
	} else if err := f.Close(); err != nil {
		return nil, err
	}
	pragmas := fmt.Sprintf("_pragma=busy_timeout(%d)&_pragma=journal_mode(%s)&_pragma=foreign_keys(%s)", l.Pragmas.BusyTimeoutMS, l.Pragmas.JournalMode, l.Pragmas.ForeignKeys)
	writer, err := sql.Open("sqlite", path+"?"+pragmas+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	reader, err := sql.Open("sqlite", path+"?"+pragmas)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(4)
	s := &Store{options: options.WithDefaults(), layout: l, writer: writer, reader: reader, done: make(chan struct{}), notify: make(chan struct{})}
	if err := s.init(ctx); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(ctx context.Context) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'store'`).Scan(&tables); err != nil {
		return wrap(err)
	}
	if tables == 0 {
		var other int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&other); err != nil {
			return wrap(err)
		}
		if other > 0 {
			return fmt.Errorf("%w: the database holds other tables", store.ErrUnsupported)
		}
		for _, stmt := range s.layout.statements() {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return wrap(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO store (id, layout, cursor, history_floor) VALUES (1, ?, 0, 0)`, s.layout.Layout); err != nil {
			return wrap(err)
		}
		return wrap(tx.Commit())
	}
	var got string
	if err := tx.QueryRowContext(ctx, `SELECT layout FROM store WHERE id = 1`).Scan(&got); err != nil {
		return fmt.Errorf("%w: %v", store.ErrUnsupported, err)
	}
	if got != s.layout.Layout {
		return fmt.Errorf("%w: layout %q, this store writes %q", store.ErrUnsupported, got, s.layout.Layout)
	}
	return nil
}

// wrap maps SQLite's lock errors to the contract's busy code and leaves
// everything else as io.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
			return fmt.Errorf("%w: %v", store.ErrBusy, err)
		}
	}
	return err
}

func (s *Store) begin(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, store.ErrClosed
	}
	return s.mu.RUnlock, nil
}

func (s *Store) checkLimit(limit int) error {
	if limit <= 0 {
		return store.ErrLimitRequired
	}
	if limit > s.options.MaxResults {
		return fmt.Errorf("%w: limit %d exceeds %d", store.ErrTooLarge, limit, s.options.MaxResults)
	}
	return nil
}

func (s *Store) signal() {
	s.notifyMu.Lock()
	close(s.notify)
	s.notify = make(chan struct{})
	s.notifyMu.Unlock()
}

func (s *Store) changed() <-chan struct{} {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	return s.notify
}

func (s *Store) Register(ctx context.Context, schema store.Schema) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateSchema(schema); err != nil {
		return err
	}
	schema = store.NormalizeSchema(schema)
	encoded, err := store.Canonical(schema)
	if err != nil {
		return err
	}
	release, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	var version, stored string
	err = tx.QueryRowContext(ctx, `SELECT schema_version, schema FROM namespaces WHERE namespace = ?`, schema.Namespace).Scan(&version, &stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO namespaces (namespace, schema_version, schema, revision) VALUES (?, ?, ?, 0)`, schema.Namespace, schema.Version, string(encoded)); err != nil {
			return wrap(err)
		}
	case err != nil:
		return wrap(err)
	case stored == string(encoded):
		return nil
	case version == schema.Version:
		return fmt.Errorf("%w: %s at version %s", store.ErrSchemaMismatch, schema.Namespace, schema.Version)
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE namespaces SET schema_version = ?, schema = ? WHERE namespace = ?`, schema.Version, string(encoded), schema.Namespace); err != nil {
			return wrap(err)
		}
	}
	return wrap(tx.Commit())
}

func (s *Store) Apply(ctx context.Context, t store.Transaction) (store.Commit, error) {
	if err := ctx.Err(); err != nil {
		return store.Commit{}, err
	}
	if err := store.CheckTransaction(t, s.options); err != nil {
		return store.Commit{}, err
	}
	fingerprint, err := store.Fingerprint(t)
	if err != nil {
		return store.Commit{}, err
	}
	release, err := s.begin(ctx)
	if err != nil {
		return store.Commit{}, err
	}
	defer release()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return store.Commit{}, wrap(err)
	}
	defer tx.Rollback()

	var encodedSchema string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT schema, revision FROM namespaces WHERE namespace = ?`, t.Namespace).Scan(&encodedSchema, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Commit{}, fmt.Errorf("%w: %s", store.ErrNotRegistered, t.Namespace)
	} else if err != nil {
		return store.Commit{}, wrap(err)
	}
	var schema store.Schema
	if err := json.Unmarshal([]byte(encodedSchema), &schema); err != nil {
		return store.Commit{}, fmt.Errorf("stored schema of %s: %w", t.Namespace, err)
	}
	if t.IdempotencyKey != "" {
		var stored, committed string
		var rev, cursor int64
		err := tx.QueryRowContext(ctx, `SELECT fingerprint, revision, cursor, committed_at FROM idempotency WHERE namespace = ? AND key = ?`, t.Namespace, t.IdempotencyKey).Scan(&stored, &rev, &cursor, &committed)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return store.Commit{}, wrap(err)
		case stored != fingerprint:
			return store.Commit{}, store.ErrIdempotency
		default:
			at, err := parseTime(committed)
			if err != nil {
				return store.Commit{}, err
			}
			return store.Commit{Namespace: t.Namespace, Revision: uint64(rev), Cursor: uint64(cursor), Committed: at, Replayed: true}, nil
		}
	}
	if t.ExpectedRevision != nil && *t.ExpectedRevision != uint64(revision) {
		return store.Commit{}, fmt.Errorf("%w: namespace %s expected %d, current %d", store.ErrConflict, t.Namespace, *t.ExpectedRevision, revision)
	}
	now := time.Now().UTC()
	change, err := store.Prepare(ctx, txReader{tx: tx, ns: t.Namespace}, schema, uint64(revision)+1, now, t)
	if err != nil {
		return store.Commit{}, err
	}
	var cursor, floor int64
	if err := tx.QueryRowContext(ctx, `SELECT cursor, history_floor FROM store WHERE id = 1`).Scan(&cursor, &floor); err != nil {
		return store.Commit{}, wrap(err)
	}
	cursor++
	change.Cursor = uint64(cursor)
	if err := writeChange(ctx, tx, change); err != nil {
		return store.Commit{}, err
	}
	body, err := store.Canonical(change)
	if err != nil {
		return store.Commit{}, err
	}
	stamp := formatTime(now)
	steps := []struct {
		query string
		args  []any
	}{
		{`UPDATE namespaces SET revision = ? WHERE namespace = ?`, []any{int64(change.Revision), t.Namespace}},
		{`UPDATE store SET cursor = ? WHERE id = 1`, []any{cursor}},
		{`INSERT INTO changes (cursor, namespace, revision, committed_at, size, body) VALUES (?, ?, ?, ?, ?, ?)`, []any{cursor, t.Namespace, int64(change.Revision), stamp, len(body), string(body)}},
	}
	if t.IdempotencyKey != "" {
		steps = append(steps, struct {
			query string
			args  []any
		}{`INSERT INTO idempotency (namespace, key, fingerprint, revision, cursor, committed_at) VALUES (?, ?, ?, ?, ?, ?)`, []any{t.Namespace, t.IdempotencyKey, fingerprint, int64(change.Revision), cursor, stamp}})
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.query, st.args...); err != nil {
			return store.Commit{}, wrap(err)
		}
	}
	if err := s.compact(ctx, tx, floor); err != nil {
		return store.Commit{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.Commit{}, wrap(err)
	}
	s.signal()
	return store.Commit{Namespace: t.Namespace, Revision: change.Revision, Cursor: change.Cursor, Committed: now}, nil
}

func writeChange(ctx context.Context, tx *sql.Tx, change store.Change) error {
	for _, id := range change.DeletedEdges {
		if _, err := tx.ExecContext(ctx, `DELETE FROM edges WHERE namespace = ? AND id = ?`, change.Namespace, id); err != nil {
			return wrap(err)
		}
	}
	for _, id := range change.DeletedVertices {
		if _, err := tx.ExecContext(ctx, `DELETE FROM vertices WHERE namespace = ? AND id = ?`, change.Namespace, id); err != nil {
			return wrap(err)
		}
	}
	for _, v := range change.Vertices {
		labels, attrs, prov, err := encodeRecord(v.Labels, v.Attributes, v.Provenance)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vertices (namespace, id, kind, revision, created_at, updated_at, labels, attributes, provenance)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (namespace, id) DO UPDATE SET kind = excluded.kind, revision = excluded.revision, created_at = excluded.created_at,
			updated_at = excluded.updated_at, labels = excluded.labels, attributes = excluded.attributes, provenance = excluded.provenance`,
			change.Namespace, v.ID, v.Kind, int64(v.Revision), formatTime(v.CreatedAt), formatTime(v.UpdatedAt), labels, attrs, prov); err != nil {
			return wrap(err)
		}
	}
	for _, e := range change.Edges {
		labels, attrs, prov, err := encodeRecord(e.Labels, e.Attributes, e.Provenance)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO edges (namespace, id, type, from_id, to_id, revision, created_at, updated_at, labels, attributes, provenance)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (namespace, id) DO UPDATE SET type = excluded.type, from_id = excluded.from_id, to_id = excluded.to_id, revision = excluded.revision,
			created_at = excluded.created_at, updated_at = excluded.updated_at, labels = excluded.labels, attributes = excluded.attributes, provenance = excluded.provenance`,
			change.Namespace, e.ID, e.Type, e.From, e.To, int64(e.Revision), formatTime(e.CreatedAt), formatTime(e.UpdatedAt), labels, attrs, prov); err != nil {
			return wrap(err)
		}
	}
	return nil
}

// compact drops the oldest commits beyond the history bounds, keeping the
// latest, and the idempotency records of the commits it drops.
func (s *Store) compact(ctx context.Context, tx *sql.Tx, floor int64) error {
	var count, total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM changes`).Scan(&count, &total); err != nil {
		return wrap(err)
	}
	maxChanges, maxBytes := int64(s.options.MaxChanges), int64(s.options.MaxChangeBytes)
	if count <= maxChanges && total <= maxBytes {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT cursor, size FROM changes ORDER BY cursor`)
	if err != nil {
		return wrap(err)
	}
	newFloor := floor
	for rows.Next() && count > 1 && (count > maxChanges || total > maxBytes) {
		var cursor, size int64
		if err := rows.Scan(&cursor, &size); err != nil {
			rows.Close()
			return wrap(err)
		}
		newFloor, count, total = cursor, count-1, total-size
	}
	if err := rows.Close(); err != nil {
		return wrap(err)
	}
	if newFloor == floor {
		return nil
	}
	for _, q := range []string{`DELETE FROM changes WHERE cursor <= ?`, `DELETE FROM idempotency WHERE cursor <= ?`, `UPDATE store SET history_floor = ? WHERE id = 1`} {
		if _, err := tx.ExecContext(ctx, q, newFloor); err != nil {
			return wrap(err)
		}
	}
	return nil
}

func (s *Store) GetVertex(ctx context.Context, ns, id string) (store.Vertex, error) {
	release, err := s.begin(ctx)
	if err != nil {
		return store.Vertex{}, err
	}
	defer release()
	v, ok, err := getVertex(ctx, s.reader, ns, id)
	if err != nil {
		return store.Vertex{}, err
	}
	if !ok {
		return store.Vertex{}, store.ErrNotFound
	}
	return v, nil
}

func (s *Store) GetEdge(ctx context.Context, ns, id string) (store.Edge, error) {
	release, err := s.begin(ctx)
	if err != nil {
		return store.Edge{}, err
	}
	defer release()
	e, ok, err := getEdge(ctx, s.reader, ns, id)
	if err != nil {
		return store.Edge{}, err
	}
	if !ok {
		return store.Edge{}, store.ErrNotFound
	}
	return e, nil
}

func (s *Store) QueryVertices(ctx context.Context, q store.VertexQuery) ([]store.Vertex, error) {
	if err := s.checkLimit(q.Limit); err != nil {
		return nil, err
	}
	release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if q.Kind == "" {
		return queryVertices(ctx, s.reader, `WHERE namespace = ? ORDER BY id LIMIT ?`, q.Namespace, q.Limit)
	}
	return queryVertices(ctx, s.reader, `WHERE namespace = ? AND kind = ? ORDER BY id LIMIT ?`, q.Namespace, q.Kind, q.Limit)
}

func (s *Store) QueryEdges(ctx context.Context, q store.EdgeQuery) ([]store.Edge, error) {
	if err := s.checkLimit(q.Limit); err != nil {
		return nil, err
	}
	release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	where, args := `WHERE namespace = ?`, []any{q.Namespace}
	for _, f := range []struct{ column, value string }{{"type", q.Type}, {"from_id", q.From}, {"to_id", q.To}} {
		if f.value != "" {
			where += ` AND ` + f.column + ` = ?`
			args = append(args, f.value)
		}
	}
	return queryEdges(ctx, s.reader, where+` ORDER BY id LIMIT ?`, append(args, q.Limit)...)
}

func (s *Store) Traverse(ctx context.Context, ns, start string, maxDepth, maxVertices int) ([]store.Path, error) {
	if maxDepth < 0 {
		return nil, store.ErrLimitRequired
	}
	if err := s.checkLimit(maxVertices); err != nil {
		return nil, err
	}
	release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := s.reader.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	return store.Traverse(ctx, txTraversal{tx: tx, ns: ns}, start, maxDepth, maxVertices)
}

func (s *Store) Snapshot(ctx context.Context, ns string) (store.Snapshot, error) {
	release, err := s.begin(ctx)
	if err != nil {
		return store.Snapshot{}, err
	}
	defer release()
	tx, err := s.reader.BeginTx(ctx, nil)
	if err != nil {
		return store.Snapshot{}, wrap(err)
	}
	defer tx.Rollback()
	var cursor, floor int64
	if err := tx.QueryRowContext(ctx, `SELECT cursor, history_floor FROM store WHERE id = 1`).Scan(&cursor, &floor); err != nil {
		return store.Snapshot{}, wrap(err)
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM namespaces WHERE namespace = ?`, ns).Scan(&revision); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return store.Snapshot{}, wrap(err)
	}
	var records int64
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM vertices WHERE namespace = ?) + (SELECT COUNT(*) FROM edges WHERE namespace = ?)`, ns, ns).Scan(&records); err != nil {
		return store.Snapshot{}, wrap(err)
	}
	if records > int64(s.options.MaxSnapshotRecords) {
		return store.Snapshot{}, fmt.Errorf("%w: namespace %s holds %d records, the snapshot bound is %d", store.ErrTooLarge, ns, records, s.options.MaxSnapshotRecords)
	}
	vertices, err := queryVertices(ctx, tx, `WHERE namespace = ? ORDER BY id`, ns)
	if err != nil {
		return store.Snapshot{}, err
	}
	edges, err := queryEdges(ctx, tx, `WHERE namespace = ? ORDER BY id`, ns)
	if err != nil {
		return store.Snapshot{}, err
	}
	return store.Snapshot{Namespace: ns, Revision: uint64(revision), Cursor: uint64(cursor), HistoryFloor: uint64(floor), Vertices: vertices, Edges: edges}, nil
}

// Changes returns at most limit retained commits newer than after. Cursors are
// global to the store even when the result is filtered by namespace.
func (s *Store) Changes(ctx context.Context, ns string, after uint64, limit int) ([]store.Change, error) {
	if err := s.checkLimit(limit); err != nil {
		return nil, err
	}
	release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.changes(ctx, ns, after, limit)
}

func (s *Store) changes(ctx context.Context, ns string, after uint64, limit int) ([]store.Change, error) {
	tx, err := s.reader.BeginTx(ctx, nil)
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	var floor int64
	if err := tx.QueryRowContext(ctx, `SELECT history_floor FROM store WHERE id = 1`).Scan(&floor); err != nil {
		return nil, wrap(err)
	}
	if after < uint64(floor) {
		return nil, store.ErrCursorExpired
	}
	query, args := `SELECT body FROM changes WHERE cursor > ? ORDER BY cursor LIMIT ?`, []any{int64(after), limit}
	if ns != "" {
		query, args = `SELECT body FROM changes WHERE namespace = ? AND cursor > ? ORDER BY cursor LIMIT ?`, []any{ns, int64(after), limit}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []store.Change{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, wrap(err)
		}
		var change store.Change
		if err := json.Unmarshal([]byte(body), &change); err != nil {
			return nil, fmt.Errorf("stored change: %w", err)
		}
		out = append(out, change)
	}
	return out, wrap(rows.Err())
}

// Watch replays the retained changes after the cursor and then delivers later
// commits in cursor order. The channel closes when ctx ends, the store closes,
// the buffer fills or the cursor falls below the history floor; the consumer
// resumes from the last cursor it processed.
func (s *Store) Watch(ctx context.Context, ns string, after uint64, buffer int) (<-chan store.Change, error) {
	if err := s.checkLimit(buffer); err != nil {
		return nil, err
	}
	release, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	backlog, err := s.changes(ctx, ns, after, buffer+1)
	if err != nil {
		return nil, err
	}
	if len(backlog) > buffer {
		return nil, fmt.Errorf("%w: watch backlog exceeds buffer; resume from a later cursor", store.ErrTooLarge)
	}
	ch := make(chan store.Change, buffer)
	for _, change := range backlog {
		ch <- change
		after = change.Cursor
	}
	s.wg.Add(1)
	go s.poll(ctx, ns, after, ch)
	return ch, nil
}

func (s *Store) poll(ctx context.Context, ns string, after uint64, ch chan store.Change) {
	defer s.wg.Done()
	defer close(ch)
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		wake := s.changed()
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-wake:
		case <-ticker.C:
		}
		free := cap(ch) - len(ch)
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return
		}
		changes, err := s.changes(ctx, ns, after, free+1)
		s.mu.RUnlock()
		if err != nil {
			return
		}
		for i, change := range changes {
			if i == free {
				return // the buffer is full
			}
			ch <- change
			after = change.Cursor
		}
	}
}

// Close ends every watch and closes the database. It is safe to call twice.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.writer.Close(), s.reader.Close())
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type txReader struct {
	tx *sql.Tx
	ns string
}

func (r txReader) Vertex(ctx context.Context, id string) (store.Vertex, bool, error) {
	return getVertex(ctx, r.tx, r.ns, id)
}
func (r txReader) Edge(ctx context.Context, id string) (store.Edge, bool, error) {
	return getEdge(ctx, r.tx, r.ns, id)
}
func (r txReader) IncidentEdges(ctx context.Context, id string, limit int) ([]store.Edge, error) {
	return queryEdges(ctx, r.tx, `WHERE namespace = ? AND (from_id = ? OR to_id = ?) ORDER BY id LIMIT ?`, r.ns, id, id, limit)
}

type txTraversal struct {
	tx *sql.Tx
	ns string
}

func (t txTraversal) Vertex(ctx context.Context, id string) (store.Vertex, bool, error) {
	return getVertex(ctx, t.tx, t.ns, id)
}
func (t txTraversal) Outgoing(ctx context.Context, id string) ([]store.Edge, error) {
	return queryEdges(ctx, t.tx, `WHERE namespace = ? AND from_id = ? ORDER BY id`, t.ns, id)
}

func getVertex(ctx context.Context, q querier, ns, id string) (store.Vertex, bool, error) {
	out, err := queryVertices(ctx, q, `WHERE namespace = ? AND id = ?`, ns, id)
	if err != nil || len(out) == 0 {
		return store.Vertex{}, false, err
	}
	return out[0], true, nil
}

func getEdge(ctx context.Context, q querier, ns, id string) (store.Edge, bool, error) {
	out, err := queryEdges(ctx, q, `WHERE namespace = ? AND id = ?`, ns, id)
	if err != nil || len(out) == 0 {
		return store.Edge{}, false, err
	}
	return out[0], true, nil
}

func queryVertices(ctx context.Context, q querier, where string, args ...any) ([]store.Vertex, error) {
	rows, err := q.QueryContext(ctx, `SELECT namespace, id, kind, revision, created_at, updated_at, labels, attributes, provenance FROM vertices `+where, args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []store.Vertex{}
	for rows.Next() {
		var v store.Vertex
		var revision int64
		var created, updated string
		var labels, attrs, prov sql.NullString
		if err := rows.Scan(&v.Namespace, &v.ID, &v.Kind, &revision, &created, &updated, &labels, &attrs, &prov); err != nil {
			return nil, wrap(err)
		}
		v.Revision = uint64(revision)
		if err := decodeRecord(created, updated, labels, attrs, prov, &v.CreatedAt, &v.UpdatedAt, &v.Labels, &v.Attributes, &v.Provenance); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, wrap(rows.Err())
}

func queryEdges(ctx context.Context, q querier, where string, args ...any) ([]store.Edge, error) {
	rows, err := q.QueryContext(ctx, `SELECT namespace, id, type, from_id, to_id, revision, created_at, updated_at, labels, attributes, provenance FROM edges `+where, args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []store.Edge{}
	for rows.Next() {
		var e store.Edge
		var revision int64
		var created, updated string
		var labels, attrs, prov sql.NullString
		if err := rows.Scan(&e.Namespace, &e.ID, &e.Type, &e.From, &e.To, &revision, &created, &updated, &labels, &attrs, &prov); err != nil {
			return nil, wrap(err)
		}
		e.Revision = uint64(revision)
		if err := decodeRecord(created, updated, labels, attrs, prov, &e.CreatedAt, &e.UpdatedAt, &e.Labels, &e.Attributes, &e.Provenance); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, wrap(rows.Err())
}

func encodeRecord(labels map[string]string, attrs map[string]any, p store.Provenance) (l, a, pr sql.NullString, err error) {
	encode := func(empty bool, v any) (sql.NullString, error) {
		if empty {
			return sql.NullString{}, nil
		}
		b, err := store.Canonical(v)
		if err != nil {
			return sql.NullString{}, err
		}
		return sql.NullString{String: string(b), Valid: true}, nil
	}
	if l, err = encode(len(labels) == 0, labels); err != nil {
		return
	}
	if a, err = encode(len(attrs) == 0, attrs); err != nil {
		return
	}
	pr, err = encode(p == (store.Provenance{}), p)
	return
}

func decodeRecord(created, updated string, labels, attrs, prov sql.NullString, c, u *time.Time, l *map[string]string, a *map[string]any, p *store.Provenance) error {
	var err error
	if *c, err = parseTime(created); err != nil {
		return err
	}
	if *u, err = parseTime(updated); err != nil {
		return err
	}
	for _, f := range []struct {
		col sql.NullString
		dst any
	}{{labels, l}, {attrs, a}, {prov, p}} {
		if f.col.Valid {
			if err := json.Unmarshal([]byte(f.col.String), f.dst); err != nil {
				return fmt.Errorf("stored record: %w", err)
			}
		}
	}
	return nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("stored time %q: %w", s, err)
	}
	return t.UTC(), nil
}
