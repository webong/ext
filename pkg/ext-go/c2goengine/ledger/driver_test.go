//go:build ext_ledger_c2go && (darwin || linux) && (amd64 || arm64)

package ledger_test

import (
	"context"
	"encoding/json"
	"errors"
	native "github.com/webong/ext/pkg/ext-go/c2goengine/ledger"
	model "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"github.com/webong/ext/pkg/ext-go/core/ledger/ledgertest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTranslatedLedgerConformance(t *testing.T) {
	l, err := native.Bind()
	if err != nil {
		t.Fatal(err)
	}
	b := ledgertest.Backend{Durable: true, Open: func(t testing.TB, dir string, o model.Options) model.Store {
		s, err := l.OpenStore(context.Background(), "file", dir, o)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}}
	ledgertest.Run(t, b)
	ledgertest.RunBehaviour(t, b)
}

func TestTranslatedLedgerMemoryAndFingerprints(t *testing.T) {
	l, err := native.Bind()
	if err != nil {
		t.Fatal(err)
	}
	b := ledgertest.Backend{Open: func(t testing.TB, _ string, o model.Options) model.Store {
		s, err := l.OpenStore(context.Background(), "memory", "", o)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}}
	ledgertest.RunBehaviour(t, b)
	ledgertest.RunFingerprints(t, func(tx model.Transaction) (json.RawMessage, string, error) {
		return l.CanonicalTransaction(context.Background(), tx)
	})
}

func TestTranslatedLedgerUnsupported(t *testing.T) {
	l, err := native.Bind()
	if err != nil {
		t.Fatal(err)
	}
	check := func(err error) {
		t.Helper()
		var code *library.CodeError
		if !errors.As(err, &code) || code.Name != "unsupported" {
			t.Fatalf("want unsupported, got %v", err)
		}
	}
	check(l.Collect(context.Background(), "processes", nil, nil))
	s, err := l.Objects().Open("store", map[string]any{"backend": "memory"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = l.Objects().Open("recorder", map[string]any{"store": s.Handle(), "machine": true}, nil)
	check(err)
	_, err = l.Objects().Open("supervisor", map[string]any{"store": s.Handle(), "runtime_id": "fixture"}, nil)
	check(err)
}

func TestTranslatedLedgerRecorderFixtures(t *testing.T) {
	l, err := native.Bind()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ledgertest.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(dir)), "recorder/v1/cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name        string
			Environment json.RawMessage
			Steps       []struct {
				Op     string
				Input  json.RawMessage
				Error  string
				Expect json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Cases) == 0 {
		t.Fatal("no recorder cases")
	}
	for _, c := range fixtures.Cases {
		t.Run(c.Name, func(t *testing.T) {
			s, err := l.Objects().Open("store", map[string]any{"backend": "memory"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r, err := l.Objects().Open("recorder", map[string]any{"store": s.Handle(), "environment": c.Environment}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			for i, step := range c.Steps {
				var out json.RawMessage
				if step.Op == "snapshot" {
					err = s.Call("snapshot", map[string]any{"namespace": "ext.system"}, &out)
				} else {
					in := map[string]any{"operation": step.Op}
					if len(step.Input) > 0 {
						in["input"] = step.Input
					}
					err = r.Call("record", in, &out)
				}
				code := "ok"
				if err != nil {
					var c *library.CodeError
					if !errors.As(err, &c) {
						t.Fatal(err)
					}
					code = c.Name
				}
				want := step.Error
				if want == "" {
					want = "ok"
				}
				if code != want {
					t.Fatalf("step %d %s: %s, want %s", i, step.Op, code, want)
				}
				if len(step.Expect) > 0 && err == nil {
					var want, got any
					if err := json.Unmarshal(step.Expect, &want); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(out, &got); err != nil {
						t.Fatal(err)
					}
					if !subset(want, got) {
						t.Fatalf("step %d %s: recorder output differs from canonical fixture", i, step.Op)
					}
				}
			}
		})
	}
}
func subset(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			if !subset(v, g[k]) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(w) != len(g) {
			return false
		}
		for i, v := range w {
			if !subset(v, g[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, got)
	}
}
