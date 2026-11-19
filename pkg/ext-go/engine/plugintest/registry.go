package plugintest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// RegistryCases is the shared guest registry fixture,
// lib/plugin/testdata/v1/registry-cases.json: the relay guest, the guests a
// case opens, the edges it declares and the cases. Its "about" fields say what a
// host runs.
type RegistryCases struct {
	Guest struct {
		Reverse struct {
			Name       string   `json:"name"`
			Version    string   `json:"version"`
			Operations []string `json:"operations"`
		} `json:"reverse"`
		FailError plugin.RemoteError `json:"failError"`
	} `json:"guest"`
	// Guests maps a guest's name to the contract it serves (version v1).
	Guests map[string]string `json:"guests"`
	Edges  []RegistryEdge    `json:"edges"`
	Cases  []RegistryCase    `json:"cases"`
}

// RegistryEdgeEnd is one end of an edge.
type RegistryEdgeEnd struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Operation string `json:"operation"`
}

// Ref is the end's contract.
func (e RegistryEdgeEnd) Ref() plugin.ContractRef {
	return plugin.ContractRef{Name: e.Name, Version: e.Version}
}

// RegistryEdge routes the reverse operation From to the forward operation To.
type RegistryEdge struct {
	From RegistryEdgeEnd `json:"from"`
	To   RegistryEdgeEnd `json:"to"`
}

// RegistryCase is one case: the guests whose reverse or forward authorize
// denies, and the steps.
type RegistryCase struct {
	Name        string         `json:"name"`
	ReverseDeny []string       `json:"reverseDeny"`
	ForwardDeny []string       `json:"forwardDeny"`
	Steps       []RegistryStep `json:"steps"`
}

// RegistryStep calls a guest's operation, or unregisters a guest's contract.
type RegistryStep struct {
	Call        string          `json:"call"`
	Operation   string          `json:"operation"`
	Payload     json.RawMessage `json:"payload"`
	TimeoutMs   int             `json:"timeoutMs"`
	Result      json.RawMessage `json:"result"`
	ResultError []string        `json:"resultError"`
	Unregister  string          `json:"unregister"`
}

// RegistryCasesPath finds the fixture: it walks up from the working directory
// to the repository's lib/plugin/testdata/v1, and reports os.ErrNotExist
// outside the repository (a module fetched on its own).
func RegistryCasesPath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, "lib", "plugin", "testdata", "v1", "registry-cases.json")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// ReadRegistryCases reads the fixture at path.
func ReadRegistryCases(path string) (RegistryCases, error) {
	var cases RegistryCases
	data, err := os.ReadFile(path)
	if err != nil {
		return cases, err
	}
	return cases, json.Unmarshal(data, &cases)
}

// Contract is the contract the named guest serves.
func (c RegistryCases) Contract(name string) plugin.ContractRef {
	return plugin.ContractRef{Name: c.Guests[name], Version: "v1"}
}

// RelayDescriptor is the descriptor of the named relay guest.
func (c RegistryCases) RelayDescriptor(name string) plugin.Descriptor {
	reverse := plugin.Contract{ContractRef: plugin.ContractRef{Name: c.Guest.Reverse.Name, Version: c.Guest.Reverse.Version}}
	for _, op := range c.Guest.Reverse.Operations {
		reverse.Operations = append(reverse.Operations, plugin.Operation{Name: op})
	}
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "registry/" + name, Revision: "r1", Version: "1.0.0"},
		Contracts:  []plugin.Contract{{ContractRef: c.Contract(name), Operations: []plugin.Operation{{Name: "echo"}, {Name: "fail"}, {Name: "slow"}, {Name: "relay"}}}},
		Reverse:    []plugin.Contract{reverse},
	}
}

