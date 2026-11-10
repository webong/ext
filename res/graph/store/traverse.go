package store

import (
	"context"
	"fmt"
)

// Traversal is what Traverse reads from one namespace.
type Traversal interface {
	Vertex(ctx context.Context, id string) (Vertex, bool, error)
	// Outgoing returns the edges whose From is id, ordered by ID.
	Outgoing(ctx context.Context, id string) ([]Edge, error)
}

// Traverse walks outgoing edges breadth first from start. It visits at most
// maxVertices vertices, including start, and follows paths of at most maxDepth
// edges. Each vertex is reached once, by the first path found; edges are taken
// in ID order. It returns one path per reached vertex other than start, in
// visiting order.
func Traverse(ctx context.Context, t Traversal, start string, maxDepth, maxVertices int) ([]Path, error) {
	if _, ok, err := t.Vertex(ctx, start); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: vertex %q", ErrNotFound, start)
	}
	type step struct {
		ids   []string
		edges []Edge
	}
	queue := []step{{ids: []string{start}}}
	visited := map[string]bool{start: true}
	paths := []Path{}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cur := queue[0]
		queue = queue[1:]
		if len(cur.ids) > 1 {
			path := Path{Vertices: make([]Vertex, 0, len(cur.ids)), Edges: make([]Edge, 0, len(cur.edges))}
			for _, id := range cur.ids {
				v, _, err := t.Vertex(ctx, id)
				if err != nil {
					return nil, err
				}
				path.Vertices = append(path.Vertices, cloneVertex(v))
			}
			for _, e := range cur.edges {
				path.Edges = append(path.Edges, cloneEdge(e))
			}
			paths = append(paths, path)
		}
		if len(cur.edges) >= maxDepth || len(visited) >= maxVertices {
			continue
		}
		out, err := t.Outgoing(ctx, cur.ids[len(cur.ids)-1])
		if err != nil {
			return nil, err
		}
		for _, e := range out {
			if len(visited) >= maxVertices {
				break
			}
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			queue = append(queue, step{
				ids:   append(append([]string(nil), cur.ids...), e.To),
				edges: append(append([]Edge(nil), cur.edges...), e),
			})
		}
	}
	return paths, nil
}
