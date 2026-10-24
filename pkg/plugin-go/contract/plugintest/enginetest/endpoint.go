package enginetest

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/endpoint"
)

// The endpoint rules are judged by the C engine and by the Go package alike. The
// fixture in lib/plugin/testdata/v1 is the shared statement of them: every binding
// reproduces its outcomes. The tests below hold the fixture, the Go package and the
// engine against each other, and then go beyond what a fixture can list.

// outcome names how an endpoint check ended, in the vocabulary of the fixture.
func endpointOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, endpoint.ErrExpired):
		return "expired"
	case errors.Is(err, plugin.ErrDenied):
		return "denied"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	}
	return "other: " + err.Error()
}

type endpointPolicy struct {
	Schemes                 []string `json:"schemes"`
	MaxLifetimeMilliseconds int64    `json:"maxLifetimeMilliseconds"`
	Now                     string   `json:"now"`
}

// goPolicy reads a policy object as the service does; ok is false for one that is
// not well formed, which the engine refuses as invalid input.
func goPolicy(raw json.RawMessage) (endpoint.Policy, bool) {
	var p endpointPolicy
	if err := plugin.Decode(raw, &p); err != nil {
		return endpoint.Policy{}, false
	}
	policy := endpoint.Policy{Schemes: p.Schemes, MaxLifetime: time.Duration(p.MaxLifetimeMilliseconds) * time.Millisecond}
	if p.Now != "" {
		now, err := time.Parse(time.RFC3339Nano, p.Now)
		if err != nil {
			return endpoint.Policy{}, false
		}
		policy.Now = func() time.Time { return now }
	}
	return policy, true
}

// goCheck is endpoint.Decode: the shape rules alone.
func goCheck(raw json.RawMessage) string {
	_, err := endpoint.Decode(raw)
	return endpointOutcome(err)
}

// goValidate decodes the endpoint strictly and validates it against the policy,
// the order in which a host admits one.
func goValidate(raw, policy json.RawMessage) string {
	var e endpoint.Endpoint
	if err := plugin.Decode(raw, &e); err != nil {
		return "invalid"
	}
	p, ok := goPolicy(policy)
	if !ok {
		return "invalid"
	}
	return endpointOutcome(e.Validate(p))
}

func (e Engine) engineCheck(raw json.RawMessage) string {
	_, err := e.Call("endpoint.check", raw)
	return endpointOutcome(err)
}

func (e Engine) engineValidate(raw, policy json.RawMessage) string {
	input, _ := json.Marshal(map[string]json.RawMessage{"endpoint": raw, "policy": policy})
	_, err := e.Call("endpoint.validate", input)
	return endpointOutcome(err)
}

type endpointCase struct {
	Name     string          `json:"name"`
	Endpoint json.RawMessage `json:"endpoint"`
	Policy   json.RawMessage `json:"policy"`
	Check    string          `json:"check"`
	Validate string          `json:"validate"`
}

