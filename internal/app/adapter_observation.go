package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/webong/ctx/pkg/graph"
	systemgraph "github.com/webong/ctx/pkg/graph/system"
)

const maxAdapterObservationBytes = 1 << 20

type adapterObservationDocument struct {
	Version   int                              `json:"version"`
	Contexts  []systemgraph.ContextObservation `json:"contexts"`
	Resources []struct {
		ID         string         `json:"id"`
		Kind       string         `json:"kind"`
		Context    string         `json:"context"`
		Attributes map[string]any `json:"attributes,omitempty"`
	} `json:"resources,omitempty"`
	Relations []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	} `json:"relations,omitempty"`
}

func parseAdapterObservation(data []byte, declaredCapabilities, declaredSupports, declaredBrowserShare []string) ([]systemgraph.ContextObservation, []systemgraph.ResourceObservation, []systemgraph.RelationObservation, error) {
	if len(data) > maxAdapterObservationBytes {
		return nil, nil, nil, fmt.Errorf("adapter observation exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document adapterObservationDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid adapter observation: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, nil, nil, fmt.Errorf("adapter observation must contain one JSON object")
	}
	if document.Version != 1 {
		return nil, nil, nil, fmt.Errorf("unsupported adapter observation version %d", document.Version)
	}
	if len(document.Contexts) > 4096 || len(document.Resources) > 4096 || len(document.Relations) > 8192 {
		return nil, nil, nil, fmt.Errorf("adapter observation has too many records")
	}
	capabilities := map[string]bool{}
	for _, capability := range declaredCapabilities {
		capabilities[capability] = true
	}
	supports := map[string]bool{}
	for _, kind := range declaredSupports {
		supports[kind] = true
	}
	browserShare := map[string]bool{}
	for _, operation := range declaredBrowserShare {
		browserShare[operation] = true
	}
	contexts := map[string]bool{}
	for _, item := range document.Contexts {
		if !validObservationName(item.Selection) || contexts[item.Selection] {
			return nil, nil, nil, fmt.Errorf("adapter observation has an invalid or duplicate context")
		}
		contexts[item.Selection] = true
		if err := graph.ValidateAttributes(item.Attributes); err != nil {
			return nil, nil, nil, fmt.Errorf("adapter context metadata is invalid: %w", err)
		}
		for _, capability := range item.Capabilities {
			if !capabilities[capability] {
				return nil, nil, nil, fmt.Errorf("adapter context declares undeclared capability %q", capability)
			}
		}
		seenSupports := map[string]bool{}
		for _, kind := range item.Supports {
			if !supports[kind] || seenSupports[kind] {
				return nil, nil, nil, fmt.Errorf("adapter context declares undeclared or duplicate support %q", kind)
			}
			seenSupports[kind] = true
		}
		for operation, state := range item.BrowserShare {
			if !browserShare[operation] || !validBrowserShareState(state) {
				return nil, nil, nil, fmt.Errorf("adapter context declares invalid browser share state for %q", operation)
			}
		}
	}
	resources := make([]systemgraph.ResourceObservation, 0, len(document.Resources))
	resourceIDs := map[string]bool{}
	for _, item := range document.Resources {
		if !validObservationName(item.ID) || !validObservationName(item.Kind) || !contexts[item.Context] || resourceIDs[item.ID] {
			return nil, nil, nil, fmt.Errorf("adapter observation has an invalid or duplicate resource")
		}
		resourceIDs[item.ID] = true
		if err := graph.ValidateAttributes(item.Attributes); err != nil {
			return nil, nil, nil, fmt.Errorf("adapter resource metadata is invalid: %w", err)
		}
		resources = append(resources, systemgraph.ResourceObservation{ID: item.ID, Kind: item.Kind, Context: item.Context, Attributes: item.Attributes})
	}
	relations := make([]systemgraph.RelationObservation, 0, len(document.Relations))
	for _, item := range document.Relations {
		if !resourceIDs[item.From] || !resourceIDs[item.To] || !validObservationName(item.Kind) {
			return nil, nil, nil, fmt.Errorf("adapter observation has an invalid resource relation")
		}
		relations = append(relations, systemgraph.RelationObservation{From: item.From, To: item.To, Kind: item.Kind})
	}
	return document.Contexts, resources, relations, nil
}

func validBrowserShareState(state string) bool {
	return state == "ready" || state == "blocked" || state == "unknown"
}

func validObservationName(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n")
}
