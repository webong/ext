package supervisor

import (
	"context"
	"sync"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	ledger "github.com/webong/ext/pkg/ext-go/engine/ledger"
)

// NativeOptions configures the C supervisor. Ownership and orphan policy remain
// explicit; opening does not silently substitute the compatibility supervisor.
type NativeOptions struct {
	LogLimit          int    `json:"log_limit,omitempty"`
	RuntimeID         string `json:"runtime_id,omitempty"`
	RequireChecksum   bool   `json:"require_checksum,omitempty"`
	OrphanPolicy      string `json:"orphan_policy,omitempty"`
	LeaseMilliseconds int64  `json:"lease_ms,omitempty"`
	RetainTerminal    int    `json:"retain_terminal,omitempty"`
}
type NativeRestart struct {
	MaxAttempts              int   `json:"max_attempts,omitempty"`
	InitialDelayMilliseconds int64 `json:"initial_delay_ms,omitempty"`
	MaxDelayMilliseconds     int64 `json:"max_delay_ms,omitempty"`
}

// NativeSpec is the C wire contract. Readiness and health checks are reported
// explicitly by the application; no Go callback is passed into the engine.
type NativeSpec struct {
	Artifact                     Artifact          `json:"artifact"`
	Command                      string            `json:"command"`
	Args                         []string          `json:"args,omitempty"`
	Dir                          string            `json:"dir,omitempty"`
	Environment                  map[string]string `json:"environment,omitempty"`
	Endpoints                    []Endpoint        `json:"endpoints,omitempty"`
	Connections                  []Connection      `json:"connections,omitempty"`
	Dependencies                 []string          `json:"dependencies,omitempty"`
	Restart                      NativeRestart     `json:"restart,omitempty"`
	AwaitReadiness               bool              `json:"await_readiness,omitempty"`
	ReadinessTimeoutMilliseconds int64             `json:"readiness_timeout_ms,omitempty"`
}

type Native struct {
	mu     sync.Mutex
	object *library.Object
	closed bool
}

func OpenNative(ctx context.Context, store *ledger.NativeStore, options NativeOptions) (*Native, error) {
	if store == nil {
		return nil, ledger.ErrInvalid
	}
	object, err := store.OpenChild(ctx, "supervisor", map[string]any{"options": options})
	if err != nil {
		return nil, err
	}
	return &Native{object: object}, nil
}
func (n *Native) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	err := n.object.Close()
	if err == nil {
		n.closed = true
	}
	return err
}
func (n *Native) call(ctx context.Context, op string, in map[string]any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return ledger.ErrClosed
	}
	if d, ok := ctx.Deadline(); ok {
		remaining := time.Until(d)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		in["timeout_ms"] = (remaining + time.Millisecond - 1) / time.Millisecond
	}
	err := n.object.Call(op, in, out)
	if ledger.Code(err) == "timeout" {
		return context.DeadlineExceeded
	}
	if ledger.Code(err) == "canceled" {
		return context.Canceled
	}
	return err
}
func (n *Native) Start(ctx context.Context, spec NativeSpec, secrets map[string]string) (out Instance, err error) {
	in := map[string]any{"spec": spec}
	if secrets != nil {
		in["secrets"] = secrets
	}
	err = n.call(ctx, "start", in, &out)
	return
}
func (n *Native) Ready(ctx context.Context, id string, pid int, ready bool) error {
	return n.call(ctx, "ready", map[string]any{"id": id, "pid": pid, "ready": ready}, nil)
}
func (n *Native) Await(ctx context.Context, id string, states ...State) (Instance, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Instance{}, err
		}
		poll, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
		var out Instance
		err := n.call(poll, "await", map[string]any{"id": id, "states": states}, &out)
		cancel()
		if ledger.Code(err) == "timeout" {
			continue
		}
		return out, err
	}
}
func (n *Native) Stop(ctx context.Context, id string) error {
	return n.call(ctx, "stop", map[string]any{"id": id}, nil)
}
func (n *Native) Health(ctx context.Context, id string, check bool) (bool, error) {
	var out struct{ Healthy bool }
	err := n.call(ctx, "health", map[string]any{"id": id, "check": check}, &out)
	return out.Healthy, err
}
func (n *Native) Instance(ctx context.Context, id string) (out Instance, err error) {
	err = n.call(ctx, "instance", map[string]any{"id": id}, &out)
	return
}
func (n *Native) Instances(ctx context.Context) (out []Instance, err error) {
	err = n.call(ctx, "instances", map[string]any{}, &out)
	return
}
func (n *Native) Logs(ctx context.Context, id string) (out string, err error) {
	err = n.call(ctx, "logs", map[string]any{"id": id}, &out)
	return
}
func (n *Native) Orphans(ctx context.Context) (out []Instance, err error) {
	err = n.call(ctx, "orphans", map[string]any{}, &out)
	return
}
func (n *Native) ResolveOrphan(ctx context.Context, id string) error {
	return n.call(ctx, "resolve_orphan", map[string]any{"id": id}, nil)
}
