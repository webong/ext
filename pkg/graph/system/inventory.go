package systemgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"time"

	"github.com/webong/ext/pkg/graph"
)

// AdapterObservation describes an installed adapter and any contexts it listed.
// Listed means its discovery operation completed; it does not prove each
// configured remote endpoint is running or authorize its use.
type AdapterObservation struct {
	Name, Runtime, Selector                          string
	DiscoveryStatus                                  string
	Surfaces, Capabilities, Supports                 []string
	BrowserShare                                     []string
	Trusted, ListSupported, ObserveSupported, Listed bool
	Contexts                                         []ContextObservation
	Resources                                        []ResourceObservation
	Relations                                        []RelationObservation
}

// ContextObservation is a native adapter selection with optional narrower
// capabilities and resource support, plus ordinary non-secret metadata.
type ContextObservation struct {
	Selection    string            `json:"selection"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Supports     []string          `json:"supports,omitempty"`
	BrowserShare map[string]string `json:"browser_share,omitempty"`
	Attributes   map[string]any    `json:"attributes,omitempty"`
}

// ResourceObservation identifies a resource in one observed context. ID is
// adapter-local; the graph stores a digest rather than the raw identifier.
type ResourceObservation struct {
	ID, Kind, Context string
	Attributes        map[string]any
}

// RelationObservation connects two resources reported by the same adapter.
type RelationObservation struct{ From, To, Kind string }

// AliasObservation is a local user declaration that names one adapter context.
type AliasObservation struct {
	Space, Name, Adapter, Selection string
	Metadata                        map[string]string
}

var graphContextName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ +-]{0,127}$`)

func contextID(adapter, selection string) string {
	return "context/" + digest(adapter+"\x00"+selection)
}

