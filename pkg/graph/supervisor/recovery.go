package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/webong/ext/pkg/graph"
)

func activeState(state State) bool {
	switch state {
	case StateStarting, StateRunning, StateReady, StateStopping, StateRestarting, StateOrphaned:
		return true
	default:
		return false
	}
}

func (s *Supervisor) recoverOrphans(ctx context.Context) error {
	snapshot, err := s.graph.Snapshot(ctx, RuntimeNamespace)
	if err != nil {
		return err
	}
	for _, vertex := range snapshot.Vertices {
		if vertex.Kind != RuntimeNamespace+"/process-instance" || vertex.Attributes["runtime_id"] != s.runtimeID {
			continue
		}
		state, _ := vertex.Attributes["state"].(string)
		if !activeState(State(state)) {
			continue
		}
		id := strings.TrimPrefix(vertex.ID, "process/")
		pid := intAttribute(vertex.Attributes["pid"])
		identity, _ := vertex.Attributes["process_identity"].(string)
		alive := pid > 0 && processAlive(pid)
		if raw, ok := vertex.Attributes["lease_until"].(string); ok {
			if until, err := time.Parse(time.RFC3339Nano, raw); err == nil && time.Now().Before(until) && state != string(StateOrphaned) {
				return ErrRuntimeLeased
			}
		}
		if !alive {
			if err := s.markRecovered(ctx, vertex, StateStopped, false); err != nil {
				return err
			}
			continue
		}
		if s.orphanPolicy == OrphanTerminate && identity != "" && vertex.Attributes["isolated"] == true {
			if err := terminateOrphan(pid, identity); err == nil {
				if err := s.markRecovered(ctx, vertex, StateStopped, false); err != nil {
					return err
				}
				continue
			}
		}
		if err := s.markRecovered(ctx, vertex, StateOrphaned, true); err != nil {
			return err
		}
		s.orphans[id] = Orphan{ID: id, PID: pid, ProcessIdentity: identity, Alive: true}
	}
	return s.pruneTerminals(ctx)
}

func intAttribute(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func (s *Supervisor) markRecovered(ctx context.Context, vertex graph.Vertex, state State, alive bool) error {
	vertex.Attributes["state"] = string(state)
	vertex.Attributes["health"] = "unhealthy"
	vertex.Attributes["supervisor_owner"] = s.ownerID
	delete(vertex.Attributes, "lease_until")
	if !alive {
		delete(vertex.Attributes, "pid")
		delete(vertex.Attributes, "process_identity")
	}
	_, err := s.graph.Apply(ctx, graph.Transaction{Namespace: RuntimeNamespace, Vertices: []graph.Vertex{vertex}})
	return err
}

// Orphans returns processes left by an expired supervisor lease. While any
// live orphan remains unresolved, Start refuses to create another process.
func (s *Supervisor) Orphans() []Orphan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Orphan, 0, len(s.orphans))
	for _, orphan := range s.orphans {
		out = append(out, orphan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ResolveOrphan terminates an identity-matched orphan process tree and clears
// its durable orphan state. It never targets a reused PID.
func (s *Supervisor) ResolveOrphan(ctx context.Context, id string) error {
	s.mu.RLock()
	orphan, ok := s.orphans[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("orphan %s not found", id)
	}
	if orphan.Alive && processAlive(orphan.PID) {
		if orphan.ProcessIdentity == "" {
			return errors.New("orphan process identity is unavailable")
		}
		if err := terminateOrphan(orphan.PID, orphan.ProcessIdentity); err != nil {
			return err
		}
	}
	vertex, err := s.graph.GetVertex(ctx, RuntimeNamespace, "process/"+id)
	if err != nil {
		return err
	}
	if vertex.Attributes["runtime_id"] != s.runtimeID || vertex.Attributes["state"] != string(StateOrphaned) {
		return errors.New("orphan state changed")
	}
	if err := s.markRecovered(ctx, vertex, StateStopped, false); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.orphans, id)
	s.mu.Unlock()
	return s.pruneTerminals(ctx)
}

func (s *Supervisor) pruneTerminals(ctx context.Context) error {
	snapshot, err := s.graph.Snapshot(ctx, RuntimeNamespace)
	if err != nil {
		return err
	}
	terminal := []graph.Vertex{}
	for _, vertex := range snapshot.Vertices {
		if vertex.Kind != RuntimeNamespace+"/process-instance" || vertex.Attributes["runtime_id"] != s.runtimeID {
			continue
		}
		state, _ := vertex.Attributes["state"].(string)
		if state == string(StateStopped) || state == string(StateCrashed) {
			terminal = append(terminal, vertex)
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].UpdatedAt.Before(terminal[j].UpdatedAt) })
	remove := len(terminal) - s.retainTerminal
	if remove <= 0 {
		return nil
	}
	deleted := map[string]bool{}
	tx := graph.Transaction{Namespace: RuntimeNamespace, ExpectedRevision: &snapshot.Revision, DeleteIncidentEdges: true}
	for _, vertex := range terminal[:remove] {
		deleted[vertex.ID] = true
		tx.DeleteVertices = append(tx.DeleteVertices, graph.VertexRef{Namespace: RuntimeNamespace, ID: vertex.ID})
	}
	for _, vertex := range snapshot.Vertices {
		if vertex.Kind != RuntimeNamespace+"/artifact" && vertex.Kind != RuntimeNamespace+"/endpoint" && vertex.Kind != RuntimeNamespace+"/component" {
			continue
		}
		if vertex.Provenance.Source != "ext.supervisor" {
			continue
		}
		used := false
		for _, edge := range snapshot.Edges {
			if (edge.From == vertex.ID || edge.To == vertex.ID) && !deleted[edge.From] && !deleted[edge.To] {
				used = true
				break
			}
		}
		if !used {
			tx.DeleteVertices = append(tx.DeleteVertices, graph.VertexRef{Namespace: RuntimeNamespace, ID: vertex.ID})
		}
	}
	_, err = s.graph.Apply(ctx, tx)
	if errors.Is(err, graph.ErrConflict) || errors.Is(err, graph.ErrNotFound) {
		return nil // Another writer will cause the next terminal transition to retry.
	}
	return err
}
