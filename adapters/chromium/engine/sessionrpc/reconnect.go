package sessionrpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	management "github.com/webong/ext/res/browser/contract"
)

// ErrTargetClosed identifies a page lifecycle event, not a transport outage.
var ErrTargetClosed = errors.New("selected native page no longer exists or detached")

type permanentError struct{ error }

func (err permanentError) Unwrap() error { return err.error }

// Permanent marks failures for which another attachment cannot be useful.
func Permanent(err error) error { return permanentError{err} }
func isPermanent(err error) bool {
	var permanent permanentError
	return errors.Is(err, ErrTargetClosed) || errors.As(err, &permanent)
}

type reconnectInjection struct {
	source  string
	options management.InjectionOptions
}

// ReconnectingSession restores native session-owned registrations after a
// transport loss. Native adapters supply recovery for the exact selected page.
// Navigation and immediate injections are never resent implicitly.
type ReconnectingSession struct {
	operation        chan struct{}
	state            sync.Mutex
	current          management.PageSession
	recover          func(context.Context, management.PageSession) (management.PageSession, error)
	registrations    []management.UserscriptRegistration
	injections       []reconnectInjection
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	finished         sync.Once
	err              error
	terminal, closed bool
}

// WithReconnect wraps an established native attachment. A recovery callback may
// return a session with an error to retain native ownership after partial setup.
func WithReconnect(ctx context.Context, initial management.PageSession, recover func(context.Context, management.PageSession) (management.PageSession, error)) *ReconnectingSession {
	lifetime, cancel := context.WithCancel(ctx)
	session := &ReconnectingSession{operation: make(chan struct{}, 1), current: initial, recover: recover, ctx: lifetime, cancel: cancel, done: make(chan struct{})}
	go session.monitor()
	return session
}

func (session *ReconnectingSession) finish(err error) {
	session.finished.Do(func() {
		session.state.Lock()
		session.err, session.terminal = err, true
		session.state.Unlock()
		close(session.done)
	})
}

