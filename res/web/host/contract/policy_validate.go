package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DecodePolicyRequest reads one bounded policy.export request. No other fields
// are part of this wire operation.
func DecodePolicyRequest(input io.Reader) (PolicyRequest, error) {
	var request PolicyRequest
	data, err := io.ReadAll(io.LimitReader(input, (8<<20)+1))
	if err != nil {
		return request, err
	}
	if len(data) > 8<<20 {
		return request, errors.New("policy request exceeds 8 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return request, errors.New("policy request must contain exactly one JSON object")
	}
	return request, request.Validate()
}

// Validate checks the language-neutral policy.export request.
func (request PolicyRequest) Validate() error {
	if request.Version != Version {
		return fmt.Errorf("unsupported policy request version %d", request.Version)
	}
	return nil
}

// Validate checks a policy.export response before a host exposes it.
// The source is assigned by the host, so an adapter may leave it empty.
func (bundle PolicyBundle) Validate() error {
	if bundle.Version != Version {
		return fmt.Errorf("unsupported policy bundle version %d", bundle.Version)
	}
	if bundle.Entries == nil {
		return errors.New("policy bundle entries must be an array")
	}
	for index, entry := range bundle.Entries {
		if strings.TrimSpace(entry.Location) == "" || strings.TrimSpace(entry.Level) == "" || strings.TrimSpace(entry.Format) == "" {
			return fmt.Errorf("policy entry %d needs location, level, and format", index)
		}
		if entry.Format == "json" && !json.Valid([]byte(entry.Content)) {
			return fmt.Errorf("policy entry %d has invalid JSON content", index)
		}
	}
	return nil
}
