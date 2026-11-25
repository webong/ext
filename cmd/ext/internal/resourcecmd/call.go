package resourcecmd

import (
	"encoding/json"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

type callRequest struct {
	Resource  json.RawMessage    `json:"resource"`
	Contract  plugin.ContractRef `json:"contract"`
	Operation string             `json:"operation"`
	Input     json.RawMessage    `json:"input"`
}

// The user explicitly requested this exact operation. The resource reference
// travels in the payload so providers can enforce their domain authority; it is
// not a grant. A contract used here must accept {resource, input} as its payload.
func invokeResource(lib *library.Described, host *library.Object, descriptor plugin.Descriptor, inspection json.RawMessage, request callRequest) (json.RawMessage, error) {
	selection := map[string]any{"descriptor": descriptor, "inspection": inspection, "requirement": map[string]any{"resource": request.Resource, "contract": request.Contract, "operation": request.Operation}}
	payload := map[string]any{"resource": request.Resource, "input": request.Input}
	if err := lib.Library().Call("resource.call.validate", map[string]any{"selection": selection, "payload": payload}, nil); err != nil {
		return nil, err
	}
	var response plugin.Response
	if err := host.Call("call", map[string]any{"call": map[string]any{"contract": request.Contract, "operation": request.Operation, "payload": payload}, "timeout_ms": 5000}, &response); err != nil {
		return nil, err
	}
	// These are errors of the domain operation, not discovery errors. Preserve
	// their existing plugin envelope; the provider owns the domain mapping.
	if response.Error != nil {
		return nil, response.Error
	}
	if err := lib.Library().Call("resource.result.validate", map[string]any{"selection": selection, "payload": response.Payload}, nil); err != nil {
		return nil, err
	}
	return response.Payload, nil
}
