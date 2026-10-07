// Package systemgraph projects CTX machine inventory, shell, and browser
// observations onto the public generic graph API. It owns the ctx.system
// vocabulary; the base graph package remains product-neutral.
package systemgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/webong/ext/pkg/graph"
)

const Namespace = "ctx.system"

type Graph struct{ Store graph.Store }

func Open(configHome string) (*Graph, error) {
	store, err := graph.OpenFile(filepath.Join(configHome, "graph.json"))
	if err != nil {
		return nil, err
	}
	projected, err := New(store)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return projected, nil
}

// New registers the CTX system vocabulary on any graph.Store. Other services
// may keep their own namespaces in the same store.
func New(store graph.Store) (*Graph, error) {
	if err := store.Register(graph.Schema{Namespace: Namespace, Version: "1", Validate: validateSchema}); err != nil {
		return nil, err
	}
	return &Graph{Store: store}, nil
}

func validateSchema(view graph.View, tx graph.Transaction) error {
	allowedKinds := map[string]bool{Namespace + "/machine": true, Namespace + "/shell-session": true, Namespace + "/directory": true, Namespace + "/project": true, Namespace + "/profile": true, Namespace + "/selection": true, Namespace + "/browser-context": true, Namespace + "/inventory": true, Namespace + "/adapter": true, Namespace + "/capability": true, Namespace + "/support": true, Namespace + "/context": true, Namespace + "/resource": true, Namespace + "/alias": true,
		Namespace + "/host-inventory": true, Namespace + "/shell": true, Namespace + "/filesystem": true, Namespace + "/webview": true, Namespace + "/process": true, Namespace + "/executable": true, Namespace + "/application": true, Namespace + "/process-resource": true}
	for _, vertex := range tx.Vertices {
		if !allowedKinds[vertex.Kind] {
			return fmt.Errorf("unsupported ctx.system kind %q", vertex.Kind)
		}
	}
	allowedRelations := map[string]struct{ from, to string }{
		Namespace + "/runs":                   {Namespace + "/machine", Namespace + "/process"},
		Namespace + "/parent-of":              {Namespace + "/process", Namespace + "/process"},
		Namespace + "/executes":               {Namespace + "/process", Namespace + "/executable"},
		Namespace + "/belongs-to-application": {Namespace + "/process", Namespace + "/application"},
		Namespace + "/uses-resource":          {Namespace + "/process", Namespace + "/process-resource"},
		Namespace + "/hosts":                  {Namespace + "/machine", Namespace + "/shell-session"},
		Namespace + "/has-shell":              {Namespace + "/machine", Namespace + "/shell"},
		Namespace + "/has-filesystem":         {Namespace + "/machine", Namespace + "/filesystem"},
		Namespace + "/has-webview":            {Namespace + "/machine", Namespace + "/webview"},
		Namespace + "/has-host-inventory":     {Namespace + "/machine", Namespace + "/host-inventory"},
		Namespace + "/working-in":             {Namespace + "/shell-session", Namespace + "/directory"},
		Namespace + "/within":                 {Namespace + "/directory", Namespace + "/project"},
		Namespace + "/uses-profile":           {Namespace + "/shell-session", Namespace + "/profile"},
		Namespace + "/selects":                {Namespace + "/shell-session", Namespace + "/selection"},
		Namespace + "/opened":                 {Namespace + "/shell-session", Namespace + "/browser-context"},
		Namespace + "/has-adapter":            {Namespace + "/machine", Namespace + "/adapter"},
		Namespace + "/supports":               {Namespace + "/adapter", Namespace + "/capability"},
		Namespace + "/supports-kind":          {Namespace + "/adapter", Namespace + "/support"},
		Namespace + "/offers":                 {Namespace + "/adapter", Namespace + "/context"},
		Namespace + "/contains":               {Namespace + "/context", Namespace + "/resource"},
		Namespace + "/relates":                {Namespace + "/resource", Namespace + "/resource"},
		Namespace + "/resolves-to":            {Namespace + "/alias", Namespace + "/context"},
	}
	for _, edge := range tx.Edges {
		expected, ok := allowedRelations[edge.Type]
		if !ok {
			return fmt.Errorf("unsupported ctx.system relationship %q", edge.Type)
		}
		from, fromOK := view.Vertex(Namespace, edge.From)
		to, toOK := view.Vertex(Namespace, edge.To)
		if !fromOK || !toOK {
			return graph.ErrDanglingEdge
		}
		if from.Kind != expected.from || to.Kind != expected.to {
			return fmt.Errorf("invalid endpoints for ctx.system relationship %q", edge.Type)
		}
	}
	return nil
}
func (g *Graph) Close() error { return g.Store.Close() }

