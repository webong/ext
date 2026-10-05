package firefox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webong/ctx/adapters/chromium/engine/sessionrpc"
	management "github.com/webong/ctx/res/browser/contract"
	"github.com/webong/ctx/res/browser/userscript"
)

// NewPageSessionRuntime creates a WebDriver BiDi backend for a provider-supplied
// endpoint. Firefox does not publish a Chromium debugging marker in its profile.
func NewPageSessionRuntime(config Config, endpoint string) management.PageSessionRuntime {
	return &bidiPageRuntime{config: config, endpoint: endpoint}
}

type bidiPageRuntime struct {
	config   Config
	endpoint string
}

var errBiDiActive = errors.New("Firefox endpoint already has an active automation session")

type bidiConnection struct {
	client        *sessionrpc.Client
	id, resumeURL string
	owned         bool
}

// openBiDi creates a session on a sessionless endpoint, or attaches to the
// caller's explicit session URL. It never creates a browser process.
func openBiDi(ctx context.Context, endpoint string) (*bidiConnection, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return nil, errors.New("Firefox attachment requires a provider-supplied BiDi WebSocket URL")
	}
	client, err := sessionrpc.Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	// A caller-supplied /session/<id> URL explicitly selects an existing
	// provider-owned session. It already attaches the socket to that session;
	// session.new and session.end would change ownership and are forbidden.
	if split := strings.LastIndex(parsed.Path, "/session/"); split >= 0 {
		id := parsed.Path[split+len("/session/"):]
		if id != "" && !strings.Contains(id, "/") {
			return &bidiConnection{client: client, id: id, resumeURL: endpoint}, nil
		}
	}
	var status struct {
		Ready bool `json:"ready"`
	}
	if err := client.Call(ctx, "", "session.status", map[string]any{}, &status); err != nil {
		client.Close()
		return nil, err
	}
	if !status.Ready {
		client.Close()
		return nil, errBiDiActive
	}
	var created struct {
		ID           string `json:"sessionId"`
		Capabilities struct {
			WebSocket json.RawMessage `json:"webSocketUrl"`
		} `json:"capabilities"`
	}
	if err := client.Call(ctx, "", "session.new", map[string]any{"capabilities": map[string]any{"alwaysMatch": map[string]any{"webSocketUrl": true}}}, &created); err != nil {
		client.Close()
		return nil, err
	}
	if created.ID == "" {
		endBiDi(context.Background(), client)
		return nil, errors.New("Firefox returned no BiDi session ID")
	}
	connection := &bidiConnection{client: client, id: created.ID, owned: true}
	var resume string
	if json.Unmarshal(created.Capabilities.WebSocket, &resume) == nil && resume != "" {
		address, err := url.Parse(resume)
		// Only the URL advertised by this newly created session can establish
		// ownership. Do not synthesize /session/<id>: direct Firefox BiDi does
		// not register that route. Keep recovery on the provider's same origin.
		if err == nil && address.User == nil && address.Scheme == parsed.Scheme && address.Host == parsed.Host && address.String() != parsed.String() {
			connection.resumeURL = address.String()
		}
	}
	return connection, nil
}

// endBiDi releases the attachment's automation session. It never sends
// browser.close or browsingContext.close to the caller's browser.
func endBiDi(ctx context.Context, client *sessionrpc.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := client.Call(ctx, "", "session.end", map[string]any{}, nil)
	return errors.Join(err, client.Close())
}

func closeBiDi(ctx context.Context, connection *bidiConnection) error {
	if !connection.owned {
		return connection.client.Close()
	}
	return endBiDi(ctx, connection.client)
}

type bidiContext struct {
	ID       string        `json:"context"`
	URL      string        `json:"url"`
	Children []bidiContext `json:"children"`
}

func bidiContexts(ctx context.Context, client *sessionrpc.Client, root string) ([]bidiContext, error) {
	params := map[string]any{}
	if root != "" {
		params["root"] = root
	}
	var tree struct {
		Contexts []bidiContext `json:"contexts"`
	}
	if err := client.Call(ctx, "", "browsingContext.getTree", params, &tree); err != nil {
		return nil, err
	}
	return tree.Contexts, nil
}

