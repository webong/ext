package observation

import (
	"encoding/json"
	"github.com/webong/ext/pkg/ext-go/core/ledger/ledgertest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The shared recorder fixtures are the wire contract, not res/system's Go structs.
func TestRecorderWireRecords(t *testing.T) {
	dir, err := ledgertest.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(dir)), "recorder", "v1", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name  string
			Steps []struct {
				Op    string
				Input json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range fixtures.Cases {
		for _, step := range c.Steps {
			var target any
			switch step.Op {
			case "observe_host":
				target = &HostInventory{}
			case "observe_processes":
				target = &ProcessSnapshot{}
			default:
				continue
			}
			t.Run(c.Name+"/"+step.Op, func(t *testing.T) {
				if err := json.Unmarshal(step.Input, target); err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(target)
				if err != nil {
					t.Fatal(err)
				}
				var want, got any
				if err := json.Unmarshal(step.Input, &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(encoded, &got); err != nil {
					t.Fatal(err)
				}
				checkWire(t, "", want, got)
			})
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no recorder wire cases executed")
	}
}
func checkWire(t *testing.T, path string, want, got any) {
	t.Helper()
	switch v := want.(type) {
	case map[string]any:
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("%s: missing object", path)
		}
		for k, x := range v {
			checkWire(t, path+"/"+k, x, m[k])
		}
	case []any:
		if len(v) == 0 && got == nil {
			return
		}
		a, ok := got.([]any)
		if !ok || len(a) != len(v) {
			t.Fatalf("%s: array changed", path)
		}
		for i, x := range v {
			checkWire(t, path, x, a[i])
		}
	default:
		if got == nil && (want == nil || want == "" || want == false || want == float64(0)) {
			return
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: want %#v, got %#v", path, want, got)
		}
	}
}