// ObserveShell records the current shell session and its active directory.
// It never reads arbitrary environment values or shell history.
func (g *Graph) ObserveShell(ctx context.Context, cwd, profile string, selections map[string]string) error {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd, _ = filepath.Abs(cwd)
	project := projectRoot(cwd)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = os.Getenv("ComSpec")
	}
	shell = filepath.Base(shell)
	terminal := os.Getenv("TERM_PROGRAM")
	parent := os.Getppid()
	sessionID := "shell/" + itoa(parent)
	host, _ := os.Hostname()
	now := time.Now().UTC()
	vertices := []graph.Vertex{
		{ID: "machine/local", Kind: Namespace + "/machine", Attributes: map[string]any{"hostname": host, "os": runtime.GOOS, "architecture": runtime.GOARCH}, Provenance: graph.Provenance{Source: "ctx", Operation: "shell-observed", At: now}},
		{ID: sessionID, Kind: Namespace + "/shell-session", Attributes: map[string]any{"parent_pid": float64(parent), "shell": shell, "terminal": terminal, "working_directory": cwd, "active_profile": profile, "observed_at": now.Format(time.RFC3339Nano)}, Provenance: graph.Provenance{Source: "ctx", Operation: "shell-observed", At: now}},
		{ID: "directory/" + digest(cwd), Kind: Namespace + "/directory", Attributes: map[string]any{"path": cwd}, Provenance: graph.Provenance{Source: "ctx", Operation: "directory-observed", At: now}},
		{ID: "project/" + digest(project), Kind: Namespace + "/project", Attributes: map[string]any{"path": project}, Provenance: graph.Provenance{Source: "ctx", Operation: "project-observed", At: now}},
	}
	edges := []graph.Edge{
		{ID: "machine-shell/" + itoa(parent), From: "machine/local", To: sessionID, Type: Namespace + "/hosts"},
		{ID: "shell-directory/" + itoa(parent), From: sessionID, To: "directory/" + digest(cwd), Type: Namespace + "/working-in"},
		{ID: "directory-project/" + digest(cwd), From: "directory/" + digest(cwd), To: "project/" + digest(project), Type: Namespace + "/within"},
	}
	if profile != "" {
		vertices = append(vertices, graph.Vertex{ID: "profile/" + digest(profile), Kind: Namespace + "/profile", Attributes: map[string]any{"name": profile}, Provenance: graph.Provenance{Source: "ctx", Operation: "profile-observed", At: now}})
		edges = append(edges, graph.Edge{ID: "shell-profile/" + itoa(parent), From: sessionID, To: "profile/" + digest(profile), Type: Namespace + "/uses-profile"})
	}

	selectionEdges := []graph.Edge{}
	for key, value := range selections {
		if key == "" || key == "profile" || value == "" {
			continue
		}
		selectionID := "selection/" + digest(key+"\x00"+value)
		attributes := map[string]any{"key": key, "value_digest": digest(value)}
		if graphContextName.MatchString(value) {
			attributes["value"] = value
		} else {
			attributes["value_redacted"] = true
		}
		vertices = append(vertices, graph.Vertex{ID: selectionID, Kind: Namespace + "/selection", Attributes: attributes, Provenance: graph.Provenance{Source: "ctx", Operation: "selection-observed", At: now}})
		selectionEdges = append(selectionEdges, graph.Edge{ID: "shell-selection/" + itoa(parent) + "/" + digest(key), From: sessionID, To: selectionID, Type: Namespace + "/selects"})
	}
	edges = append(edges, selectionEdges...)
	tx := graph.Transaction{Namespace: Namespace, Vertices: vertices, Edges: edges}
	if profile == "" {
		if _, err := g.Store.GetEdge(ctx, Namespace, "shell-profile/"+itoa(parent)); err == nil {
			tx.DeleteEdges = []graph.EdgeRef{{Namespace: Namespace, ID: "shell-profile/" + itoa(parent)}}
		}
	}
	oldSelections, err := g.Store.QueryEdges(ctx, graph.EdgeQuery{Namespace: Namespace, Type: Namespace + "/selects", From: sessionID, Limit: 256})
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, edge := range selectionEdges {
		current[edge.ID] = true
	}
	for _, edge := range oldSelections {
		if !current[edge.ID] {
			tx.DeleteEdges = append(tx.DeleteEdges, graph.EdgeRef{Namespace: Namespace, ID: edge.ID})
		}
	}
	if existing, err := g.Store.GetVertex(ctx, Namespace, sessionID); err == nil && equalAttributes(existing.Attributes, vertices[1].Attributes) {
		unchanged := len(oldSelections) == len(selectionEdges)
		if unchanged {
			for _, edge := range selectionEdges {
				found := false
				for _, old := range oldSelections {
					if old.ID == edge.ID && old.To == edge.To {
						found = true
						break
					}
				}
				if !found {
					unchanged = false
					break
				}
			}
		}
		if unchanged {
			profileCurrent := profile != ""
			oldProfile, profileErr := g.Store.GetEdge(ctx, Namespace, "shell-profile/"+itoa(parent))
			if (profile == "" && profileErr != nil) || (profileCurrent && profileErr == nil && oldProfile.To == "profile/"+digest(profile)) {
				return nil
			}
		}
	}
	_, err = g.Store.Apply(ctx, tx)
	return err
}