func (runtime *bidiPageRuntime) DiscoverTargets(ctx context.Context, profile string) ([]management.SessionTarget, error) {
	if !runtime.config.NativeExtensions {
		return nil, errors.New("this adapter has no native BiDi page route")
	}
	connection, err := openBiDi(ctx, runtime.endpoint)
	if err != nil {
		return nil, err
	}
	contexts, err := bidiContexts(ctx, connection.client, "")
	cleanupErr := closeBiDi(context.Background(), connection)
	if err != nil || cleanupErr != nil {
		return nil, errors.Join(err, cleanupErr)
	}
	targets := make([]management.SessionTarget, 0, len(contexts))
	for _, item := range contexts {
		targets = append(targets, management.SessionTarget{ID: item.ID, Protocol: "bidi", Endpoint: runtime.endpoint, URL: item.URL})
	}
	return targets, nil
}

func (runtime *bidiPageRuntime) Connect(ctx context.Context, profile string, target management.SessionTarget) (management.PageSession, error) {
	if !runtime.config.NativeExtensions {
		return nil, errors.New("this adapter has no native BiDi page route")
	}
	if (target.Protocol != "bidi" && target.Protocol != "webdriver-bidi") || target.ID == "" || target.Endpoint == "" {
		return nil, errors.New("BiDi attachment requires a caller-selected page context and endpoint")
	}
	connection, err := openBiDi(ctx, target.Endpoint)
	if err != nil {
		return nil, err
	}
	page, err := attachBiDi(ctx, target.ID, connection, nil)
	if err != nil {
		if page != nil {
			page.Close(context.Background())
		} else {
			closeBiDi(context.Background(), connection)
		}
		return nil, err
	}
	return sessionrpc.WithReconnect(ctx, page, func(recovery context.Context, previous management.PageSession) (management.PageSession, error) {
		old := previous.(*bidiPageSession)
		old.client.Close()
		var resumeErr error
		if old.connection.resumeURL != "" {
			client, err := sessionrpc.Dial(recovery, old.connection.resumeURL)
			if err == nil {
				connection := &bidiConnection{client: client, id: old.connection.id, resumeURL: old.connection.resumeURL, owned: old.connection.owned}
				candidate, err := attachBiDi(recovery, target.ID, connection, old)
				if candidate != nil {
					return candidate, err
				}
				client.Close()
				resumeErr = err
			} else {
				resumeErr = err
			}
		}
		if !old.connection.owned {
			return nil, resumeErr
		}
		connection, err := openBiDi(recovery, target.Endpoint)
		if err != nil {
			if errors.Is(err, errBiDiActive) && old.connection.resumeURL == "" {
				return nil, sessionrpc.Permanent(fmt.Errorf("Firefox session %s remains active after transport loss; the direct BiDi provider supplied no resumable session URL: %w", old.connection.id, err))
			}
			return nil, errors.Join(resumeErr, err)
		}
		// A ready endpoint has released the previous session and its preloads.
		// A fresh session still attaches only the originally selected context.
		candidate, err := attachBiDi(recovery, target.ID, connection, nil)
		if candidate == nil {
			closeBiDi(context.Background(), connection)
			return nil, err
		}
		return candidate, err
	}), nil
}