// ObserveInventory replaces CTX's adapter inventory projection while leaving
// shell and web observations intact. It records only ordinary metadata and
// redacts context names that resemble paths, URLs, or opaque values.
func (g *Graph) ObserveInventory(ctx context.Context, adapters []AdapterObservation, aliases []AliasObservation) error {
	now := time.Now().UTC()
	host, _ := os.Hostname()
	vertices := map[string]graph.Vertex{
		"machine/local":     {ID: "machine/local", Kind: Namespace + "/machine", Attributes: map[string]any{"hostname": host, "os": runtime.GOOS, "architecture": runtime.GOARCH}, Provenance: graph.Provenance{Source: "ctx", Operation: "inventory-observed", At: now}},
		"inventory/current": {ID: "inventory/current", Kind: Namespace + "/inventory", Attributes: map[string]any{"observed_at": now.Format(time.RFC3339Nano)}, Provenance: graph.Provenance{Source: "ctx", Operation: "inventory-observed", At: now}},
	}
	edges := map[string]graph.Edge{}
	observed := map[string]bool{}
	for _, adapter := range adapters {
		if adapter.Name == "" {
			continue
		}
		adapterID := "adapter/" + adapter.Name
		capabilities := append([]string(nil), adapter.Capabilities...)
		supports := append([]string(nil), adapter.Supports...)
		surfaces := append([]string(nil), adapter.Surfaces...)
		sort.Strings(capabilities)
		sort.Strings(supports)
		sort.Strings(surfaces)
		vertices[adapterID] = graph.Vertex{ID: adapterID, Kind: Namespace + "/adapter", Attributes: map[string]any{"name": adapter.Name, "runtime": adapter.Runtime, "surfaces": surfaces, "supports": supports, "browser_share": adapter.BrowserShare, "selector": adapter.Selector, "trusted": adapter.Trusted, "list_supported": adapter.ListSupported, "observe_supported": adapter.ObserveSupported, "listed": adapter.Listed, "discovery_status": adapter.DiscoveryStatus}, Provenance: graph.Provenance{Source: "ctx", Operation: "adapter-inventory", At: now}}
		edgeID := "machine-adapter/" + adapter.Name
		edges[edgeID] = graph.Edge{ID: edgeID, From: "machine/local", To: adapterID, Type: Namespace + "/has-adapter"}
		for _, capability := range capabilities {
			capabilityID := "capability/" + digest(capability)
			vertices[capabilityID] = graph.Vertex{ID: capabilityID, Kind: Namespace + "/capability", Attributes: map[string]any{"name": capability}, Provenance: graph.Provenance{Source: "ctx", Operation: "adapter-declared", At: now}}
			edgeID := "adapter-capability/" + digest(adapter.Name+"\x00"+capability)
			edges[edgeID] = graph.Edge{ID: edgeID, From: adapterID, To: capabilityID, Type: Namespace + "/supports"}
		}
		for _, kind := range supports {
			supportID := "support/" + digest(kind)
			vertices[supportID] = graph.Vertex{ID: supportID, Kind: Namespace + "/support", Attributes: map[string]any{"name": kind}, Provenance: graph.Provenance{Source: "ctx", Operation: "adapter-declared", At: now}}
			edgeID := "adapter-support/" + digest(adapter.Name+"\x00"+kind)
			edges[edgeID] = graph.Edge{ID: edgeID, From: adapterID, To: supportID, Type: Namespace + "/supports-kind"}
		}
		if !adapter.Trusted || !adapter.Listed {
			continue
		}
		for _, context := range adapter.Contexts {
			selection := context.Selection
			if selection == "" {
				continue
			}
			id := contextID(adapter.Name, selection)
			attributes := map[string]any{"adapter": adapter.Name, "runtime": adapter.Runtime, "selection_digest": digest(selection)}
			if context.Capabilities != nil {
				attributes["capabilities"] = context.Capabilities
			}
			if context.Supports != nil {
				attributes["supports"] = context.Supports
			}
			if context.BrowserShare != nil {
				attributes["browser_share"] = context.BrowserShare
			}
			if len(context.Attributes) > 0 {
				attributes["metadata"] = context.Attributes
			}
			if graphContextName.MatchString(selection) {
				attributes["selection"] = selection
			} else {
				attributes["selection_redacted"] = true
			}
			vertices[id] = graph.Vertex{ID: id, Kind: Namespace + "/context", Attributes: attributes, Provenance: graph.Provenance{Source: adapter.Name, Operation: "adapter-list", At: now}}
			edgeID := "adapter-context/" + digest(adapter.Name+"\x00"+selection)
			edges[edgeID] = graph.Edge{ID: edgeID, From: adapterID, To: id, Type: Namespace + "/offers"}
			observed[id] = true
		}
		for _, resource := range adapter.Resources {
			if resource.ID == "" || resource.Kind == "" || !observed[contextID(adapter.Name, resource.Context)] {
				continue
			}
			resourceID := "resource/" + digest(adapter.Name+"\x00"+resource.ID)
			attributes := map[string]any{"adapter": adapter.Name, "kind": resource.Kind, "id_digest": digest(resource.ID)}
			if len(resource.Attributes) > 0 {
				attributes["metadata"] = resource.Attributes
			}
			vertices[resourceID] = graph.Vertex{ID: resourceID, Kind: Namespace + "/resource", Attributes: attributes, Provenance: graph.Provenance{Source: adapter.Name, Operation: "adapter-observe", At: now}}
			edgeID := "context-resource/" + digest(adapter.Name+"\x00"+resource.Context+"\x00"+resource.ID)
			edges[edgeID] = graph.Edge{ID: edgeID, From: contextID(adapter.Name, resource.Context), To: resourceID, Type: Namespace + "/contains"}
		}
		for _, relation := range adapter.Relations {
			from := "resource/" + digest(adapter.Name+"\x00"+relation.From)
			to := "resource/" + digest(adapter.Name+"\x00"+relation.To)
			if relation.Kind == "" || vertices[from].ID == "" || vertices[to].ID == "" {
				continue
			}
			edgeID := "resource-relation/" + digest(adapter.Name+"\x00"+relation.From+"\x00"+relation.To+"\x00"+relation.Kind)
			edges[edgeID] = graph.Edge{ID: edgeID, From: from, To: to, Type: Namespace + "/relates", Attributes: map[string]any{"kind": relation.Kind}}
		}
	}
	for _, alias := range aliases {
		if alias.Name == "" || alias.Space == "" {
			continue
		}
		aliasID := "alias/" + digest(alias.Space+"\x00"+alias.Name)
		id := contextID(alias.Adapter, alias.Selection)
		attributes := map[string]any{"space": alias.Space, "name": alias.Name, "adapter": alias.Adapter, "selection_digest": digest(alias.Selection), "discovered": observed[id]}
		if graphContextName.MatchString(alias.Selection) {
			attributes["selection"] = alias.Selection
		}
		if len(alias.Metadata) > 0 {
			attributes["metadata"] = alias.Metadata
		}
		vertices[aliasID] = graph.Vertex{ID: aliasID, Kind: Namespace + "/alias", Attributes: attributes, Provenance: graph.Provenance{Source: "ctx", Operation: "alias-declared", At: now}}
		if observed[id] {
			edgeID := "alias-context/" + digest(alias.Space+"\x00"+alias.Name)
			edges[edgeID] = graph.Edge{ID: edgeID, From: aliasID, To: id, Type: Namespace + "/resolves-to"}
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := g.Store.Snapshot(ctx, Namespace)
		if err != nil {
			return err
		}
		tx := graph.Transaction{Namespace: Namespace, ExpectedRevision: &snapshot.Revision, DeleteIncidentEdges: true}
		currentVertices := map[string]graph.Vertex{}
		currentEdges := map[string]graph.Edge{}
		for _, vertex := range snapshot.Vertices {
			currentVertices[vertex.ID] = vertex
			if isInventoryVertex(vertex) {
				if _, keep := vertices[vertex.ID]; !keep {
					tx.DeleteVertices = append(tx.DeleteVertices, graph.VertexRef{Namespace: Namespace, ID: vertex.ID})
				}
			}
		}
		for _, edge := range snapshot.Edges {
			currentEdges[edge.ID] = edge
			if isInventoryEdge(edge) {
				if _, keep := edges[edge.ID]; !keep {
					tx.DeleteEdges = append(tx.DeleteEdges, graph.EdgeRef{Namespace: Namespace, ID: edge.ID})
				}
			}
		}
		for id, vertex := range vertices {
			current, ok := currentVertices[id]
			if !ok || current.Kind != vertex.Kind || !sameGraphAttributes(current.Attributes, vertex.Attributes) {
				tx.Vertices = append(tx.Vertices, vertex)
			}
		}
		for id, edge := range edges {
			current, ok := currentEdges[id]
			if !ok || current.From != edge.From || current.To != edge.To || current.Type != edge.Type {
				tx.Edges = append(tx.Edges, edge)
			}
		}
		if len(tx.Vertices)+len(tx.Edges)+len(tx.DeleteVertices)+len(tx.DeleteEdges) == 0 {
			return nil
		}
		_, err = g.Store.Apply(ctx, tx)
		if errors.Is(err, graph.ErrConflict) {
			continue
		}
		return err
	}
	return fmt.Errorf("system inventory changed during observation")
}

func isInventoryVertex(vertex graph.Vertex) bool {
	switch vertex.Kind {
	case Namespace + "/inventory", Namespace + "/adapter", Namespace + "/capability", Namespace + "/support", Namespace + "/context", Namespace + "/resource", Namespace + "/alias":
		return true
	}
	return false
}

func isInventoryEdge(edge graph.Edge) bool {
	switch edge.Type {
	case Namespace + "/has-adapter", Namespace + "/supports", Namespace + "/supports-kind", Namespace + "/offers", Namespace + "/contains", Namespace + "/relates", Namespace + "/resolves-to":
		return true
	}
	return false
}

func sameGraphAttributes(a, b map[string]any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}
