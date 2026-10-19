// Package endpoint is the standard form of a loopback data endpoint that a
// plugin result declares and a host admits or refuses. JSON lines cannot carry
// continuous data such as media or capture frames, so a plugin points at a local
// listener instead; this package validates the pointer, ties its lifetime to the
// plugin session and keeps its secret parts out of errors and logs.
//
// An endpoint value is a credential: whoever can read it and reach the port can
// use the listener. Every error, String and %v/%+v/%#v rendering from this
// package omits the address, path and token. JSON encoding carries the real
// values because the protocol needs them.
//
// Presenting the token. A client presents Endpoint.Token to the listener the
// same way for every consumer: for ws, wss, http and https, as the header
// "Authorization: Bearer <token>" on the handshake or request; for tcp, as the
// first line of the stream, the token followed by one "\n", before any other
// data. A listener that gets no token, or a wrong one, closes the connection.
// When Token is empty the listener needs none. The token is never put in a URL.
//
// Rotation. A grant is bounded by its session and by ExpiresAt. For a listener
// that outlives one expiry the plugin announces a replacement endpoint and the
// host Admits it and then Revokes the old grant. Announcing is the consumer's
// contract (a reverse operation, conventionally named "endpoint.rotate", whose
// payload is {"endpoint": <Endpoint>}); the library only validates and admits.
package endpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// Bounds on a declared endpoint.
const (
	MaxAddressBytes = 255
	MaxPathBytes    = 1024
	MaxTokenBytes   = 512
	// DefaultMaxLifetime applies when a Policy sets none.
	DefaultMaxLifetime = time.Hour
)

var (
	// ErrUsed is returned when a one-time token is read twice.
	ErrUsed = errors.New("plugin endpoint token already used")
	// ErrExpired is returned for a grant that was revoked, whose session closed
	// or whose time ran out.
	ErrExpired = errors.New("plugin endpoint expired")
)

// Endpoint is a loopback listener declared by a plugin.
type Endpoint struct {
	// Scheme is one of ws, wss, http, https or tcp, and must be allowed by the
	// host's Policy.
	Scheme string `json:"scheme"`
	// Address is a loopback "host:port": "localhost", a loopback IP literal, or
	// a bracketed IPv6 loopback literal.
	Address string `json:"address"`
	// Path is optional and begins with "/". It may hold a secret identifier.
	Path string `json:"path,omitempty"`
	// Token is an optional secret the host presents to the listener. It is
	// reusable while the grant is active unless OneTime is set.
	Token string `json:"token,omitempty"`
	// OneTime makes the token single-use: the first Grant.Token succeeds and
	// later calls return ErrUsed.
	OneTime bool `json:"oneTime,omitempty"`
	// ExpiresAt bounds the endpoint's life. It is required.
	ExpiresAt time.Time `json:"expiresAt"`
}

// Policy is the host's decision about what it will admit.
type Policy struct {
	// Schemes lists the admitted schemes. It is required: an empty list admits
	// nothing.
	Schemes []string
	// MaxLifetime bounds ExpiresAt from now; zero uses DefaultMaxLifetime.
	MaxLifetime time.Duration
	// Now overrides the clock for tests.
	Now func() time.Time
}

func (p Policy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

var knownSchemes = map[string]bool{"ws": true, "wss": true, "http": true, "https": true, "tcp": true}

func invalid(reason string) error { return fmt.Errorf("%w: endpoint %s", plugin.ErrInvalid, reason) }

// Key is the conventional JSON key under which a result carries an Endpoint:
// {"endpoint": {...}}. A contract may nest it elsewhere; Decode reads the object.
const Key = "endpoint"

// Decode reads one Endpoint from JSON, rejecting unknown fields, duplicate keys
// and anything Check refuses. It applies no host policy: call Admit (or
// Validate) for that. An error never includes a value from the input.
func Decode(raw json.RawMessage) (Endpoint, error) {
	var e Endpoint
	if err := plugin.Decode(raw, &e); err != nil {
		return Endpoint{}, invalid("is malformed")
	}
	if err := e.Check(); err != nil {
		return Endpoint{}, err
	}
	return e, nil
}

// Check validates the endpoint's shape, which no policy changes: a supported
// scheme, a loopback host with a port, a well-formed path and token, and an
// expiry. Its errors never include a value from the endpoint.
func (e Endpoint) Check() error {
	if !knownSchemes[e.Scheme] {
		return invalid("scheme is not supported")
	}
	if err := validAddress(e.Address); err != nil {
		return err
	}
	if len(e.Path) > MaxPathBytes {
		return invalid("path is too long")
	}
	if e.Path != "" {
		if e.Path[0] != '/' || strings.ContainsAny(e.Path, "?#\\") || hasControlOrSpace(e.Path) {
			return invalid("path is malformed")
		}
	}
	if len(e.Token) > MaxTokenBytes || hasControlOrSpace(e.Token) {
		return invalid("token is malformed")
	}
	if e.ExpiresAt.IsZero() {
		return invalid("needs an expiry")
	}
	return nil
}

// Validate checks the endpoint against the policy: Check, then the admitted
// schemes and the lifetime. Its errors never include a value from the endpoint.
func (e Endpoint) Validate(p Policy) error {
	if len(p.Schemes) == 0 {
		return fmt.Errorf("%w: no endpoint scheme is admitted", plugin.ErrDenied)
	}
	allowed := false
	for _, s := range p.Schemes {
		if !knownSchemes[s] {
			return invalid("policy names an unknown scheme")
		}
		if s == e.Scheme {
			allowed = true
		}
	}
	if err := e.Check(); err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: endpoint scheme is not admitted", plugin.ErrDenied)
	}
	max := p.MaxLifetime
	if max <= 0 {
		max = DefaultMaxLifetime
	}
	now := p.now()
	if !e.ExpiresAt.After(now) {
		return fmt.Errorf("%w", ErrExpired)
	}
	if e.ExpiresAt.After(now.Add(max)) {
		return invalid("outlives the admitted lifetime")
	}
	return nil
}