func (e Engine) endpointFixture(t *testing.T) []endpointCase {
	t.Helper()
	data, err := e.fixture(t, "v1/endpoint-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []endpointCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	return fixture.Cases
}

// Both implementations reproduce every outcome the fixture states.
func EndpointFixture(t *testing.T, e Engine) {
	cases := e.endpointFixture(t)
	counts := map[string]int{}
	for _, c := range cases {
		counts[c.Validate]++
		if got := goCheck(c.Endpoint); got != c.Check {
			t.Errorf("%s: Go check %s, fixture %s", c.Name, got, c.Check)
		}
		if got := goValidate(c.Endpoint, c.Policy); got != c.Validate {
			t.Errorf("%s: Go validate %s, fixture %s", c.Name, got, c.Validate)
		}
		if got := e.engineCheck(c.Endpoint); got != c.Check {
			t.Errorf("%s: engine check %s, fixture %s", c.Name, got, c.Check)
		}
		if got := e.engineValidate(c.Endpoint, c.Policy); got != c.Validate {
			t.Errorf("%s: engine validate %s, fixture %s", c.Name, got, c.Validate)
		}
	}
	t.Logf("%d cases: %v", len(cases), counts)
	for _, outcome := range []string{"ok", "invalid", "denied", "expired"} {
		if counts[outcome] < 5 {
			t.Errorf("the fixture states too few %q cases: %d", outcome, counts[outcome])
		}
	}
}

// One-field mutations of an endpoint and of a policy are judged the same.
func EndpointMutationDifferential(t *testing.T, e Engine) {
	endpointBase := map[string]any{"scheme": "ws", "address": "127.0.0.1:8080", "path": "/stream/abc", "token": "t0k3n", "oneTime": true, "expiresAt": "2030-01-01T00:30:00Z"}
	policyBase := map[string]any{"schemes": []any{"ws", "tcp"}, "maxLifetimeMilliseconds": 3600000, "now": "2030-01-01T00:00:00Z"}
	policyRaw := mustMarshal(t, policyBase)
	endpointRaw := mustMarshal(t, endpointBase)
	var accepted, total, diverged int
	compare := func(label string, raw, policy json.RawMessage, check bool) {
		total++
		want, got := goValidate(raw, policy), e.engineValidate(raw, policy)
		if check {
			want, got = goCheck(raw), e.engineCheck(raw)
		}
		if want == "ok" {
			accepted++
		}
		if want != got {
			diverged++
			if diverged <= 8 {
				t.Errorf("%s %s %s: Go %s, engine %s", label, raw, policy, want, got)
			}
		}
	}
	for _, raw := range mutations(endpointBase) {
		compare("endpoint", raw, policyRaw, false)
		compare("check", raw, policyRaw, true)
	}
	for _, raw := range mutations(policyBase) {
		compare("policy", endpointRaw, raw, false)
	}
	t.Logf("%d mutations (%d accepted), %d divergences", total, accepted, diverged)
	if accepted == 0 || accepted == total {
		t.Fatalf("mutations are vacuous: accepted %d of %d", accepted, total)
	}
}

// The loopback test rests on net.SplitHostPort and net.ParseIP; the engine carries
// its own copies of their rules, so address text of every shape is compared.
func EndpointAddressDifferential(t *testing.T, e Engine) {
	base := map[string]any{"scheme": "tcp", "address": "", "expiresAt": "2030-01-01T00:30:00Z"}
	check := func(address string) (string, string) {
		base["address"] = address
		raw := mustMarshal(t, base)
		return goCheck(raw), e.engineCheck(raw)
	}
	var hosts []string
	// Structured forms: every way to write the two loopback hosts and their neighbors.
	for _, h := range []string{
		"::1", "0:0:0:0:0:0:0:1", "0000:0000:0000:0000:0000:0000:0000:0001", "::0001", "0::1", "0:0::1", "::ffff:127.0.0.1", "::FFFF:127.0.0.1",
		"::ffff:7f00:1", "::ffff:7F00:0001", "0:0:0:0:0:ffff:127.0.0.1", "::ffff:127.255.255.255", "::ffff:128.0.0.1", "::ffff:0.0.0.0", "::127.0.0.1",
		"::", "::2", "1::", "1::1", "127.0.0.1", "127.0.0.0", "127.255.255.255", "128.0.0.0", "126.255.255.255", "0.0.0.0", "255.255.255.255",
		"127.0.0.1.", ".127.0.0.1", "127..0.1", "127.0.0", "127.0.0.1.1", "127.0.0.01", "127.0.0.001", "127.00.0.1", "127.0.0.256", "127.0.0.-1", "127.0.0.+1",
		"1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7::", "1:2:3:4:5:6:7:8:9", "1:2:3:4:5:6::8", "::1:2:3:4:5:6:7", "::1:2:3:4:5:6:7:8", "1::2::3", ":::1", "::1::",
		"12345::1", "::12345", "g::1", "::1g", "::1%lo", "::1%", "fe80::1%eth0", "%", "::ffff:1.2.3", "::ffff:1.2.3.4.5", "1.2.3.4::", "::1.2.3.4:5",
		"1:2:3:4:5:6:1.2.3.4", "1:2:3:4:5:6:7:1.2.3.4", "1::1.2.3.4", "::0.0.0.1", "::ffff:00.0.0.1", "localhost", "LOCALHOST", "localhost.", "local", "",
		"[::1]", "::1]", "[::1", "[[::1]]", "a:b", ":", "::::", "0x7f.0.0.1", "2130706433", "0177.0.0.1", "127.1", "127.0.1",
	} {
		hosts = append(hosts, h)
	}
	var addresses []string
	for _, h := range hosts {
		for _, wrap := range []string{"%s:80", "[%s]:80", "[%s]", "%s", "[%s]:", "[%s]:0", "[%s]:65535", "[%s]:65536", "[%s]:080", "[%s]:80:80", "[%s]80"} {
			addresses = append(addresses, fmt.Sprintf(wrap, h))
		}
	}
	// Generated text over the alphabet these rules care about.
	random := rand.New(rand.NewSource(1))
	alphabet := []string{"0", "1", "7", "f", "F", "a", "g", ":", ":", ".", ".", "[", "]", "%", "x", "localhost", "::1", "127", "ffff"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := random.Intn(14) + 1; n > 0; n-- {
			b.WriteString(alphabet[random.Intn(len(alphabet))])
		}
		addresses = append(addresses, b.String(), b.String()+":80", "["+b.String()+"]:80")
	}
	accepted, denied, diverged := 0, 0, 0
	for _, a := range addresses {
		want, got := check(a)
		switch want {
		case "ok":
			accepted++
		case "denied":
			denied++
		}
		if want != got {
			diverged++
			if diverged <= 12 {
				t.Errorf("address %q: Go %s, engine %s", a, want, got)
			}
		}
	}
	t.Logf("%d addresses (%d loopback, %d not), %d divergences", len(addresses), accepted, denied, diverged)
	if accepted < 100 || denied < 100 {
		t.Fatalf("the addresses do not exercise both outcomes: %d loopback, %d not", accepted, denied)
	}
}

// Expiry is compared to the nanosecond, in any zone, at both edges of the allowed
// lifetime.
func EndpointTimeDifferential(t *testing.T, e Engine) {
	now := time.Date(2030, 6, 15, 12, 0, 0, 123456789, time.UTC)
	lifetime := 90 * time.Second
	policy := mustMarshal(t, map[string]any{"schemes": []string{"ws"}, "maxLifetimeMilliseconds": lifetime.Milliseconds(), "now": now.Format(time.RFC3339Nano)})
	zones := []*time.Location{time.UTC, time.FixedZone("p", 5*3600+1800), time.FixedZone("m", -7*3600), time.FixedZone("z", 0), time.FixedZone("e", 14*3600)}
	var accepted, total, diverged int
	for _, anchor := range []time.Duration{0, lifetime} {
		for _, delta := range []time.Duration{-time.Hour, -time.Second, -time.Millisecond, -2 * time.Nanosecond, -time.Nanosecond, 0, time.Nanosecond, 2 * time.Nanosecond, 999999 * time.Nanosecond, time.Millisecond, time.Second, time.Hour} {
			for _, zone := range zones {
				expires := now.Add(anchor + delta).In(zone)
				for _, text := range []string{expires.Format(time.RFC3339Nano), expires.Format("2006-01-02T15:04:05.000000000Z07:00"), expires.Format("2006-01-02T15:04:05Z07:00")} {
					raw := mustMarshal(t, map[string]any{"scheme": "ws", "address": "localhost:80", "expiresAt": text})
					total++
					want, got := goValidate(raw, policy), e.engineValidate(raw, policy)
					if want == "ok" {
						accepted++
					}
					if want != got {
						diverged++
						if diverged <= 8 {
							t.Errorf("expires %s (now %s): Go %s, engine %s", text, now.Format(time.RFC3339Nano), want, got)
						}
					}
				}
			}
		}
	}
	t.Logf("%d expiries (%d admitted), %d divergences", total, accepted, diverged)
	if accepted == 0 || accepted == total {
		t.Fatalf("the expiries do not exercise both outcomes: %d of %d admitted", accepted, total)
	}
}

// A policy with no clock uses the wall clock, as the Go package does.
func EndpointDefaultClock(t *testing.T, e Engine) {
	policy := mustMarshal(t, map[string]any{"schemes": []string{"tcp"}})
	soon := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)
	late := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339Nano)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	for text, want := range map[string]string{soon: "ok", late: "invalid", past: "expired"} {
		raw := mustMarshal(t, map[string]any{"scheme": "tcp", "address": "[::1]:9", "expiresAt": text})
		if got := e.engineValidate(raw, policy); got != want {
			t.Errorf("expires %s: engine %s, want %s", text, got, want)
		}
		if got := goValidate(raw, policy); got != want {
			t.Errorf("expires %s: Go %s, want %s", text, got, want)
		}
	}
}

