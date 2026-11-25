package plugin

import (
	"encoding/json"
	"errors"
	contract "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"os"
	"reflect"
	"testing"
)

// Catalog and bundle semantics are C-first: compare the translation to the shared cases,
// not to new features in the retiring pure-Go host.
func TestCatalogAndBundleFixtures(t *testing.T) {
	if !Available {
		t.Skip("requires the ext_c2go translation")
	}
	for _, file := range []string{"catalog-cases.json", "bundle-cases.json", "resource-cases.json", "dependency-cases.json", "routing-cases.json"} {
		t.Run(file, func(t *testing.T) { testManagementFixtures(t, file) })
	}
}
func testManagementFixtures(t *testing.T, file string) {
	data, err := os.ReadFile("../../../../lib/plugin/testdata/v1/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name, Operation, Status string
			Input, Result           json.RawMessage
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) < 10 {
		t.Fatal("incomplete catalog cases")
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			out, err := EngineCall(c.Operation, c.Input)
			switch c.Status {
			case "ok":
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err := json.Unmarshal(out, &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(c.Result, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %s want %s", out, c.Result)
				}
			case "invalid":
				if !errors.Is(err, contract.ErrInvalid) {
					t.Fatalf("want invalid: %v", err)
				}
			case "mismatch":
				if !errors.Is(err, contract.ErrMismatch) {
					t.Fatalf("want mismatch: %v", err)
				}
			case "unsupported":
				if !errors.Is(err, contract.ErrUnsupported) {
					t.Fatalf("want unsupported: %v", err)
				}
			case "ambiguous":
				if !errors.Is(err, contract.ErrAmbiguous) {
					t.Fatalf("want ambiguous: %v", err)
				}
			case "not_found":
				if !errors.Is(err, contract.ErrNotFound) {
					t.Fatalf("want not_found: %v", err)
				}
			default:
				t.Fatalf("unknown status %s", c.Status)
			}
		})
	}
}