func attachBiDi(ctx context.Context, contextID string, connection *bidiConnection, previous *bidiPageSession) (*bidiPageSession, error) {
	client := connection.client
	page := &bidiPageSession{client: client, connection: connection, id: contextID, scripts: map[string]bidiRegistration{}, done: make(chan struct{})}
	if previous != nil {
		for id, registration := range previous.scripts {
			page.scripts[id] = registration
		}
		page.injections = append([]string(nil), previous.injections...)
		page.subscription = previous.subscription
	}
	client.OnEvent(func(event sessionrpc.Event) {
		if event.Method != "browsingContext.contextDestroyed" {
			return
		}
		var destroyed struct {
			ID string `json:"context"`
		}
		if json.Unmarshal(event.Params, &destroyed) == nil && destroyed.ID == page.id {
			page.targetClosed.Store(true)
			page.finished.Do(func() { close(page.done) })
		}
	})
	go func() { <-client.Done(); page.finished.Do(func() { close(page.done) }) }()
	contexts, err := bidiContexts(ctx, client, contextID)
	if err != nil {
		var rejected *sessionrpc.ProtocolError
		if errors.As(err, &rejected) {
			err = sessionrpc.Permanent(err)
		}
		return page, err
	}
	if len(contexts) != 1 || contexts[0].ID != contextID {
		page.targetClosed.Store(true)
		return page, sessionrpc.ErrTargetClosed
	}
	if previous != nil {
		// Preloads belong to the BiDi session and survive socket loss. Remove
		// only our recorded handles before rebuilding the desired state.
		if err := page.clearPreloads(ctx); err != nil {
			var rejected *sessionrpc.ProtocolError
			if errors.As(err, &rejected) {
				err = sessionrpc.Permanent(err)
			}
			return page, err
		}
		if page.subscription != "" {
			if err := page.unsubscribe(ctx); err != nil {
				return page, err
			}
		}
	}
	var subscribed struct {
		ID string `json:"subscription"`
	}
	if err := client.Call(ctx, "", "session.subscribe", map[string]any{"events": []string{"browsingContext.contextDestroyed"}, "contexts": []string{page.id}}, &subscribed); err != nil {
		var rejected *sessionrpc.ProtocolError
		if errors.As(err, &rejected) {
			err = sessionrpc.Permanent(err)
		}
		return page, err
	}
	page.subscription = subscribed.ID
	return page, nil
}

type bidiRegistration struct{ source, handle string }
type bidiPageSession struct {
	client       *sessionrpc.Client
	connection   *bidiConnection
	subscription string
	id           string
	mu           sync.Mutex
	scripts      map[string]bidiRegistration
	injections   []string
	closed       bool
	done         chan struct{}
	finished     sync.Once
	targetClosed atomic.Bool
}

func (page *bidiPageSession) clearPreloads(ctx context.Context) error {
	for id, record := range page.scripts {
		if err := page.remove(ctx, record.handle); err != nil {
			return err
		}
		delete(page.scripts, id)
	}
	for len(page.injections) > 0 {
		if err := page.remove(ctx, page.injections[0]); err != nil {
			return err
		}
		page.injections = page.injections[1:]
	}
	return nil
}

func (page *bidiPageSession) unsubscribe(ctx context.Context) error {
	if page.subscription == "" {
		return nil
	}
	err := page.client.Call(ctx, "", "session.unsubscribe", map[string]any{"subscriptions": []string{page.subscription}}, nil)
	var rejected *sessionrpc.ProtocolError
	// A previous unsubscribe may have succeeded before its reply was lost.
	// This command contains exactly one known-owned ID and no event names.
	if errors.As(err, &rejected) && rejected.Kind == "invalid argument" {
		err = nil
	}
	if err == nil {
		page.subscription = ""
	}
	return err
}

