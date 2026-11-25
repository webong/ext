//go:build ext_cengine && cgo && (darwin || linux || windows)

package packages

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	cgoengine "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestNamedDependencyFixtures(t *testing.T) {
	data, err := os.ReadFile("../../../../../lib/plugin/testdata/v1/dependency-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Operation, Status string
			Input, Result           json.RawMessage
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]error{"ok": nil, "invalid": plugin.ErrInvalid, "not_found": plugin.ErrNotFound, "ambiguous": plugin.ErrAmbiguous}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			native, nativeErr := cgoengine.EngineCall(c.Operation, c.Input)
			var value any
			var goErr error
			switch c.Operation {
			case "descriptor.select":
				var input struct {
					Candidates  []plugin.Descriptor
					Requirement packagekit.Dependency
				}
				if err := json.Unmarshal(c.Input, &input); err != nil {
					t.Fatal(err)
				}
				r := input.Requirement
				value, goErr = plugin.Select(input.Candidates, plugin.Requirement{Contract: r.Contract, Operation: r.Operation, Identity: r.Identity, Provider: r.Provider})
			case "package.resolve":
				var manifests []packagekit.Manifest
				if err := json.Unmarshal(c.Input, &manifests); err != nil {
					t.Fatal(err)
				}
				value, goErr = packagekit.Resolve(manifests)
			case "package.resolve_for":
				var input struct {
					Packages []packagekit.Manifest
					OS, Arch string
				}
				if err := json.Unmarshal(c.Input, &input); err != nil {
					t.Fatal(err)
				}
				value, goErr = packagekit.ResolveFor(input.Packages, input.OS, input.Arch)
			default:
				t.Fatal("unhandled fixture operation", c.Operation)
			}
			expected, ok := statuses[c.Status]
			if !ok {
				t.Fatal("unknown fixture status", c.Status)
			}
			if !errors.Is(nativeErr, expected) || !errors.Is(goErr, expected) {
				t.Fatalf("C %v, Go %v, want %v", nativeErr, goErr, expected)
			}
			if expected == nil {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var a, b, w any
				for _, pair := range []struct {
					raw []byte
					to  *any
				}{{native, &a}, {encoded, &b}, {c.Result, &w}} {
					if err := json.Unmarshal(pair.raw, pair.to); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(a, w) || !reflect.DeepEqual(b, w) {
					t.Fatalf("C %s, Go %s, want %s", native, encoded, c.Result)
				}
			}
		})
	}
}
