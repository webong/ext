package endpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

var now = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

const (
	secretToken = "tok-9f8e7d6c5b4a"
	secretPath  = "/devtools/browser/abcd-1234-secret"
)

func policy() Policy {
	return Policy{Schemes: []string{"ws", "tcp"}, Now: func() time.Time { return now }}
}

func good() Endpoint {
	return Endpoint{Scheme: "ws", Address: "127.0.0.1:4455", Path: secretPath, Token: secretToken, ExpiresAt: now.Add(time.Minute)}
}

type session struct{ done chan struct{} }

func (s session) Done() <-chan struct{} { return s.done }

func TestValidateAcceptsLoopbackForms(t *testing.T) {
	for _, address := range []string{"127.0.0.1:1", "localhost:65535", "[::1]:8080", "127.1.2.3:9"} {
		e := good()
		e.Address = address
		if err := e.Validate(policy()); err != nil {
			t.Fatalf("%s: %v", address, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	mutate := map[string]func(*Endpoint){
		"remote host":      func(e *Endpoint) { e.Address = "203.0.113.9:80" },
		"name":             func(e *Endpoint) { e.Address = "example.com:80" },
		"wildcard":         func(e *Endpoint) { e.Address = "0.0.0.0:80" },
		"zone":             func(e *Endpoint) { e.Address = "[fe80::1%eth0]:80" },
		"no port":          func(e *Endpoint) { e.Address = "127.0.0.1" },
		"port zero":        func(e *Endpoint) { e.Address = "127.0.0.1:0" },
		"port range":       func(e *Endpoint) { e.Address = "127.0.0.1:65536" },
		"port padded":      func(e *Endpoint) { e.Address = "127.0.0.1:080" },
		"credentials":      func(e *Endpoint) { e.Address = "user:pass@127.0.0.1:80" },
		"space":            func(e *Endpoint) { e.Address = "127.0.0.1: 80" },
		"scheme unknown":   func(e *Endpoint) { e.Scheme = "ftp" },
		"scheme not asked": func(e *Endpoint) { e.Scheme = "https" },
		"query":            func(e *Endpoint) { e.Path = "/a?b=" + secretToken },
		"fragment":         func(e *Endpoint) { e.Path = "/a#" + secretToken },
		"relative path":    func(e *Endpoint) { e.Path = "a/" + secretToken },
		"control in token": func(e *Endpoint) { e.Token = secretToken + "\n" },
		"long token":       func(e *Endpoint) { e.Token = strings.Repeat("a", MaxTokenBytes+1) },
		"long path":        func(e *Endpoint) { e.Path = "/" + strings.Repeat("a", MaxPathBytes) },
		"no expiry":        func(e *Endpoint) { e.ExpiresAt = time.Time{} },
		"expired":          func(e *Endpoint) { e.ExpiresAt = now.Add(-time.Second) },
		"too long lived":   func(e *Endpoint) { e.ExpiresAt = now.Add(48 * time.Hour) },
	}
	for name, change := range mutate {
		e := good()
		change(&e)
		err := e.Validate(policy())
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		if !errors.Is(err, plugin.ErrInvalid) && !errors.Is(err, plugin.ErrDenied) && !errors.Is(err, ErrExpired) {
			t.Fatalf("%s: unexpected error class %v", name, err)
		}
		for _, secret := range []string{secretToken, secretPath, e.Address, "example.com", "203.0.113.9"} {
			if secret != "" && strings.Contains(err.Error(), secret) {
				t.Fatalf("%s: error leaks %q: %v", name, secret, err)
			}
		}
	}
}

func TestEmptyPolicyAdmitsNothing(t *testing.T) {
	err := good().Validate(Policy{Now: policy().Now})
	if !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("got %v", err)
	}
}

func TestFormattingNeverShowsSecrets(t *testing.T) {
	e := good()
	g, err := Admit(session{make(chan struct{})}, e, policy())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{e, &e, g, struct{ E Endpoint }{e}, []Endpoint{e}, map[string]Endpoint{"k": e}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			out := fmt.Sprintf(verb, v)
			for _, secret := range []string{secretToken, secretPath, "127.0.0.1", "4455"} {
				if strings.Contains(out, secret) {
					t.Fatalf("%s of %T leaks %q: %s", verb, v, secret, out)
				}
			}
		}
	}
	data, err := json.Marshal(e)
	if err != nil || !strings.Contains(string(data), secretToken) {
		t.Fatalf("JSON must carry the real value: %s %v", data, err)
	}
}

func TestGrantEndsWithSession(t *testing.T) {
	s := session{make(chan struct{})}
	g, err := Admit(s, good(), policy())
	if err != nil {
		t.Fatal(err)
	}
	if token, err := g.Token(); err != nil || token != secretToken {
		t.Fatalf("token %q %v", token, err)
	}
	close(s.done)
	if g.Active() {
		t.Fatal("grant outlived its session")
	}
	if _, err := g.Token(); !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v", err)
	}
	if _, _, _, err := g.Target(); !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v", err)
	}
	if _, err := Admit(s, good(), policy()); !errors.Is(err, ErrExpired) {
		t.Fatalf("a closed session admits nothing, got %v", err)
	}
}