func (session *ReconnectingSession) acquire(ctx context.Context) error {
	select {
	case session.operation <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (session *ReconnectingSession) release() { <-session.operation }

func disconnected(page management.PageSession) bool {
	state, ok := page.(management.PageSessionState)
	if !ok {
		return false
	}
	select {
	case <-state.Done():
		return true
	default:
		return false
	}
}

func (session *ReconnectingSession) monitor() {
	for {
		if err := session.acquire(session.ctx); err != nil {
			session.finish(err)
			return
		}
		current := session.current
		session.release()
		state, ok := current.(management.PageSessionState)
		if !ok {
			session.finish(errors.New("native page does not expose connection state"))
			return
		}
		select {
		case <-session.done:
			return
		case <-session.ctx.Done():
			session.finish(session.ctx.Err())
			return
		case <-state.Done():
		}
		if err := session.acquire(session.ctx); err != nil {
			session.finish(err)
			return
		}
		if session.current != current {
			session.release()
			continue
		}
		err := session.readyLocked(session.ctx)
		session.release()
		if err != nil {
			session.finish(err)
			return
		}
	}
}

func (session *ReconnectingSession) reconnectLocked(ctx context.Context) (failure error) {
	defer func() {
		// A caller's deadline can interrupt a partially restored attachment.
		// Drop only its transport so the lifetime monitor can finish recovery.
		// Keep a live socket on terminal failures for explicit native cleanup.
		if failure != nil && !isPermanent(failure) && session.ctx.Err() == nil {
			if transport, ok := session.current.(interface{ DropTransport() error }); ok {
				transport.DropTransport()
			}
		}
	}()
	recovery, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	delay := 250 * time.Millisecond
	var last error
	for {
		if recovery.Err() != nil {
			return fmt.Errorf("native page reconnection failed: %w", errors.Join(recovery.Err(), last))
		}
		candidate, err := session.recover(recovery, session.current)
		if candidate != nil {
			session.current = candidate
		}
		if err == nil && candidate == nil {
			return Permanent(errors.New("native recovery returned no page session"))
		}
		if err == nil {
			_, err = candidate.ReplayUserscripts(recovery, session.registrations)
			if err == nil {
				for _, injection := range session.injections {
					if err = candidate.Inject(recovery, injection.source, injection.options); err != nil {
						break
					}
				}
			}
			if err == nil {
				return nil
			}
			var transport *TransportError
			if !disconnected(candidate) && !errors.As(err, &transport) && recovery.Err() == nil {
				return Permanent(fmt.Errorf("restore native page registrations: %w", err))
			}
		}
		last = err
		if isPermanent(err) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-recovery.Done():
			timer.Stop()
			return fmt.Errorf("native page reconnection failed: %w", errors.Join(recovery.Err(), last))
		case <-timer.C:
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

func (session *ReconnectingSession) readyLocked(ctx context.Context) error {
	session.state.Lock()
	closed, terminal, failure := session.closed, session.terminal, session.err
	session.state.Unlock()
	if closed {
		return errors.New("native page session is closed")
	}
	if terminal {
		if failure != nil {
			return failure
		}
		return errors.New("native page session ended")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if disconnected(session.current) {
		state := session.current.(management.PageSessionState)
		if isPermanent(state.Err()) {
			session.finish(state.Err())
			return state.Err()
		}
		err := session.reconnectLocked(ctx)
		if err != nil && ctx.Err() == nil {
			session.finish(err)
		}
		return err
	}
	return nil
}

func (session *ReconnectingSession) commandContext(ctx context.Context) (context.Context, func()) {
	command, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(session.ctx, cancel)
	return command, func() { stop(); cancel() }
}

func (session *ReconnectingSession) Navigate(ctx context.Context, url string) error {
	command, cancel := session.commandContext(ctx)
	defer cancel()
	if err := session.acquire(command); err != nil {
		return err
	}
	defer session.release()
	if err := session.readyLocked(command); err != nil {
		return err
	}
	return session.current.Navigate(command, url)
}
func (session *ReconnectingSession) Inject(ctx context.Context, source string, options management.InjectionOptions) error {
	command, cancel := session.commandContext(ctx)
	defer cancel()
	if err := session.acquire(command); err != nil {
		return err
	}
	defer session.release()
	if err := session.readyLocked(command); err != nil {
		return err
	}
	if err := session.current.Inject(command, source, options); err != nil {
		return err
	}
	if options.RunAt == "document-start" {
		session.injections = append(session.injections, reconnectInjection{source, options})
	}
	return nil
}
func copyRegistrations(records []management.UserscriptRegistration) []management.UserscriptRegistration {
	cloned := make([]management.UserscriptRegistration, len(records))
	for i, record := range records {
		record.Matches = append([]string(nil), record.Matches...)
		record.ExcludeMatches = append([]string(nil), record.ExcludeMatches...)
		cloned[i] = record
	}
	return cloned
}
func (session *ReconnectingSession) ReplayUserscripts(ctx context.Context, records []management.UserscriptRegistration) (management.ReplayResult, error) {
	bounded, boundCancel := context.WithTimeout(ctx, 30*time.Second)
	defer boundCancel()
	command, cancel := session.commandContext(bounded)
	defer cancel()
	if err := session.acquire(command); err != nil {
		return management.ReplayResult{}, err
	}
	defer session.release()
	if err := session.readyLocked(command); err != nil {
		return management.ReplayResult{}, err
	}
	result, err := session.current.ReplayUserscripts(command, records)
	for err != nil && command.Err() == nil {
		var transport *TransportError
		if !disconnected(session.current) && !errors.As(err, &transport) {
			break
		}
		if state, ok := session.current.(management.PageSessionState); ok && isPermanent(state.Err()) {
			session.finish(state.Err())
			return result, state.Err()
		}
		// Replay is idempotent by ID/revision. Restore the last successful set,
		// then reconcile this requested update after the transport recovers.
		if recoveryErr := session.reconnectLocked(command); recoveryErr != nil {
			if command.Err() == nil {
				session.finish(recoveryErr)
			}
			return result, recoveryErr
		}
		result, err = session.current.ReplayUserscripts(command, records)
	}
	if err == nil {
		session.registrations = copyRegistrations(records)
	}
	return result, err
}
func (session *ReconnectingSession) Close(ctx context.Context) error {
	session.cancel()
	if err := session.acquire(ctx); err != nil {
		return err
	}
	defer session.release()
	session.state.Lock()
	if session.closed {
		session.state.Unlock()
		return nil
	}
	session.closed = true
	session.state.Unlock()
	err := session.current.Close(ctx)
	session.finish(err)
	return err
}
func (session *ReconnectingSession) Done() <-chan struct{} { return session.done }
func (session *ReconnectingSession) Err() error {
	session.state.Lock()
	defer session.state.Unlock()
	return session.err
}
