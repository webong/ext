package web

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/webong/ext/res/web/contract"
)

// The Go implementation is the behavioral baseline for both native-language ports.
func TestManagementFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/management/v1/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Operations map[string][]string `json:"operations"`
		Cases      []struct {
			Name          string  `json:"name"`
			Request       string  `json:"request"`
			Valid         bool    `json:"valid"`
			Response      *string `json:"response"`
			ResponseValid bool    `json:"response_valid"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.Operations, contract.Operations()) {
		t.Fatal("operation vocabulary drift")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var request contract.Request
			err := json.Unmarshal([]byte(c.Request), &request)
			if err == nil {
				err = contract.ValidateRequest(request)
			}
			if (err == nil) != c.Valid {
				t.Fatalf("request error = %v; valid = %v", err, c.Valid)
			}
			if c.Response == nil || !c.Valid {
				return
			}
			var response contract.Response
			err = json.Unmarshal([]byte(*c.Response), &response)
			if err == nil {
				err = contract.ValidateResponse(response, request)
			}
			if (err == nil) != c.ResponseValid {
				t.Fatalf("response error = %v; valid = %v", err, c.ResponseValid)
			}
		})
	}
}