func TestTokenReusableUnlessOneTime(t *testing.T) {
	s := session{make(chan struct{})}
	g, _ := Admit(s, good(), policy())
	for i := 0; i < 3; i++ {
		if token, err := g.Token(); err != nil || token != secretToken {
			t.Fatalf("read %d: %q %v", i, token, err)
		}
	}
	e := good()
	e.OneTime = true
	once, _ := Admit(s, e, policy())
	if _, err := once.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := once.Token(); !errors.Is(err, ErrUsed) {
		t.Fatalf("second read: %v", err)
	}
}

func TestRevokeAndRotate(t *testing.T) {
	s := session{make(chan struct{})}
	old, _ := Admit(s, good(), policy())
	next := good()
	next.Token = "tok-new-0000"
	fresh, err := Admit(s, next, policy())
	if err != nil {
		t.Fatal(err)
	}
	old.Revoke()
	old.Revoke()
	if _, err := old.Token(); !errors.Is(err, ErrExpired) {
		t.Fatalf("revoked grant: %v", err)
	}
	if token, err := fresh.Token(); err != nil || token != "tok-new-0000" {
		t.Fatalf("rotated grant: %q %v", token, err)
	}
}

func TestGrantExpiresWithTime(t *testing.T) {
	clock := now
	p := Policy{Schemes: []string{"ws"}, Now: func() time.Time { return clock }}
	g, err := Admit(session{make(chan struct{})}, good(), p)
	if err != nil {
		t.Fatal(err)
	}
	clock = now.Add(2 * time.Minute)
	if _, err := g.Token(); !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v", err)
	}
}

func TestAdmitNeedsSession(t *testing.T) {
	if _, err := Admit(nil, good(), policy()); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeReadsAnEndpointStrictly(t *testing.T) {
	good := `{"scheme":"ws","address":"127.0.0.1:4455","path":"` + secretPath + `","token":"` + secretToken + `","oneTime":true,"expiresAt":"2026-01-02T03:05:05Z"}`
	e, err := Decode(json.RawMessage(good))
	if err != nil {
		t.Fatal(err)
	}
	if e.Scheme != "ws" || e.Address != "127.0.0.1:4455" || e.Token != secretToken || !e.OneTime || e.ExpiresAt.IsZero() {
		t.Fatalf("decoded %+v", e)
	}
	bad := map[string]string{
		"unknown field":    `{"scheme":"ws","address":"127.0.0.1:1","expiresAt":"2026-01-02T03:05:05Z","tokn":"` + secretToken + `"}`,
		"duplicate key":    `{"scheme":"ws","scheme":"tcp","address":"127.0.0.1:1","expiresAt":"2026-01-02T03:05:05Z"}`,
		"remote address":   `{"scheme":"ws","address":"203.0.113.9:80","expiresAt":"2026-01-02T03:05:05Z"}`,
		"unknown scheme":   `{"scheme":"ftp","address":"127.0.0.1:1","expiresAt":"2026-01-02T03:05:05Z"}`,
		"no expiry":        `{"scheme":"ws","address":"127.0.0.1:1"}`,
		"query in path":    `{"scheme":"ws","address":"127.0.0.1:1","path":"/a?x=` + secretToken + `","expiresAt":"2026-01-02T03:05:05Z"}`,
		"not an object":    `"` + secretToken + `"`,
		"bad time":         `{"scheme":"ws","address":"127.0.0.1:1","token":"` + secretToken + `","expiresAt":"soon"}`,
		"control in token": `{"scheme":"ws","address":"127.0.0.1:1","token":"a\u0000` + secretToken + `","expiresAt":"2026-01-02T03:05:05Z"}`,
	}
	for name, raw := range bad {
		_, err := Decode(json.RawMessage(raw))
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		if !errors.Is(err, plugin.ErrInvalid) && !errors.Is(err, plugin.ErrDenied) {
			t.Fatalf("%s: unexpected class %v", name, err)
		}
		for _, secret := range []string{secretToken, secretPath, "203.0.113.9"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("%s: error leaks %q: %v", name, secret, err)
			}
		}
	}
}

// Check is the part of Validate no host policy changes.
func TestCheckIsPolicyIndependent(t *testing.T) {
	if err := good().Check(); err != nil {
		t.Fatal(err)
	}
	e := good()
	e.Scheme = "https" // not in policy(), but a supported scheme
	if err := e.Check(); err != nil {
		t.Fatalf("Check must not apply a policy: %v", err)
	}
	if err := e.Validate(policy()); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("Validate applies the policy: %v", err)
	}
	e = good()
	e.ExpiresAt = now.Add(-time.Hour) // expiry versus now is policy-time, not shape
	if err := e.Check(); err != nil {
		t.Fatalf("Check must not judge the clock: %v", err)
	}
}
