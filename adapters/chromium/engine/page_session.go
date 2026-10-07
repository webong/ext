package chromium

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webong/ext/adapters/chromium/engine/sessionrpc"
	management "github.com/webong/ext/res/browser/contract"
	"github.com/webong/ext/res/browser/userscript"
)

// NewPageSessionRuntime creates a CDP attachment backend. An explicit endpoint
// belongs to the caller; otherwise discovery uses the selected user data root.
// A debugging marker does not prove isolation between profiles in one process.
func NewPageSessionRuntime(config Config, endpoint string) management.PageSessionRuntime {
	return &cdpPageRuntime{config: config, endpoint: endpoint}
}

type cdpPageRuntime struct {
	config   Config
	endpoint string
}

// ActiveEndpoint reads the Chromium-owned remote debugging marker.
func ActiveEndpoint(profilePath string) (string, error) {
	if !filepath.IsAbs(profilePath) {
		return "", errors.New("endpoint discovery requires an absolute user data directory")
	}
	file, err := os.Open(filepath.Join(profilePath, "DevToolsActivePort"))
	if err != nil {
		return "", fmt.Errorf("no active CDP endpoint; supply the browser provider's endpoint: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(data) > 4096 || len(lines) != 2 || !strings.HasPrefix(strings.TrimSpace(lines[1]), "/devtools/browser/") {
		return "", errors.New("invalid debugging marker")
	}
	port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("invalid debugging port")
	}
	return fmt.Sprintf("ws://127.0.0.1:%d%s", port, strings.TrimSpace(lines[1])), nil
}

func cdpWebSocket(ctx context.Context, endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("CDP endpoint requires an HTTP or WebSocket URL without credentials")
	}
	if parsed.Scheme == "ws" || parsed.Scheme == "wss" {
		return endpoint, nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("unsupported CDP endpoint scheme")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/json/version"
	parsed.RawQuery, parsed.Fragment = "", ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	client := http.Client{Timeout: 15 * time.Second}
	reply, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CDP discovery returned HTTP %d", reply.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(reply.Body, 1<<20+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", errors.New("CDP discovery response is too large")
	}
	var result struct {
		WebSocket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	socket, err := url.Parse(result.WebSocket)
	if err != nil || socket.Host == "" || socket.User != nil || (socket.Scheme != "ws" && socket.Scheme != "wss") {
		return "", errors.New("CDP discovery returned no valid browser WebSocket")
	}
	return result.WebSocket, nil
}

func (runtime *cdpPageRuntime) endpointFor(profile string) (string, error) {
	if runtime.endpoint != "" {
		return runtime.endpoint, nil
	}
	root := profile
	if !filepath.IsAbs(root) {
		var err error
		root, err = extensionProfileRoot(runtime.config)
		if err != nil {
			return "", err
		}
		if profile == "" || profile == "." || profile == ".." || filepath.Base(profile) != profile {
			return "", errors.New("select a browser profile or absolute user data directory")
		}
		info, err := os.Stat(filepath.Join(root, profile))
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", errors.New("selected profile is not a directory")
		}
	}
	return ActiveEndpoint(root)
}

