package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/ext/res/web/host/contract"
)

func TestGoBrowserPolicyHelperUsesPortableWireContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.json")
	if err := os.WriteFile(path, []byte(`{"HomepageLocation":"https://example.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	request := []byte(`{"version":2}`)
	if code := RunPolicyExport(bytes.NewReader(request), &stdout, &stderr, PolicySources{Files: []PolicyFile{{Path: path, Level: "managed", Format: "json"}}}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var bundle contract.PolicyBundle
	if err := json.Unmarshal(stdout.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Entries) != 1 || bundle.Entries[0].Location != path {
		t.Fatalf("unexpected policy entries: %+v", bundle.Entries)
	}
}

func TestGoBrowserPolicyHelperUsesSharedRequestCases(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "res", "web", "testdata", "policy", "v2", "cases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skip("shared policy fixtures are outside this adapter module")
		}
		t.Fatal(err)
	}
	var fixtures struct {
		Requests []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
			Valid bool            `json:"valid"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Requests) == 0 {
		t.Fatal("no policy request fixtures")
	}
	for _, test := range fixtures.Requests {
		t.Run(test.Name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := RunPolicyExport(bytes.NewReader(test.Value), &stdout, &stderr, PolicySources{})
			if (code == 0) != test.Valid {
				t.Fatalf("exit %d, want success %v: %s", code, test.Valid, stderr.String())
			}
			if test.Valid {
				var bundle contract.PolicyBundle
				if err := json.Unmarshal(stdout.Bytes(), &bundle); err != nil {
					t.Fatal(err)
				}
				if err := bundle.Validate(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