// ObserveWeb records the current web origin after ctx open succeeds. URL paths,
// queries, fragments, and credentials are intentionally not persisted.
func (g *Graph) ObserveWeb(ctx context.Context, provider, profile, scheme, host string) error {
	if host == "" {
		return nil
	}
	now := time.Now().UTC()
	id := "browser/current"
	vertices := []graph.Vertex{{ID: id, Kind: Namespace + "/browser-context", Attributes: map[string]any{"provider": provider, "profile": profile, "scheme": scheme, "host": host, "observed_at": now.Format(time.RFC3339Nano)}, Provenance: graph.Provenance{Source: "ctx", Operation: "web-origin-observed", At: now}}}
	parent := os.Getppid()
	edge := graph.Edge{ID: "shell-browser/" + itoa(parent), From: "shell/" + itoa(parent), To: id, Type: Namespace + "/opened"}
	if existing, err := g.Store.GetVertex(ctx, Namespace, id); err == nil && equalAttributes(existing.Attributes, vertices[0].Attributes) {
		if old, edgeErr := g.Store.GetEdge(ctx, Namespace, edge.ID); edgeErr == nil && old.From == edge.From && old.To == edge.To {
			return nil
		}
	}
	_, err := g.Store.Apply(ctx, graph.Transaction{Namespace: Namespace, Vertices: vertices, Edges: []graph.Edge{edge}})
	return err
}

func projectRoot(cwd string) string {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		for _, marker := range []string{".git", ".ctx", "go.mod", "package.json"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
	}
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buf [24]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
func equalAttributes(a, b map[string]any) bool {
	for key, value := range b {
		if key == "observed_at" {
			continue
		}
		if a[key] != value {
			return false
		}
	}
	for key := range a {
		if key != "observed_at" {
			if _, ok := b[key]; !ok {
				return false
			}
		}
	}
	return true
}

// SafeOrigin parses a navigation URL and returns only scheme and host.
func SafeOrigin(raw string) (scheme, host string, ok bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", "", false
	}
	scheme = strings.ToLower(parsed.Scheme)
	host = strings.ToLower(parsed.Hostname())
	if parsed.Port() != "" {
		host += ":" + parsed.Port()
	}
	return scheme, host, true
}
