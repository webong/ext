package plugin

import (
	"context"
	"runtime"
	"sync"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// Registry owns native reverse-call routes. C performs admission and cycle
// detection across every attached host, including concurrent call chains.
type Registry struct {
	mu      sync.Mutex
	targets map[engine.ContractRef]*Session
	lib     *Library
	object  *library.Object
}

func (l *Library) NewRegistry() (*Registry, error) {
	o, err := l.api.Open("registry", nil, nil)
	if err != nil {
		return nil, engineError(err)
	}
	r := &Registry{lib: l, object: o, targets: map[engine.ContractRef]*Session{}}
	runtime.SetFinalizer(r, func(r *Registry) { _ = r.object.Close() })
	return r, nil
}
func (r *Registry) Register(ref engine.ContractRef, s *Session) error {
	if s == nil || s.host == nil {
		return engine.ErrInvalid
	}
	r.mu.Lock()
	err := engineError(r.object.Call("register", map[string]any{"name": ref.Name, "version": ref.Version, "target": s.host.Handle()}, nil))
	if err == nil {
		r.targets[ref] = s
	}
	r.mu.Unlock()
	if err == nil {
		go func() {
			<-s.Done()
			r.mu.Lock()
			defer r.mu.Unlock()
			// Do not unregister a replacement installed after an explicit removal.
			if r.targets[ref] == s {
				_ = r.object.Call("unregister", map[string]any{"name": ref.Name, "version": ref.Version}, nil)
				delete(r.targets, ref)
			}
		}()
	}
	return err
}
func (r *Registry) Unregister(ref engine.ContractRef) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := engineError(r.object.Call("unregister", map[string]any{"name": ref.Name, "version": ref.Version}, nil))
	if err == nil {
		delete(r.targets, ref)
	}
	return err
}
func (r *Registry) Route(from engine.ContractRef, operation string, to engine.ContractRef, targetOperation string) error {
	return engineError(r.object.Call("route", map[string]any{"name": from.Name, "version": from.Version, "operation": operation, "target_name": to.Name, "target_version": to.Version, "target_operation": targetOperation}, nil))
}
func (r *Registry) OpenProcess(ctx context.Context, d engine.Descriptor, launch Launch, options engine.Options) (*Session, error) {
	if options.Reverse == nil || options.Reverse.Authorize == nil {
		return nil, engine.ErrDenied
	}
	if options.Reverse.Handle != nil {
		return nil, engine.ErrInvalid
	}
	return r.lib.open(ctx, d, launch, options, r)
}
func (r *Registry) Close() error { runtime.SetFinalizer(r, nil); return engineError(r.object.Close()) }