func validAddress(address string) error {
	if address == "" || len(address) > MaxAddressBytes || hasControlOrSpace(address) {
		return invalid("address is malformed")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return invalid("address needs a host and a port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return invalid("port is malformed")
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || strings.Contains(host, "%") || !ip.IsLoopback() {
		return fmt.Errorf("%w: endpoint is not on a loopback address", plugin.ErrDenied)
	}
	return nil
}

func hasControlOrSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// String and the formatting methods print only the scheme.
func (e Endpoint) String() string   { return "endpoint(" + redactedScheme(e.Scheme) + ")" }
func (e Endpoint) GoString() string { return e.String() }
func (e Endpoint) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(e.String()))
}

func redactedScheme(s string) string {
	if knownSchemes[s] {
		return s
	}
	return "invalid"
}

// Lifetime is anything that ends: a plugin session, normally.
type Lifetime interface{ Done() <-chan struct{} }

// Grant is an admitted endpoint. It stays usable until the session ends or the
// endpoint expires, whichever is first.
type Grant struct {
	mu      sync.Mutex
	ep      Endpoint
	session Lifetime
	now     func() time.Time
	used    bool
	revoked bool
}

// Admit validates the endpoint and returns a Grant tied to the session. A host
// that wants to refuse an endpoint returns an error before calling Admit.
func Admit(session Lifetime, e Endpoint, p Policy) (*Grant, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: endpoint needs a session", plugin.ErrInvalid)
	}
	if err := e.Validate(p); err != nil {
		return nil, err
	}
	select {
	case <-session.Done():
		return nil, ErrExpired
	default:
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	return &Grant{ep: e, session: session, now: now}, nil
}

// Revoke ends the grant now; it is how a host retires an endpoint it replaced.
// It is safe to call more than once.
func (g *Grant) Revoke() {
	g.mu.Lock()
	g.revoked = true
	g.mu.Unlock()
}

// Active reports whether the grant can still be used.
func (g *Grant) Active() bool {
	g.mu.Lock()
	revoked := g.revoked
	g.mu.Unlock()
	if revoked {
		return false
	}
	select {
	case <-g.session.Done():
		return false
	default:
	}
	return g.now().Before(g.ep.ExpiresAt)
}

// Scheme returns the admitted scheme.
func (g *Grant) Scheme() string { return g.ep.Scheme }

// Target returns the scheme, address and path without the token, or ErrExpired.
// The result is secret: do not log it.
func (g *Grant) Target() (scheme, address, path string, err error) {
	if !g.Active() {
		return "", "", "", ErrExpired
	}
	return g.ep.Scheme, g.ep.Address, g.ep.Path, nil
}

// Token returns the token, or an empty string when the endpoint has none. It
// can be called any number of times while the grant is active, so a host may
// give it to successive clients; a one-time endpoint allows one call and then
// returns ErrUsed. An ended grant returns ErrExpired.
func (g *Grant) Token() (string, error) {
	if !g.Active() {
		return "", ErrExpired
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ep.OneTime {
		if g.used {
			return "", ErrUsed
		}
		g.used = true
	}
	return g.ep.Token, nil
}

func (g *Grant) String() string   { return "endpoint grant(" + redactedScheme(g.ep.Scheme) + ")" }
func (g *Grant) GoString() string { return g.String() }
func (g *Grant) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(g.String()))
}
