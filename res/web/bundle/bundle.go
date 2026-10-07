// Package bundle runs web content through a browser the host chooses, and
// reports what the page did. It is the web engine: it serves a bundle read-only
// from loopback under a content security policy, gives the page a small
// window.ext API, and ends the run when the page exits, closes, or times out.
//
// The package depends only on the standard library. Showing the page is the
// host's job: Options.Open receives an address and opens it in whichever
// browser, simulator, emulator or device the host selected.
package bundle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Status says how a run ended.
type Status string

const (
	// StatusExited means the page called window.ext.exit(code).
	StatusExited Status = "exited"
	// StatusClosed means the page went away without exiting: the window or tab
	// was closed, or the page stopped reporting.
	StatusClosed Status = "closed"
	// StatusTimeout means Options.Timeout elapsed.
	StatusTimeout Status = "timeout"
	// StatusFailed means the page was never shown or never loaded.
	StatusFailed Status = "failed"
)

// Event is one line the page reported.
type Event struct {
	// Level is log, info, warn, error, debug or exception.
	Level string
	Text  string
}

// Options configures one run.
type Options struct {
	// Root is the directory to serve. Files are served read-only, and nothing
	// outside it, nothing behind a symbolic link that leaves it, and no dotfile.
	Root string
	// Entry is the page to show, relative to Root: an HTML file, or a .js or
	// .mjs file, for which a minimal page is generated. Defaults to index.html.
	Entry string
	// Policy limits what the page can reach. The zero value is the strict one.
	Policy Policy
	// CrossOriginIsolation makes the page cross-origin isolated, which browsers
	// require before they allow SharedArrayBuffer, and so before a page can run
	// WebAssembly threads or block a worker on a shared queue. It is off by
	// default because it also forbids loading cross-origin resources that do not
	// opt in, which suits a bundle served from one origin but not every page.
	CrossOriginIsolation bool
	// Open shows url in a browser. It is required and should return once the
	// page has been requested, not when it closes.
	Open func(ctx context.Context, url string) error
	// Console receives what the page logs, including uncaught errors. It is
	// called from the server's goroutines and must not block for long.
	Console func(Event)
	// Timeout ends the run after this long. Zero means no limit.
	Timeout time.Duration
	// StartupTimeout is how long to wait for the page to load and report in.
	// Zero means 30 seconds.
	StartupTimeout time.Duration
	// HeartbeatTimeout is how long a loaded page may stay silent before the run
	// is treated as closed. Zero means 15 seconds. Browsers slow the timers of
	// background tabs, so keep this well above a few seconds.
	HeartbeatTimeout time.Duration
}

// Result describes how a run ended.
type Result struct {
	Status Status
	// ExitCode is the code the page passed to window.ext.exit; zero otherwise.
	ExitCode int
	// Reason is a human-readable explanation for any status but exited.
	Reason string
	// URL is the address that was opened.
	URL string
}

const (
	defaultStartup   = 30 * time.Second
	defaultHeartbeat = 15 * time.Second
	maxEventBytes    = 64 << 10
	maxBodyBytes     = 256 << 10
	maxFileBytes     = 64 << 20
)

// Run serves the bundle, asks the host to open it, and waits for the page to
// exit, close, or time out. It returns an error only when the run could not
// start; how a started run ended is in the Result.
func Run(ctx context.Context, options Options) (Result, error) {
	if options.Open == nil {
		return Result{}, errors.New("bundle: Options.Open is required")
	}
	root, err := newConfinedRoot(options.Root)
	if err != nil {
		return Result{}, fmt.Errorf("bundle: %w", err)
	}
	entry := options.Entry
	if entry == "" {
		entry = "index.html"
	}
	if file, _, err := root.open(entry); err != nil {
		return Result{}, fmt.Errorf("bundle: entry %q: %w", entry, err)
	} else {
		file.Close()
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return Result{}, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Result{}, err
	}
	s := &session{
		root: root, entry: entry, policy: options.Policy.ContentSecurityPolicy(), isolated: options.CrossOriginIsolation,
		token: hex.EncodeToString(token), host: listener.Addr().String(), console: options.Console,
		finished: make(chan Result, 1),
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	go server.Serve(listener)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			server.Close()
		}
	}()

	url := "http://" + s.host + "/"
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	if err := options.Open(ctx, url); err != nil {
		return Result{Status: StatusFailed, Reason: "the page could not be opened: " + err.Error(), URL: url}, nil
	}
	startup, heartbeat := options.StartupTimeout, options.HeartbeatTimeout
	if startup <= 0 {
		startup = defaultStartup
	}
	if heartbeat <= 0 {
		heartbeat = defaultHeartbeat
	}
	started := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case result := <-s.finished:
			result.URL = url
			return result, nil
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return Result{Status: StatusTimeout, Reason: "the run reached its time limit", URL: url}, nil
			}
			return Result{URL: url}, ctx.Err()
		case <-ticker.C:
			seen, last := s.lastSeen()
			switch {
			case !seen && time.Since(started) > startup:
				return Result{Status: StatusFailed, Reason: "the page did not load within " + startup.String() +
					" (the browser may be waiting on a first-run, sign-in or permission screen)", URL: url}, nil
			case seen && time.Since(last) > heartbeat:
				return Result{Status: StatusClosed, Reason: "the page stopped reporting", URL: url}, nil
			}
		}
	}
}

// session is the http.Handler for one run.
type session struct {
	root     confinedRoot
	entry    string
	policy   string
	isolated bool
	token    string
	host     string
	console  func(Event)
	finished chan Result

	mu   sync.Mutex
	seen bool
	last time.Time
	done bool
}

func (s *session) lastSeen() (bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen, s.last
}

func (s *session) touch() {
	s.mu.Lock()
	s.seen, s.last = true, time.Now()
	s.mu.Unlock()
}

// finish records the first outcome and ignores any later one.
func (s *session) finish(result Result) {
	s.mu.Lock()
	first := !s.done
	s.done = true
	s.mu.Unlock()
	if first {
		s.finished <- result
	}
}

func (s *session) emit(level, text string) {
	if s.console == nil {
		return
	}
	if len(text) > maxEventBytes {
		text = text[:maxEventBytes] + "…"
	}
	s.console(Event{Level: level, Text: strings.ToValidUTF8(text, "�")})
}
