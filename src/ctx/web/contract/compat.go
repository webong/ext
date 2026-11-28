package contract

import "encoding/json"

// ManagementRequest and ManagementResponse are the explicit names used by
// callers that also work with cookie and policy requests.
type ManagementRequest = Request
type ManagementResponse = Response

func NewManagementRequest(kind, action string, input any) (ManagementRequest, error) {
	return NewRequest(kind, action, input)
}

func ValidateManagementRequest(request ManagementRequest) error { return ValidateRequest(request) }

func ValidateManagementResponse(response ManagementResponse, request ManagementRequest) error {
	return ValidateResponse(response, request)
}

func ManagementOperations() map[string][]string { return Operations() }

func ManagementResult(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