func (runtime *cdpPageRuntime) DiscoverTargets(ctx context.Context, profile string) ([]management.SessionTarget, error) {
	endpoint, err := runtime.endpointFor(profile)
	if err != nil {
		return nil, err
	}
	endpoint, err = cdpWebSocket(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	client, err := sessionrpc.Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	var result struct {
		Targets []struct {
			ID    string `json:"targetId"`
			Type  string `json:"type"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"targetInfos"`
	}
	if err := client.Call(ctx, "", "Target.getTargets", map[string]any{}, &result); err != nil {
		return nil, err
	}
	targets := make([]management.SessionTarget, 0, len(result.Targets))
	for _, target := range result.Targets {
		if target.Type == "page" {
			targets = append(targets, management.SessionTarget{ID: target.ID, Protocol: "cdp", Endpoint: endpoint, URL: target.URL, Title: target.Title})
		}
	}
	return targets, nil
}

func (runtime *cdpPageRuntime) Connect(ctx context.Context, profile string, target management.SessionTarget) (management.PageSession, error) {
	page, err := runtime.connectPage(ctx, target)
	if err != nil {
		return nil, err
	}
	return sessionrpc.WithReconnect(ctx, page, func(recovery context.Context, previous management.PageSession) (management.PageSession, error) {
		// CDP preload registrations belong to the old attachment. Disconnecting
		// it releases them; a fresh attachment gets its own preload identifiers.
		previous.(*cdpPageSession).client.Close()
		candidate, err := runtime.connectPage(recovery, target)
		if candidate == nil {
			return nil, err
		}
		return candidate, err
	}), nil
}

func (runtime *cdpPageRuntime) connectPage(ctx context.Context, target management.SessionTarget) (*cdpPageSession, error) {
	if target.Protocol != "cdp" || target.ID == "" || target.Endpoint == "" {
		return nil, errors.New("CDP attachment requires a caller-selected page ID and endpoint")
	}
	endpoint, err := cdpWebSocket(ctx, target.Endpoint)
	if err != nil {
		return nil, err
	}
	client, err := sessionrpc.Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var info struct {
		Target struct {
			Type string `json:"type"`
		} `json:"targetInfo"`
	}
	if err := client.Call(ctx, "", "Target.getTargetInfo", map[string]string{"targetId": target.ID}, &info); err != nil {
		client.Close()
		var rejected *sessionrpc.ProtocolError
		if errors.As(err, &rejected) {
			return nil, sessionrpc.Permanent(err)
		}
		return nil, err
	}
	if info.Target.Type != "page" {
		client.Close()
		return nil, sessionrpc.Permanent(errors.New("selected CDP target is not a page"))
	}
	var attached struct {
		ID string `json:"sessionId"`
	}
	if err := client.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &attached); err != nil {
		client.Close()
		var rejected *sessionrpc.ProtocolError
		if errors.As(err, &rejected) {
			return nil, sessionrpc.Permanent(err)
		}
		return nil, err
	}
	if attached.ID == "" {
		client.Close()
		return nil, errors.New("CDP returned no attachment session")
	}
	page := &cdpPageSession{client: client, id: attached.ID, scripts: map[string]cdpRegistration{}, done: make(chan struct{})}
	client.OnEvent(func(event sessionrpc.Event) {
		if event.Method == "Inspector.detached" && event.SessionID == page.id {
			page.targetClosed.Store(true)
			page.finished.Do(func() { close(page.done) })
		}
		if event.Method == "Target.detachedFromTarget" {
			var detached struct {
				ID string `json:"sessionId"`
			}
			if json.Unmarshal(event.Params, &detached) == nil && detached.ID == page.id {
				page.targetClosed.Store(true)
				page.finished.Do(func() { close(page.done) })
			}
		}
	})
	go func() { <-client.Done(); page.finished.Do(func() { close(page.done) }) }()
	if err := client.Call(ctx, page.id, "Page.enable", map[string]any{}, nil); err != nil {
		page.Close(ctx)
		return nil, err
	}
	return page, nil
}

type cdpRegistration struct{ source, handle string }
type cdpPageSession struct {
	client       *sessionrpc.Client
	id           string
	mu           sync.Mutex
	scripts      map[string]cdpRegistration
	injections   []string
	closed       bool
	done         chan struct{}
	finished     sync.Once
	targetClosed atomic.Bool
}

func (page *cdpPageSession) Navigate(ctx context.Context, destination string) error {
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.closed {
		return errors.New("page session is closed")
	}
	if destination == "" {
		return errors.New("navigation requires a URL")
	}
	var result struct {
		Error string `json:"errorText"`
	}
	if err := page.client.Call(ctx, page.id, "Page.navigate", map[string]string{"url": destination}, &result); err != nil {
		return err
	}
	if result.Error != "" {
		return fmt.Errorf("page navigation: %s", result.Error)
	}
	return nil
}

func (page *cdpPageSession) evaluate(ctx context.Context, source string) error {
	var result struct {
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	if err := page.client.Call(ctx, page.id, "Runtime.evaluate", map[string]any{"expression": source, "awaitPromise": true, "returnByValue": false}, &result); err != nil {
		return err
	}
	if len(result.Exception) > 0 && string(result.Exception) != "null" {
		return errors.New("page script evaluation raised an exception")
	}
	return nil
}

func (page *cdpPageSession) preload(ctx context.Context, source string) (string, error) {
	var result struct {
		ID string `json:"identifier"`
	}
	if err := page.client.Call(ctx, page.id, "Page.addScriptToEvaluateOnNewDocument", map[string]string{"source": source}, &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", errors.New("CDP returned no preload identifier")
	}
	return result.ID, nil
}
func (page *cdpPageSession) remove(ctx context.Context, handle string) error {
	return page.client.Call(ctx, page.id, "Page.removeScriptToEvaluateOnNewDocument", map[string]string{"identifier": handle}, nil)
}

func (page *cdpPageSession) Inject(ctx context.Context, source string, options management.InjectionOptions) error {
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.closed {
		return errors.New("page session is closed")
	}
	if source == "" || len(source) > 1<<20 {
		return errors.New("injection source must contain 1 byte to 1 MiB")
	}
	if options.World != "" && options.World != "page" && options.World != "main" {
		return errors.New("this CDP route supports the page world only")
	}
	switch options.RunAt {
	case "", "now":
		return page.evaluate(ctx, source)
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

func (page *cdpPageSession) ReplayUserscripts(ctx context.Context, records []management.UserscriptRegistration) (management.ReplayResult, error) {
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
		source := desired[record.ID]
		if _, exists := page.scripts[record.ID]; exists {
			continue
		}
		handle, err := page.preload(ctx, source)
		if err != nil {
			return result, err
		}
		page.scripts[record.ID] = cdpRegistration{source: source, handle: handle}
		result.Registered++
		if err := page.evaluate(ctx, source); err != nil {
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

func (page *cdpPageSession) Close(ctx context.Context) error {
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
	if err := page.client.Call(ctx, "", "Target.detachFromTarget", map[string]string{"sessionId": page.id}, nil); err != nil {
		failures = append(failures, err)
	}
	if err := page.client.Close(); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (page *cdpPageSession) Done() <-chan struct{} { return page.done }
func (page *cdpPageSession) DropTransport() error  { return page.client.Close() }
func (page *cdpPageSession) Err() error {
	if page.targetClosed.Load() {
		return sessionrpc.ErrTargetClosed
	}
	if err := page.client.Err(); err != nil {
		return err
	}
	return sessionrpc.ErrTargetClosed
}
