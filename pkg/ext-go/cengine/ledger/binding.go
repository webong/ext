// Package ledger binds the canonical C ledger through its described library
// interface. Store and recorder operations execute in C; Go retains wire types.
package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	wire "github.com/webong/ext/pkg/ext-go/core/ledger/observation"
)

type Library struct{ api *library.Described }

// Bind binds an explicitly supplied native library. Loading and installation
// policy belong to the application; the binding never falls back to a Go engine.
func Bind(native *library.Library) (*Library, error) {
	if native == nil {
		return nil, errors.New("ledger: native library is required")
	}
	api, err := library.BindInterface(native, "ext_ledger", 1)
	if err != nil {
		return nil, err
	}
	return BindInterface(api)
}

// BindInterface accepts the canonical described ABI from any explicit driver.
func BindInterface(api *library.Interface) (*Library, error) {
	if api == nil || api.Prefix() != "ext_ledger" {
		return nil, errors.New("ledger: ext_ledger interface required")
	}
	d, err := library.Describe(api)
	if err != nil {
		return nil, err
	}
	if d.Description.ABIVersion != 1 {
		return nil, errors.New("ledger: unsupported described ABI")
	}
	return &Library{api: d}, nil
}
func call(ctx context.Context, o *library.Object, op string, in map[string]any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The synchronous ABI accepts deadlines. Cancellation is checked before
	// entry; an already-entered call is not interrupted by closing a shared store.
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		in["timeout_ms"] = (remaining + time.Millisecond - 1) / time.Millisecond
	}
	err := o.Call(op, in, out)
	var code *library.CodeError
	if errors.As(err, &code) {
		switch code.Name {
		case "canceled":
			return context.Canceled
		case "timeout":
			return context.DeadlineExceeded
		}
	}
	return err
}
func (l *Library) Collect(ctx context.Context, what string, options map[string]any, out any) error {
	in := map[string]any{"what": what}
	for k, v := range options {
		in[k] = v
	}
	return call(ctx, l.api.Library(), "collect", in, out)
}

// FilesystemAt asks the C ledger for the mount containing one existing path.
func (l *Library) FilesystemAt(ctx context.Context, path string) (wire.FilesystemInfo, error) {
	var filesystem wire.FilesystemInfo
	err := call(ctx, l.api.Library(), "host_path", map[string]any{"what": "filesystem", "path": path}, &filesystem)
	return filesystem, err
}

// CheckHostAccess checks execute or directory-write access without a probe file.
func (l *Library) CheckHostAccess(ctx context.Context, path, what string) error {
	if what != "executable_access" && what != "directory_writable" {
		return errors.New("ledger: invalid host access check")
	}
	return call(ctx, l.api.Library(), "host_path", map[string]any{"what": what, "path": path}, nil)
}

// ProcessStart reads the OS birth token for a PID so an action can reject a
// process generation different from the one it observed.
func (l *Library) ProcessStart(ctx context.Context, pid int) (string, error) {
	var out struct {
		StartID string `json:"start_id"`
	}
	err := call(ctx, l.api.Library(), "process_start", map[string]any{"pid": pid}, &out)
	return out.StartID, err
}

// Program is a named executable observed on a caller-supplied PATH. The
// ledger checks and resolves it without running it; selection remains with
// the caller.
type Program struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	RealPath string `json:"realpath"`
	Source   string `json:"source"`
	OnPath   bool   `json:"on_path"`
}

// ScanPrograms asks the C ledger to discover named executables on searchPath.
func (l *Library) ScanPrograms(ctx context.Context, names []string, searchPath string) ([]Program, error) {
	var programs []Program
	err := call(ctx, l.api.Library(), "program_scan", map[string]any{"names": names, "search_path": searchPath}, &programs)
	return programs, err
}

// InspectProgram checks one caller-declared executable path without running it.
func (l *Library) InspectProgram(ctx context.Context, name, path string) (Program, error) {
	var program Program
	err := call(ctx, l.api.Library(), "program_inspect", map[string]any{"name": name, "path": path}, &program)
	return program, err
}

