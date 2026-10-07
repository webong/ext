// Browser management describes workflows; browser-native operations remain
// owned by the selected adapter.
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

const ManagementVersion = "1.0"

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Request carries one profile-scoped operation. Input is operation-specific
// JSON and may contain paths or source text, so callers should avoid logging it.
type Request struct {
	Version string          `json:"version"`
	Kind    string          `json:"kind"`
	Action  string          `json:"action"`
	Input   json.RawMessage `json:"input,omitempty"`
}

// Response reports the result of one operation. Status is descriptive; it
// must never be used to equate a prepared package with an installed extension.
type Response struct {
	Version string          `json:"version"`
	Kind    string          `json:"kind"`
	Action  string          `json:"action"`
	Status  string          `json:"status"`
	Result  json.RawMessage `json:"result,omitempty"`
}

// Operation names are stable public API names for CTX browser adapters.
var operations = map[string]map[string]bool{
	"extension":   {"targets": true, "capabilities": true, "prepare": true, "package": true, "sign": true, "stage": true, "install": true, "activate": true, "store_install": true, "store_remove": true, "update_manifest": true, "convert": true, "policy": true},
	"userscript":  {"prepare": true, "install": true, "update": true, "list": true, "describe": true, "enable": true, "disable": true, "uninstall": true, "activate": true},
	"session":     {"targets": true, "connect": true, "navigate": true, "inject": true, "replay": true},
	"bookmarklet": {"encode": true, "decode": true, "install_page": true},
}

func Operations() map[string][]string {
	result := make(map[string][]string, len(operations))
	for kind, actions := range operations {
		for action := range actions {
			result[kind] = append(result[kind], action)
		}
		// Keep the API's returned order deterministic without exposing the map.
		for i := 0; i < len(result[kind]); i++ {
			for j := i + 1; j < len(result[kind]); j++ {
				if result[kind][j] < result[kind][i] {
					result[kind][i], result[kind][j] = result[kind][j], result[kind][i]
				}
			}
		}
	}
	return result
}

func NewRequest(kind, action string, input any) (Request, error) {
	request := Request{Version: ManagementVersion, Kind: kind, Action: action}
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return Request{}, err
		}
		request.Input = data
	}
	return request, ValidateRequest(request)
}

func ValidateRequest(request Request) error {
	if request.Version != ManagementVersion {
		return fmt.Errorf("unsupported browser management version %q", request.Version)
	}
	if !namePattern.MatchString(request.Kind) || !operations[request.Kind][request.Action] {
		return fmt.Errorf("unsupported browser management operation %q %q", request.Kind, request.Action)
	}
	if len(request.Input) > 8<<20 {
		return errors.New("browser management input exceeds 8 MiB")
	}
	if len(request.Input) > 0 && !json.Valid(request.Input) {
		return errors.New("browser management input must be JSON")
	}
	if len(request.Input) > 0 && !isJSONObject(request.Input) {
		return errors.New("browser management input must be a JSON object")
	}
	return nil
}

func ValidateResponse(response Response, request Request) error {
	if response.Version != ManagementVersion || response.Kind != request.Kind || response.Action != request.Action {
		return errors.New("browser management response does not match its request")
	}
	if response.Status == "" || len(response.Status) > 64 {
		return errors.New("browser management response needs a status")
	}
	if len(response.Result) > 16<<20 || (len(response.Result) > 0 && !json.Valid(response.Result)) {
		return errors.New("browser management response has invalid or oversized result JSON")
	}
	if request.Kind == "extension" && request.Action == "activate" && response.Status == "installed" {
		return errors.New("session activation cannot report persistent installation")
	}
	if request.Kind == "extension" && request.Action == "install" && response.Status == "activated" {
		return errors.New("installation cannot report session activation")
	}
	return nil
}

func isJSONObject(data []byte) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(data, &value) == nil && value != nil
}
