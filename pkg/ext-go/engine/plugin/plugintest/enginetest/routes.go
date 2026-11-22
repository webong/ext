package enginetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/interop"
)

// routeOutcome names how a route resolution ended, in the vocabulary of route-cases.json.
func routeOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, plugin.ErrDenied):
		return "denied"
	case errors.Is(err, plugin.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, plugin.ErrInvalid):
		return "invalid"
	}
	return "other: " + err.Error()
}

type routeCase struct {
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Outcome string          `json:"outcome"`
	Route   json.RawMessage `json:"route"`
}

type routeInput struct {
	Hosts        []plugin.BackendProfile `json:"hosts"`
	Guests       []plugin.BackendProfile `json:"guests"`
	Bridges      []interop.Bridge        `json:"bridges"`
	Requirements *struct {
		Concurrent      bool     `json:"concurrent"`
		NativeStreaming bool     `json:"nativeStreaming"`
		NativeCallbacks bool     `json:"nativeCallbacks"`
		Boundaries      []string `json:"boundaries"`
	} `json:"requirements"`
}

// goRoute is interop.Resolve on the case's input, as the service reads it: input that does not
// decode is invalid.
func goRoute(raw json.RawMessage) (interop.Route, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var in routeInput
	if err := decoder.Decode(&in); err != nil {
		return interop.Route{}, plugin.ErrInvalid
	}
	var w interop.Requirements
	if r := in.Requirements; r != nil {
		w = interop.Requirements{Concurrent: r.Concurrent, NativeStreaming: r.NativeStreaming, NativeCallbacks: r.NativeCallbacks, Boundaries: r.Boundaries}
	}
	return interop.Resolve(in.Hosts, in.Guests, in.Bridges, w)
}

// sameRoute compares two routes as data: field order means nothing.
func sameRoute(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	p, _ := json.Marshal(x)
	q, _ := json.Marshal(y)
	return bytes.Equal(p, q)
}

// RouteFixture holds route.resolve, with sandbox boundaries, to route-cases.json in both the
// engine and the Go oracle (interop.Resolve): the same outcome for every case, and for ok the
// same route.
func RouteFixture(t *testing.T, e Engine) {
	data, err := e.fixture(t, "v1/route-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []routeCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	for _, c := range fixture.Cases {
		route, err := goRoute(c.Input)
		if got := routeOutcome(err); got != c.Outcome {
			t.Errorf("%s: Go %s, fixture %s", c.Name, got, c.Outcome)
		} else if err == nil {
			encoded, _ := json.Marshal(route)
			if !sameRoute(encoded, c.Route) {
				t.Errorf("%s: Go route %s, fixture %s", c.Name, encoded, c.Route)
			}
		}
		out, err := e.Call("route.resolve", c.Input)
		if got := routeOutcome(err); got != c.Outcome {
			t.Errorf("%s: %s %s, fixture %s", c.Name, e.Name, got, c.Outcome)
		} else if err == nil && !sameRoute(out, c.Route) {
			t.Errorf("%s: %s route %s, fixture %s", c.Name, e.Name, out, c.Route)
		}
	}
}