func (page *bidiPageSession) Navigate(ctx context.Context, destination string) error {
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.closed {
		return errors.New("page session is closed")
	}
	if destination == "" {
		return errors.New("navigation requires a URL")
	}
	return page.client.Call(ctx, "", "browsingContext.navigate", map[string]any{"context": page.id, "url": destination, "wait": "complete"}, nil)
}
func (page *bidiPageSession) evaluate(ctx context.Context, source, contextID string) error {
	var result struct {
		Type string `json:"type"`
	}
	if err := page.client.Call(ctx, "", "script.evaluate", map[string]any{"expression": source, "target": map[string]string{"context": contextID}, "awaitPromise": true, "resultOwnership": "none"}, &result); err != nil {
		return err
	}
	if result.Type == "exception" {
		return errors.New("page script evaluation raised an exception")
	}
	return nil
}
func (page *bidiPageSession) preload(ctx context.Context, source string) (string, error) {
	var result struct {
		ID string `json:"script"`
	}
	if err := page.client.Call(ctx, "", "script.addPreloadScript", map[string]any{"functionDeclaration": "() => {" + source + "\n}", "contexts": []string{page.id}}, &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", errors.New("BiDi returned no preload script ID")
	}
	return result.ID, nil
}
func (page *bidiPageSession) remove(ctx context.Context, handle string) error {
	err := page.client.Call(ctx, "", "script.removePreloadScript", map[string]string{"script": handle}, nil)
	var rejected *sessionrpc.ProtocolError
	if errors.As(err, &rejected) && rejected.Kind == "no such script" {
		return nil
	}
	return err
}
func (page *bidiPageSession) Inject(ctx context.Context, source string, options management.InjectionOptions) error {
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.closed {
		return errors.New("page session is closed")
	}
	if source == "" || len(source) > 1<<20 {
		return errors.New("injection source must contain 1 byte to 1 MiB")
	}
	if options.World != "" && options.World != "page" && options.World != "main" {
		return errors.New("this BiDi route supports the page world only")
	}
	switch options.RunAt {
	case "", "now":
		return page.evaluate(ctx, source, page.id)
	case "document-start":
		handle, err := page.preload(ctx, source)
		if err == nil {
			page.injections = append(page.injections, handle)
		}
		return err
	default:
		return errors.New("runAt must be now or document-start")
	}
}
func (page *bidiPageSession) restore(ctx context.Context, source string) error {
	contexts, err := bidiContexts(ctx, page.client, page.id)
	if err != nil {
		return err
	}
	var walk func([]bidiContext) error
	walk = func(items []bidiContext) error {
		for _, item := range items {
			if err := page.evaluate(ctx, source, item.ID); err != nil {
				return fmt.Errorf("restore userscript in context %s: %w", item.ID, err)
			}
			if err := walk(item.Children); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(contexts)
}
func (page *bidiPageSession) ReplayUserscripts(ctx context.Context, records []management.UserscriptRegistration) (management.ReplayResult, error) {
	page.mu.Lock()
	defer page.mu.Unlock()
	result := management.ReplayResult{}
	if page.closed {
		return result, errors.New("page session is closed")
	}
	desired := map[string]string{}
	if len(records) > 512 {
		return result, errors.New("session supports at most 512 userscripts")
	}
	total := 0
	for _, record := range records {
		total += len(record.Source)
		if total > 8<<20 {
			return result, errors.New("userscript registration source exceeds 8 MiB")
		}
		if _, duplicate := desired[record.ID]; duplicate {
			return result, errors.New("repeated userscript registration ID")
		}
		source, err := userscript.ReplaySource(record.ID, record.Revision, record.Source, record.Matches, record.ExcludeMatches)
		if err != nil {
			return result, err
		}
		desired[record.ID] = source
	}
	for id, registered := range page.scripts {
		if desired[id] != registered.source {
			if err := page.remove(ctx, registered.handle); err != nil {
				return result, err
			}
			delete(page.scripts, id)
			result.Removed++
		}
	}
	for _, record := range records {
		if _, exists := page.scripts[record.ID]; exists {
			continue
		}
		source := desired[record.ID]
		handle, err := page.preload(ctx, source)
		if err != nil {
			return result, err
		}
		page.scripts[record.ID] = bidiRegistration{source: source, handle: handle}
		result.Registered++
		if err := page.restore(ctx, source); err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cleanupErr := page.remove(cleanup, handle)
			cancel()
			if cleanupErr == nil {
				delete(page.scripts, record.ID)
			}
			return result, errors.Join(err, cleanupErr)
		}
		result.Restored++
	}
	return result, nil
}
func (page *bidiPageSession) Close(ctx context.Context) error {
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.closed {
		return nil
	}
	page.closed = true
	defer page.finished.Do(func() { close(page.done) })
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var failures []error
	for id, record := range page.scripts {
		if err := page.remove(ctx, record.handle); err != nil {
			failures = append(failures, err)
		}
		delete(page.scripts, id)
	}
	for _, handle := range page.injections {
		if err := page.remove(ctx, handle); err != nil {
			failures = append(failures, err)
		}
	}
	failures = append(failures, page.unsubscribe(ctx))
	failures = append(failures, closeBiDi(ctx, page.connection))
	return errors.Join(failures...)
}

func (page *bidiPageSession) Done() <-chan struct{} { return page.done }
func (page *bidiPageSession) DropTransport() error  { return page.client.Close() }
func (page *bidiPageSession) Err() error {
	if page.targetClosed.Load() {
		return sessionrpc.ErrTargetClosed
	}
	if err := page.client.Err(); err != nil {
		return err
	}
	return sessionrpc.ErrTargetClosed
}
