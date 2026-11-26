package crosslang_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webong/ext/res/web/host/contract"
)

func TestPythonBrowserPolicyAdapterUsesPortableWireContract(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	fixturePath := filepath.Join("..", "..", "res", "web", "testdata", "policy", "v2", "cases.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
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
			guest := filepath.Join("testdata", "policy_adapter.py")
			command := exec.Command(python, guest, "share", "default", "--", "policy", "export")
			command.Stdin = bytes.NewReader(test.Value)
			output, err := command.Output()
			if (err == nil) != test.Valid {
				t.Fatalf("exit success = %v, want %v; err = %v", err == nil, test.Valid, err)
			}
			if !test.Valid {
				return
			}
			var bundle contract.PolicyBundle
			if err := json.Unmarshal(output, &bundle); err != nil {
				t.Fatal(err)
			}
			if err := bundle.Validate(); err != nil {
				t.Fatalf("Python adapter returned an invalid bundle: %v", err)
			}
		})
	}
}