// An endpoint is a credential: no outcome carries a part of one.
func EndpointErrorsCarryNoValue(t *testing.T, e Engine) {
	secret := "s3cr3t-token-value"
	raw := mustMarshal(t, map[string]any{"scheme": "ws", "address": "10.9.8.7:6543", "path": "/hidden/path", "token": secret + " with space", "expiresAt": "2030-01-01T00:30:00Z"})
	for _, call := range []func() error{
		func() error { _, err := e.Call("endpoint.check", raw); return err },
		func() error {
			input, _ := json.Marshal(map[string]json.RawMessage{"endpoint": raw, "policy": mustMarshal(t, map[string]any{"schemes": []string{"ws"}})})
			_, err := e.Call("endpoint.validate", input)
			return err
		},
	} {
		err := call()
		if err == nil {
			t.Fatal("the endpoint is not on a loopback address")
		}
		for _, leak := range []string{secret, "10.9.8.7", "6543", "hidden"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("the error %q carries part of the endpoint: %q", err.Error(), leak)
			}
		}
	}
}

// Go parses a time with a lenient fallback when its fast path refuses a string
// (strict RFC 3339 checking is switched off in its source), so it accepts a few
// spellings that are not RFC 3339: an offset of 24 hours or 60 minutes, a comma
// before the fraction, year 0000. The engine accepts RFC 3339
// and nothing else, which is the safe side of the difference: a host built on the
// engine refuses an endpoint a Go host would admit, never the other way round.
// The engine's side is pinned; Go's is only reported, since it may tighten.
func EndpointTimeSpellingsAreStricterInTheEngine(t *testing.T, e Engine) {
	policy := mustMarshal(t, map[string]any{"schemes": []string{"ws"}, "now": "2030-01-01T00:00:00Z"})
	for _, text := range []string{
		"2030-01-01T00:30:00+24:00", "2030-01-01T00:30:00+23:60", "2030-01-01T1:30:00Z", "2030-01-01T00:30:00,5Z", "0000-06-01T00:30:00Z",
	} {
		raw := mustMarshal(t, map[string]any{"scheme": "ws", "address": "localhost:80", "expiresAt": text})
		got := e.engineValidate(raw, policy)
		t.Logf("%-28q Go: %-8s engine: %s", text, goValidate(raw, policy), got)
		if got != "invalid" {
			t.Errorf("expires %q: the engine answered %s, want invalid", text, got)
		}
	}
}

// Go's zero time is no time at all, in whatever zone it is written.
func ZeroTimeInAnyZoneMatchesGo(t *testing.T, e Engine) {
	for _, text := range []string{"0001-01-01T00:00:00Z", "0001-01-01T00:00:00.000000000Z", "0001-01-01T01:00:00+01:00", "0001-01-01T00:30:00+00:30", "0000-12-31T23:00:00-01:00"} {
		frame := mustMarshal(t, map[string]any{"frame": "reverse_request", "apiVersion": "ext.plugin/v1", "id": "g1", "parent": "1",
			"contract": map[string]any{"name": "host.svc", "version": "v1"}, "operation": "sign", "deadline": text})
		want, got := goFrameValidates(frame), error(nil)
		_, got = e.Call("frame.validate", frame)
		if (want == nil) != (got == nil) {
			t.Errorf("deadline %q: Go=%s engine=%s", text, errText(want), errText(got))
		}
		if want == nil {
			t.Errorf("deadline %q: Go accepted the zero time", text)
		}
	}
}
