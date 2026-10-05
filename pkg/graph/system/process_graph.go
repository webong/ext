package systemgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/webong/ctx/pkg/graph"
)

// ScanProcesses discovers and reconciles the caller-visible process inventory.
func (g *Graph) ScanProcesses(ctx context.Context, options ProcessOptions) (ProcessSnapshot, error) {
	snapshot, err := DiscoverProcesses(ctx, options)
	if err != nil {
		return snapshot, err
	}
	return snapshot, g.ObserveProcesses(ctx, snapshot)
}

// ObserveProcesses projects generic OS evidence. Complete enumeration removes
// missing process instances. Partial inventories retain unseen instances. A
// targeted observation only replaces that PID and its inspected resource kinds.
// Resource nodes retain their own timestamps across later identity-only scans.
func (g *Graph) ObserveProcesses(ctx context.Context, observation ProcessSnapshot) error {
	if observation.HostID == "" || observation.ObservedAt.IsZero() {
		return errors.New("process observation requires host identity and time")
	}
	if observation.Scope != "all" && observation.Scope != "process" {
		return errors.New("process observation scope must be all or process")
	}
	if observation.Scope == "process" && len(observation.Processes) != 1 {
		return errors.New("targeted process observation requires exactly one process")
	}
	if !validCollectionState(observation.Enumeration.State) {
		return errors.New("invalid process enumeration status")
	}
	seen := map[int]bool{}
	for _, p := range observation.Processes {
		if p.PID <= 0 || uint64(p.PID) > 1<<31-1 || seen[p.PID] {
			return errors.New("process observation requires unique positive PIDs")
		}
		seen[p.PID] = true
		for _, status := range p.Coverage {
			if !validCollectionState(status.State) {
				return errors.New("invalid process coverage status")
			}
		}
		for _, r := range p.Resources {
			if !processResourceKind(r.Kind) {
				return fmt.Errorf("invalid process resource kind %q", r.Kind)
			}
			if _, ok := p.Coverage[r.Kind]; !ok {
				return fmt.Errorf("resource %s requires coverage", r.Kind)
			}
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := g.Store.Snapshot(ctx, Namespace)
		if err != nil {
			return err
		}
		tx, err := processTransaction(snapshot, observation)
		if err != nil {
			return err
		}
		_, err = g.Store.Apply(ctx, tx)
		if errors.Is(err, graph.ErrConflict) {
			continue
		}
		return err
	}
	return errors.New("process inventory changed during observation")
}
func validCollectionState(state string) bool {
	switch state {
	case "complete", "partial", "permission-denied", "unsupported", "exited", "unavailable":
		return true
	}
	return false
}
func processResourceKind(kind string) bool {
	switch kind {
	case "file", "mapping", "socket", "ipc":
		return true
	}
	return false
}

func processTransaction(snapshot graph.Snapshot, observation ProcessSnapshot) (graph.Transaction, error) {
	tx := graph.Transaction{Namespace: Namespace, ExpectedRevision: &snapshot.Revision, DeleteIncidentEdges: true}
	vertices := map[string]graph.Vertex{}
	edges := map[string]graph.Edge{}
	oldVertices := map[string]graph.Vertex{}
	oldEdges := map[string]graph.Edge{}
	edgesByProcess := map[string][]graph.Edge{}
	resourcesByProcess := map[string][]graph.Vertex{}
	for _, v := range snapshot.Vertices {
		oldVertices[v.ID] = v
		if owner := v.Labels["process_id"]; owner != "" {
			resourcesByProcess[owner] = append(resourcesByProcess[owner], v)
		}
	}
	for _, e := range snapshot.Edges {
		oldEdges[e.ID] = e
		if e.Type == Namespace+"/parent-of" {
			edgesByProcess[e.To] = append(edgesByProcess[e.To], e)
		} else if e.Type == Namespace+"/executes" || e.Type == Namespace+"/belongs-to-application" {
			edgesByProcess[e.From] = append(edgesByProcess[e.From], e)
		}
	}
	provenance := graph.Provenance{Source: "host-process", Operation: "process-observed", At: observation.ObservedAt}
	addVertex := func(id, kind string, attrs map[string]any, owner string) {
		labels := map[string]string{"collector": "process", "host_id": observation.HostID}
		if owner != "" {
			labels["process_id"] = owner
		}
		vertices[id] = graph.Vertex{ID: id, Kind: Namespace + "/" + kind, Attributes: attrs, Labels: labels, Provenance: provenance}
	}
	addEdge := func(from, to, relation string) {
		id := "process-edge/" + digest(from+"\x00"+relation+"\x00"+to)
		edges[id] = graph.Edge{ID: id, From: from, To: to, Type: Namespace + "/" + relation, Provenance: provenance}
	}
	// Keep the machine's existing attributes and provenance.
	if _, ok := oldVertices["machine/local"]; !ok {
		hostname, _ := os.Hostname()
		vertices["machine/local"] = graph.Vertex{ID: "machine/local", Kind: Namespace + "/machine", Attributes: map[string]any{"hostname": hostname, "os": runtime.GOOS, "architecture": runtime.GOARCH}, Provenance: provenance}
	}
	marker := "host-inventory/processes"
	if old, ok := oldVertices[marker]; ok && observation.ObservedAt.Before(old.Provenance.At) {
		return tx, errors.New("process observation predates the stored inventory")
	}
	if observation.Scope == "all" {
		attrs, err := processAttributes(struct {
			HostID      string           `json:"host_id"`
			Enumeration CollectionStatus `json:"enumeration"`
			Count       int              `json:"count"`
			ObservedAt  time.Time        `json:"observed_at"`
		}{observation.HostID, observation.Enumeration, len(observation.Processes), observation.ObservedAt})
		if err != nil {
			return tx, err
		}
		attrs["kind"] = "process"
		unidentified := []any{}
		for _, p := range observation.Processes {
			if p.StartID != "" {
				continue
			}
			p.Resources = nil
			details, err := processAttributes(p)
			if err != nil {
				return tx, err
			}
			unidentified = append(unidentified, details)
		}
		if len(unidentified) > 0 {
			attrs["unidentified_processes"] = unidentified
		}
		addVertex(marker, "host-inventory", attrs, "")
		addEdge("machine/local", marker, "has-host-inventory")
	}
	ids := map[int]string{}
	reported := map[int]ProcessInfo{}
	for _, p := range observation.Processes {
		reported[p.PID] = p
		if p.StartID != "" {
			ids[p.PID] = processInstanceID(observation.HostID, p)
		}
	}
	remove := map[string]bool{}
	// Missing start identities still count as seen: lack of permission must never
	// be interpreted as proof that an older known instance has exited.
	for _, v := range snapshot.Vertices {
		if v.Kind != Namespace+"/process" {
			continue
		}
		pidNumber, _ := v.Attributes["pid"].(float64)
		pid := int(pidNumber)
		incoming, seen := reported[pid]
		sameHost := v.Labels["host_id"] == observation.HostID
		replace := sameHost && seen && incoming.StartID != "" && ids[pid] != v.ID
		absent := observation.Scope == "all" && observation.Enumeration.State == "complete" && (!sameHost || !seen)
		if replace || absent {
			if observation.ObservedAt.Before(v.Provenance.At) {
				return tx, errors.New("process observation predates a stored process")
			}
			remove[v.ID] = true
		}
	}
	for _, p := range observation.Processes {
		id := ids[p.PID]
		if id == "" {
			continue
		}
		if old, ok := oldVertices[id]; ok && observation.ObservedAt.Before(old.Provenance.At) {
			return tx, errors.New("process observation predates a stored process")
		}
		copy := p
		copy.Resources = nil
		attrs, err := processAttributes(copy)
		if err != nil {
			return tx, err
		}
		// Identity-only scans preserve dated resource coverage and usage alongside
		// retained resource vertices, rather than presenting old details as fresh.
		if old, ok := oldVertices[id]; ok {
			previous, _ := old.Attributes["coverage"].(map[string]any)
			coverage, _ := attrs["coverage"].(map[string]any)
			if coverage == nil {
				coverage = map[string]any{}
				attrs["coverage"] = coverage
			}
			for category, status := range previous {
				if !processResourceKind(category) && category != "usage" {
					continue
				}
				if _, refreshed := p.Coverage[category]; !refreshed {
					coverage[category] = status
					if category == "usage" {
						if usage, exists := old.Attributes["usage"]; exists {
							attrs["usage"] = usage
						}
					}
				}
			}
		}
		attrs["host_id"] = observation.HostID
		attrs["observed_at"] = observation.ObservedAt.Format(time.RFC3339Nano)
		addVertex(id, "process", attrs, "")
		addEdge("machine/local", id, "runs")
		// Each identity refresh replaces parent and executable/application relations.
		for _, e := range edgesByProcess[id] {
			// A targeted refresh cannot verify the parent's instance. Retain the
			// existing relation as dated evidence only while its PID still agrees.
			if observation.Scope == "process" && e.Type == Namespace+"/parent-of" {
				if parent, ok := oldVertices[e.From]; ok && parent.Labels["host_id"] == observation.HostID {
					if number, ok := parent.Attributes["pid"].(float64); ok && int(number) == p.ParentPID {
						continue
					}
				}
			}
			delete(oldEdges, e.ID)
		}
		if parent := ids[p.ParentPID]; parent != "" && parent != id {
			addEdge(parent, id, "parent-of")
		}
		if p.Executable != "" {
			exe := "process-executable/" + digest(observation.HostID+"\x00"+p.Executable)
			addVertex(exe, "executable", map[string]any{"path": p.Executable}, "")
			addEdge(id, exe, "executes")
		}
		if p.Application != nil && p.Application.Path != "" {
			app := "process-application/" + digest(observation.HostID+"\x00"+p.Application.Path)
			addVertex(app, "application", map[string]any{"name": p.Application.Name, "path": p.Application.Path}, "")
			addEdge(id, app, "belongs-to-application")
		}
		for _, v := range resourcesByProcess[id] {
			kind, _ := v.Attributes["kind"].(string)
			if _, collected := p.Coverage[kind]; collected {
				remove[v.ID] = true
			}
		}
		for _, r := range p.Resources {
			attrs, err := processAttributes(r)
			if err != nil {
				return tx, err
			}
			attrs["observed_at"] = observation.ObservedAt.Format(time.RFC3339Nano)
			// Hash the full evidence so fd reuse or multiple sockets without descriptors
			// do not collapse into one resource. Duplicate evidence is harmless.
			encoded, err := json.Marshal(r)
			if err != nil {
				return tx, err
			}
			resourceID := "process-resource/" + digest(id+string(encoded))
			addVertex(resourceID, "process-resource", attrs, id)
			addEdge(id, resourceID, "uses-resource")
			delete(remove, resourceID)
		}
	}
	for _, v := range snapshot.Vertices {
		if remove[v.Labels["process_id"]] {
			remove[v.ID] = true
		}
	}
	for key, e := range oldEdges {
		if remove[e.From] || remove[e.To] {
			delete(oldEdges, key)
		}
	}
	for id, e := range edges {
		oldEdges[id] = e
	}
	// Collect orphaned executable/application nodes owned by this collector.
	referenced := map[string]bool{}
	for _, e := range oldEdges {
		referenced[e.From] = true
		referenced[e.To] = true
	}
	for _, v := range snapshot.Vertices {
		if v.Labels["collector"] == "process" && (v.Kind == Namespace+"/application" || v.Kind == Namespace+"/executable") && !referenced[v.ID] {
			remove[v.ID] = true
		}
	}
	for id := range remove {
		tx.DeleteVertices = append(tx.DeleteVertices, graph.VertexRef{Namespace: Namespace, ID: id})
	}
	for _, e := range snapshot.Edges {
		if _, keep := oldEdges[e.ID]; !keep {
			tx.DeleteEdges = append(tx.DeleteEdges, graph.EdgeRef{Namespace: Namespace, ID: e.ID})
		}
	}
	for _, v := range vertices {
		tx.Vertices = append(tx.Vertices, v)
	}
	for _, e := range edges {
		tx.Edges = append(tx.Edges, e)
	}
	return tx, nil
}
func processAttributes(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var attrs map[string]any
	err = json.Unmarshal(data, &attrs)
	return attrs, err
}
