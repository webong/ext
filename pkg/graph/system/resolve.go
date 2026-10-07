package systemgraph

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/webong/ext/pkg/graph"
)

// ContextCandidate is a graph observation, not authorization to run an
// adapter. Callers must still check trust and validate the native selection.
type ContextCandidate struct {
	Adapter           string            `json:"adapter"`
	Runtime           string            `json:"runtime"`
	Selection         string            `json:"selection"`
	SelectionDigest   string            `json:"selection_digest"`
	SelectionRedacted bool              `json:"selection_redacted,omitempty"`
	Capabilities      []string          `json:"capabilities"`
	Supports          []string          `json:"supports,omitempty"`
	BrowserShare      map[string]string `json:"browser_share,omitempty"`
	ObservedAt        time.Time         `json:"observed_at"`
}

// Inventory is the latest reconciled adapter view. ObservedAt is refreshed on
// every scan, even when the set of contexts is unchanged.
type Inventory struct {
	ObservedAt time.Time          `json:"observed_at"`
	Contexts   []ContextCandidate `json:"contexts"`
}

// ResolveInventory reads the CTX projection from the public graph. A blank
// runtime returns every context. Redacted selections remain in the graph for
// inspection but cannot be used as executable candidates.
func (g *Graph) ResolveInventory(ctx context.Context, runtimeName string) (Inventory, error) {
	snapshot, err := g.Store.Snapshot(ctx, Namespace)
	if err != nil {
		return Inventory{}, err
	}
	result := Inventory{}
	adapters := map[string]graph.Vertex{}
	capabilityNames := map[string]string{}
	adapterCapabilities := map[string][]string{}
	supportNames := map[string]string{}
	adapterSupports := map[string][]string{}
	for _, vertex := range snapshot.Vertices {
		switch vertex.Kind {
		case Namespace + "/inventory":
			if vertex.ID == "inventory/current" {
				if value, ok := vertex.Attributes["observed_at"].(string); ok {
					result.ObservedAt, _ = time.Parse(time.RFC3339Nano, value)
				}
			}
		case Namespace + "/adapter":
			if name, ok := vertex.Attributes["name"].(string); ok {
				adapters[name] = vertex
			}
		case Namespace + "/capability":
			if name, ok := vertex.Attributes["name"].(string); ok {
				capabilityNames[vertex.ID] = name
			}
		case Namespace + "/support":
			if name, ok := vertex.Attributes["name"].(string); ok {
				supportNames[vertex.ID] = name
			}
		}
	}
	for _, edge := range snapshot.Edges {
		if edge.Type == Namespace+"/supports" {
			if name := capabilityNames[edge.To]; name != "" {
				adapterCapabilities[edge.From] = append(adapterCapabilities[edge.From], name)
			}
		}
		if edge.Type == Namespace+"/supports-kind" {
			if name := supportNames[edge.To]; name != "" {
				adapterSupports[edge.From] = append(adapterSupports[edge.From], name)
			}
		}
	}
	for _, vertex := range snapshot.Vertices {
		if vertex.Kind != Namespace+"/context" {
			continue
		}
		adapter, _ := vertex.Attributes["adapter"].(string)
		selection, _ := vertex.Attributes["selection"].(string)
		selectionDigest, _ := vertex.Attributes["selection_digest"].(string)
		adapterVertex, ok := adapters[adapter]
		if !ok || selectionDigest == "" || adapterVertex.Attributes["trusted"] != true || adapterVertex.Attributes["listed"] != true {
			continue
		}
		runtimeValue, _ := adapterVertex.Attributes["runtime"].(string)
		if runtimeName != "" && runtimeValue != runtimeName {
			continue
		}
		capabilityOverride, hasOverride := vertex.Attributes["capabilities"]
		capabilities := stringList(capabilityOverride)
		if !hasOverride {
			// A context without an override inherits its adapter manifest.
			capabilities = append(capabilities, adapterCapabilities[adapterVertex.ID]...)
		}
		supportOverride, hasSupportOverride := vertex.Attributes["supports"]
		supports := stringList(supportOverride)
		if !hasSupportOverride {
			supports = append(supports, adapterSupports[adapterVertex.ID]...)
		}
		sort.Strings(capabilities)
		sort.Strings(supports)
		browserShare := map[string]string{}
		switch statuses := vertex.Attributes["browser_share"].(type) {
		case map[string]string:
			for operation, status := range statuses {
				browserShare[operation] = status
			}
		case map[string]any:
			for operation, status := range statuses {
				if value, ok := status.(string); ok {
					browserShare[operation] = value
				}
			}
		}
		result.Contexts = append(result.Contexts, ContextCandidate{Adapter: adapter, Runtime: runtimeValue, Selection: selection, SelectionDigest: selectionDigest, SelectionRedacted: selection == "", Capabilities: capabilities, Supports: supports, BrowserShare: browserShare, ObservedAt: vertex.Provenance.At})
	}
	sort.Slice(result.Contexts, func(i, j int) bool {
		if result.Contexts[i].Adapter == result.Contexts[j].Adapter {
			return result.Contexts[i].Selection < result.Contexts[j].Selection
		}
		return result.Contexts[i].Adapter < result.Contexts[j].Adapter
	})
	return result, nil
}

func (inventory Inventory) FreshWithin(maxAge time.Duration) bool {
	return !inventory.ObservedAt.IsZero() && time.Since(inventory.ObservedAt) <= maxAge
}

// Find returns a currently observed context with the requested capabilities.
func (inventory Inventory) Find(adapter, selection string, capabilities ...string) (ContextCandidate, error) {
	for _, candidate := range inventory.Contexts {
		if candidate.Adapter != adapter || (candidate.Selection != selection && candidate.SelectionDigest != digest(selection)) {
			continue
		}
		for _, required := range capabilities {
			found := false
			for _, capability := range candidate.Capabilities {
				if capability == required {
					found = true
					break
				}
			}
			if !found {
				return ContextCandidate{}, fmt.Errorf("%s:%s does not offer %s", adapter, selection, required)
			}
		}
		return candidate, nil
	}
	return ContextCandidate{}, fmt.Errorf("%s:%s is not in the current machine graph", adapter, selection)
}

func stringList(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if stringValue, ok := value.(string); ok {
				result = append(result, stringValue)
			}
		}
		return result
	}
	return nil
}