// ServiceSpec describes a caller-selected per-user OS service. The C ledger
// validates and renders it; a platform adapter registers the definition with
// its native manager.
type ServiceSpec struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Executable       string            `json:"executable"`
	Arguments        []string          `json:"arguments,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Restart          string            `json:"restart,omitempty"`
	RestartSeconds   int               `json:"restart_seconds,omitempty"`
	UserID           string            `json:"user_id,omitempty"`
}

type ServiceDefinition struct {
	Platform string `json:"platform"`
	Identity string `json:"identity"`
	FileName string `json:"file_name"`
	Content  string `json:"content"`
	Digest   string `json:"digest"`
}

// RenderService produces a platform definition without invoking a manager.
func (l *Library) RenderService(ctx context.Context, platform string, spec ServiceSpec) (ServiceDefinition, error) {
	var definition ServiceDefinition
	err := call(ctx, l.api.Library(), "service_render", map[string]any{"platform": platform, "spec": spec}, &definition)
	return definition, err
}

// ServiceInspection reports the state of a rendered definition in one
// caller-selected directory. Created and Existing describe a publish attempt.
type ServiceInspection struct {
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Matches   bool   `json:"matches"`
	Created   bool   `json:"created"`
	Existing  bool   `json:"existing"`
	Digest    string `json:"digest,omitempty"`
}

func (l *Library) serviceFile(ctx context.Context, operation string, definition ServiceDefinition, directory string) (ServiceInspection, error) {
	var result ServiceInspection
	err := call(ctx, l.api.Library(), operation, map[string]any{"definition": definition, "directory": directory}, &result)
	return result, err
}

// PublishService writes the definition without replacing an existing file.
func (l *Library) PublishService(ctx context.Context, definition ServiceDefinition, directory string) (ServiceInspection, error) {
	return l.serviceFile(ctx, "service_publish", definition, directory)
}

// InspectService checks the installed definition against the expected content.
func (l *Library) InspectService(ctx context.Context, definition ServiceDefinition, directory string) (ServiceInspection, error) {
	return l.serviceFile(ctx, "service_inspect", definition, directory)
}

// RemoveService removes the definition only while its content still matches.
func (l *Library) RemoveService(ctx context.Context, definition ServiceDefinition, directory string) (ServiceInspection, error) {
	return l.serviceFile(ctx, "service_remove", definition, directory)
}
func (l *Library) OpenStore(ctx context.Context, backend, path string, options Options) (*NativeStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o, err := l.api.Open("store", map[string]any{"backend": backend, "path": path, "options": options}, nil)
	if err != nil {
		return nil, err
	}
	return &NativeStore{object: o, library: l}, nil
}

type NativeStore struct {
	watchers map[*NativeWatch]struct{}
	mu       sync.Mutex
	closed   bool
	object   *library.Object
	library  *Library
}

func (s *NativeStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	for w := range s.watchers {
		if err := w.Close(); err != nil {
			return err
		}
	}
	err := s.object.Close()
	if err == nil {
		s.closed = true
	}
	return err
}
func (s *NativeStore) call(ctx context.Context, op string, in map[string]any, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return call(ctx, s.object, op, in, out)
}
func (s *NativeStore) Register(ctx context.Context, schema Schema) error {
	return s.call(ctx, "register", map[string]any{"schema": schema}, nil)
}
func (s *NativeStore) Apply(ctx context.Context, tx Transaction) (out Commit, err error) {
	err = s.call(ctx, "apply", map[string]any{"transaction": tx}, &out)
	return
}
func (s *NativeStore) Snapshot(ctx context.Context, ns string) (out Snapshot, err error) {
	err = s.call(ctx, "snapshot", map[string]any{"namespace": ns}, &out)
	return
}
func (s *NativeStore) GetVertex(ctx context.Context, ns, id string) (out Vertex, err error) {
	err = s.call(ctx, "get_vertex", map[string]any{"namespace": ns, "id": id}, &out)
	return
}
func (s *NativeStore) GetEdge(ctx context.Context, ns, id string) (out Edge, err error) {
	err = s.call(ctx, "get_edge", map[string]any{"namespace": ns, "id": id}, &out)
	return
}
func (s *NativeStore) QueryVertices(ctx context.Context, q VertexQuery) (out []Vertex, err error) {
	err = s.call(ctx, "query_vertices", map[string]any{"query": q}, &out)
	return
}
func (s *NativeStore) QueryEdges(ctx context.Context, q EdgeQuery) (out []Edge, err error) {
	err = s.call(ctx, "query_edges", map[string]any{"query": q}, &out)
	return
}
func (s *NativeStore) Changes(ctx context.Context, ns string, after uint64, limit int) (out []Change, err error) {
	err = s.call(ctx, "changes", map[string]any{"namespace": ns, "after": after, "limit": limit}, &out)
	return
}

// Objects exposes the complete described C API, including watches and
// supervisors, without requiring a separate hand-maintained wrapper per method.
func (l *Library) Objects() *library.Described { return l.api }

// Reconcile replaces the complete desired namespace at its observed revision.
func (s *NativeStore) Reconcile(ctx context.Context, desired Snapshot) (out Commit, err error) {
	vertices, edges := desired.Vertices, desired.Edges
	if vertices == nil {
		vertices = []Vertex{}
	}
	if edges == nil {
		edges = []Edge{}
	}
	err = s.call(ctx, "reconcile", map[string]any{"transaction": map[string]any{"namespace": desired.Namespace, "expected_revision": desired.Revision, "vertices": vertices, "edges": edges}}, &out)
	return
}

// CanonicalTransaction asks C for the canonical JSON and its idempotency fingerprint.
func (l *Library) CanonicalTransaction(ctx context.Context, tx Transaction) (json.RawMessage, string, error) {
	var result struct {
		Canonical   json.RawMessage `json:"canonical"`
		Fingerprint string          `json:"fingerprint"`
	}
	err := call(ctx, l.api.Library(), "canonical_transaction", map[string]any{"transaction": tx}, &result)
	return result.Canonical, result.Fingerprint, err
}

// ValidateAttributes applies the C ledger's graph metadata rules without writing a record.
func (l *Library) ValidateAttributes(ctx context.Context, attributes map[string]any) error {
	if attributes == nil {
		attributes = map[string]any{}
	}
	return call(ctx, l.api.Library(), "validate_attributes", map[string]any{"attributes": attributes}, nil)
}