// RelayGuest is the named relay guest, which the fixture's guest.about
// describes.
func (c RegistryCases) RelayGuest(name string) (*plugin.Guest, error) {
	reverseRef := plugin.ContractRef{Name: c.Guest.Reverse.Name, Version: c.Guest.Reverse.Version}
	fail := c.Guest.FailError
	return plugin.NewGuest(c.RelayDescriptor(name), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		switch r.Operation {
		case "echo":
			if len(r.Payload) == 0 {
				return json.RawMessage("null"), nil
			}
			return r.Payload, nil
		case "fail":
			e := fail
			return nil, &e
		case "slow":
			var in struct {
				Ms int `json:"ms"`
			}
			_ = json.Unmarshal(r.Payload, &in)
			time.Sleep(time.Duration(in.Ms) * time.Millisecond)
			return json.RawMessage(`{"slept":true}`), nil
		}
		var in struct {
			Operation  string          `json:"operation"`
			Payload    json.RawMessage `json:"payload"`
			Scope      string          `json:"scope"`
			DeadlineMs int             `json:"deadlineMs"`
		}
		if err := json.Unmarshal(r.Payload, &in); err != nil {
			return nil, err
		}
		caller := plugin.ReverseFrom(ctx)
		if in.Scope == "session" {
			caller = plugin.SessionReverseFrom(ctx)
		}
		if caller == nil {
			return nil, errors.New("no reverse caller")
		}
		if in.DeadlineMs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(in.DeadlineMs)*time.Millisecond)
			defer cancel()
		}
		payload, err := caller.Call(ctx, reverseRef, in.Operation, in.Payload)
		var remote *plugin.RemoteError
		if errors.As(err, &remote) {
			return json.Marshal(map[string]any{"error": remote})
		}
		if err != nil && ctx.Err() != nil {
			// The reverse call's deadline passed before its answer came.
			return json.Marshal(map[string]any{"error": plugin.RemoteError{Code: plugin.ReverseExpired, Message: "reverse call expired"}})
		}
		if err != nil {
			return nil, err
		}
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		return json.Marshal(map[string]json.RawMessage{"payload": payload})
	}})
}

// RegistryHost is what RunRegistryCases drives: the hosts of one case, each
// guest attached to one registry as a source.
type RegistryHost interface {
	// Open opens, attaches and registers the named guest. denyForward makes its
	// forward authorize deny, denyReverse its reverse authorize.
	Open(name string, contract plugin.ContractRef, denyForward, denyReverse bool) error
	Route(RegistryEdge) error
	Unregister(contract plugin.ContractRef) error
	Call(ctx context.Context, name string, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error)
	// Close closes every host and frees the registry.
	Close()
}

// RunRegistryCases runs every case against a fresh host from open.
func RunRegistryCases(t *testing.T, cases RegistryCases, open func(t *testing.T) RegistryHost) {
	t.Helper()
	names := make([]string, 0, len(cases.Guests))
	for name := range cases.Guests {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, c := range cases.Cases {
		t.Run(c.Name, func(t *testing.T) {
			h := open(t)
			defer h.Close()
			for _, name := range names {
				if err := h.Open(name, cases.Contract(name), slices.Contains(c.ForwardDeny, name), slices.Contains(c.ReverseDeny, name)); err != nil {
					t.Fatalf("open %s: %v", name, err)
				}
			}
			for _, e := range cases.Edges {
				if err := h.Route(e); err != nil {
					t.Fatalf("route %+v: %v", e, err)
				}
			}
			for i, s := range c.Steps {
				if s.Unregister != "" {
					if err := h.Unregister(cases.Contract(s.Unregister)); err != nil {
						t.Fatalf("step %d: unregister: %v", i, err)
					}
					continue
				}
				timeout := 5 * time.Second
				if s.TimeoutMs > 0 {
					timeout = time.Duration(s.TimeoutMs) * time.Millisecond
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				payload := s.Payload
				if len(payload) == 0 {
					payload = json.RawMessage("null")
				}
				got, err := h.Call(ctx, s.Call, cases.Contract(s.Call), s.Operation, payload)
				cancel()
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if err := s.check(got); err != nil {
					t.Errorf("step %d: %v", i, err)
				}
			}
		})
	}
}

func (s RegistryStep) check(got json.RawMessage) error {
	if s.ResultError != nil {
		var out struct {
			Error *plugin.RemoteError `json:"error"`
		}
		if err := json.Unmarshal(got, &out); err != nil || out.Error == nil || !slices.Contains(s.ResultError, out.Error.Code) {
			return fmt.Errorf("got %s, want an error coded %v", got, s.ResultError)
		}
		return nil
	}
	if !sameJSON(got, s.Result) {
		return fmt.Errorf("got %s, want %s", got, s.Result)
	}
	return nil
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}
