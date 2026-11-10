// Package graph projects CTX machine inventory, shell, and browser
// observations onto the public generic graph API. It owns the ext.system
// vocabulary; the store package remains product-neutral.
package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/webong/ext/res/graph/store"
	"github.com/webong/ext/res/graph/store/sqlite"
)

const Namespace = "ext.system"

type Graph struct{ Store store.Store }

// Open opens the ext.system graph in configHome/graph.db, the SQLite store.
func Open(configHome string) (*Graph, error) {
	backing, err := sqlite.Open(context.Background(), filepath.Join(configHome, "graph.db"), store.Options{})
	if err != nil {
		return nil, err
	}
	projected, err := New(backing)
	if err != nil {
		_ = backing.Close()
		return nil, err
	}
	return projected, nil
}

// New registers the CTX system vocabulary on any store.Store. Other services
// may keep their own namespaces in the same store.
func New(backing store.Store) (*Graph, error) {
	if err := backing.Register(context.Background(), Schema()); err != nil {
		return nil, err
	}
	return &Graph{Store: backing}, nil
}

// SchemaVersion changes whenever the kinds or relationships of Schema change.
const SchemaVersion = "1"

// Schema is the declarative ext.system vocabulary: every vertex kind, and
// every relationship with the kinds of its endpoints.
func Schema() store.Schema {
	return store.Schema{Namespace: Namespace, Version: SchemaVersion, Kinds: append([]string(nil), systemKinds...), Relationships: append([]store.Relationship(nil), systemRelationships...)}
}

var systemKinds = []string{
	Namespace + "/machine",
	Namespace + "/shell-session",
	Namespace + "/directory",
	Namespace + "/project",
	Namespace + "/profile",
	Namespace + "/selection",
	Namespace + "/browser-context",
	Namespace + "/inventory",
	Namespace + "/adapter",
	Namespace + "/capability",
	Namespace + "/support",
	Namespace + "/context",
	Namespace + "/resource",
	Namespace + "/alias",
	Namespace + "/program",
	Namespace + "/installer",
	Namespace + "/host-inventory",
	Namespace + "/shell",
	Namespace + "/filesystem",
	Namespace + "/webview",
	Namespace + "/desktop",
	Namespace + "/display",
	Namespace + "/window",
	Namespace + "/accessibility-element",
	Namespace + "/permission",
	Namespace + "/process",
	Namespace + "/executable",
	Namespace + "/application",
	Namespace + "/process-resource",
	Namespace + "/host-resource",
}

var systemRelationships = []store.Relationship{
	{Type: Namespace + "/runs", From: Namespace + "/machine", To: Namespace + "/process"},
	{Type: Namespace + "/parent-of", From: Namespace + "/process", To: Namespace + "/process"},
	{Type: Namespace + "/executes", From: Namespace + "/process", To: Namespace + "/executable"},
	{Type: Namespace + "/belongs-to-application", From: Namespace + "/process", To: Namespace + "/application"},
	{Type: Namespace + "/uses-resource", From: Namespace + "/process", To: Namespace + "/process-resource"},
	{Type: Namespace + "/hosts", From: Namespace + "/machine", To: Namespace + "/shell-session"},
	{Type: Namespace + "/has-shell", From: Namespace + "/machine", To: Namespace + "/shell"},
	{Type: Namespace + "/has-filesystem", From: Namespace + "/machine", To: Namespace + "/filesystem"},
	{Type: Namespace + "/has-webview", From: Namespace + "/machine", To: Namespace + "/webview"},
	{Type: Namespace + "/has-desktop", From: Namespace + "/machine", To: Namespace + "/desktop"},
	{Type: Namespace + "/has-display", From: Namespace + "/machine", To: Namespace + "/display"},
	{Type: Namespace + "/has-window", From: Namespace + "/machine", To: Namespace + "/window"},
	{Type: Namespace + "/has-accessibility-element", From: Namespace + "/machine", To: Namespace + "/accessibility-element"},
	{Type: Namespace + "/has-permission", From: Namespace + "/machine", To: Namespace + "/permission"},
	{Type: Namespace + "/has-host-inventory", From: Namespace + "/machine", To: Namespace + "/host-inventory"},
	{Type: Namespace + "/has-host-resource", From: Namespace + "/machine", To: Namespace + "/host-resource"},
	{Type: Namespace + "/working-in", From: Namespace + "/shell-session", To: Namespace + "/directory"},
	{Type: Namespace + "/within", From: Namespace + "/directory", To: Namespace + "/project"},
	{Type: Namespace + "/uses-profile", From: Namespace + "/shell-session", To: Namespace + "/profile"},
	{Type: Namespace + "/selects", From: Namespace + "/shell-session", To: Namespace + "/selection"},
	{Type: Namespace + "/opened", From: Namespace + "/shell-session", To: Namespace + "/browser-context"},
	{Type: Namespace + "/has-adapter", From: Namespace + "/machine", To: Namespace + "/adapter"},
	{Type: Namespace + "/supports", From: Namespace + "/adapter", To: Namespace + "/capability"},
	{Type: Namespace + "/supports-kind", From: Namespace + "/adapter", To: Namespace + "/support"},
	{Type: Namespace + "/offers", From: Namespace + "/adapter", To: Namespace + "/context"},
	{Type: Namespace + "/contains", From: Namespace + "/context", To: Namespace + "/resource"},
	{Type: Namespace + "/relates", From: Namespace + "/resource", To: Namespace + "/resource"},
	{Type: Namespace + "/resolves-to", From: Namespace + "/alias", To: Namespace + "/context"},
	{Type: Namespace + "/has-program", From: Namespace + "/machine", To: Namespace + "/program"},
	{Type: Namespace + "/installed-by", From: Namespace + "/program", To: Namespace + "/installer"},
	{Type: Namespace + "/implemented-by", From: Namespace + "/installer", To: Namespace + "/adapter"},
	{Type: Namespace + "/uses-program", From: Namespace + "/context", To: Namespace + "/program"},
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
	vertices := []store.Vertex{
		{ID: "machine/local", Kind: Namespace + "/machine", Attributes: map[string]any{"hostname": host, "os": runtime.GOOS, "architecture": runtime.GOARCH}, Provenance: store.Provenance{Source: "ctx", Operation: "shell-observed", At: now}},
		{ID: sessionID, Kind: Namespace + "/shell-session", Attributes: map[string]any{"parent_pid": float64(parent), "shell": shell, "terminal": terminal, "working_directory": cwd, "active_profile": profile, "observed_at": now.Format(time.RFC3339Nano)}, Provenance: store.Provenance{Source: "ctx", Operation: "shell-observed", At: now}},
		{ID: "directory/" + digest(cwd), Kind: Namespace + "/directory", Attributes: map[string]any{"path": cwd}, Provenance: store.Provenance{Source: "ctx", Operation: "directory-observed", At: now}},
		{ID: "project/" + digest(project), Kind: Namespace + "/project", Attributes: map[string]any{"path": project}, Provenance: store.Provenance{Source: "ctx", Operation: "project-observed", At: now}},
	}
	edges := []store.Edge{
		{ID: "machine-shell/" + itoa(parent), From: "machine/local", To: sessionID, Type: Namespace + "/hosts"},
		{ID: "shell-directory/" + itoa(parent), From: sessionID, To: "directory/" + digest(cwd), Type: Namespace + "/working-in"},
		{ID: "directory-project/" + digest(cwd), From: "directory/" + digest(cwd), To: "project/" + digest(project), Type: Namespace + "/within"},
	}
	if profile != "" {
		vertices = append(vertices, store.Vertex{ID: "profile/" + digest(profile), Kind: Namespace + "/profile", Attributes: map[string]any{"name": profile}, Provenance: store.Provenance{Source: "ctx", Operation: "profile-observed", At: now}})
		edges = append(edges, store.Edge{ID: "shell-profile/" + itoa(parent), From: sessionID, To: "profile/" + digest(profile), Type: Namespace + "/uses-profile"})
	}

	selectionEdges := []store.Edge{}
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
		vertices = append(vertices, store.Vertex{ID: selectionID, Kind: Namespace + "/selection", Attributes: attributes, Provenance: store.Provenance{Source: "ctx", Operation: "selection-observed", At: now}})
		selectionEdges = append(selectionEdges, store.Edge{ID: "shell-selection/" + itoa(parent) + "/" + digest(key), From: sessionID, To: selectionID, Type: Namespace + "/selects"})
	}
	edges = append(edges, selectionEdges...)
	tx := store.Transaction{Namespace: Namespace, Vertices: vertices, Edges: edges}
	if profile == "" {
		if _, err := g.Store.GetEdge(ctx, Namespace, "shell-profile/"+itoa(parent)); err == nil {
			tx.DeleteEdges = []store.EdgeRef{{Namespace: Namespace, ID: "shell-profile/" + itoa(parent)}}
		}
	}
	oldSelections, err := g.Store.QueryEdges(ctx, store.EdgeQuery{Namespace: Namespace, Type: Namespace + "/selects", From: sessionID, Limit: 256})
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, edge := range selectionEdges {
		current[edge.ID] = true
	}
	for _, edge := range oldSelections {
		if !current[edge.ID] {
			tx.DeleteEdges = append(tx.DeleteEdges, store.EdgeRef{Namespace: Namespace, ID: edge.ID})
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
	vertices := []store.Vertex{{ID: id, Kind: Namespace + "/browser-context", Attributes: map[string]any{"provider": provider, "profile": profile, "scheme": scheme, "host": host, "observed_at": now.Format(time.RFC3339Nano)}, Provenance: store.Provenance{Source: "ctx", Operation: "web-origin-observed", At: now}}}
	parent := os.Getppid()
	edge := store.Edge{ID: "shell-browser/" + itoa(parent), From: "shell/" + itoa(parent), To: id, Type: Namespace + "/opened"}
	if existing, err := g.Store.GetVertex(ctx, Namespace, id); err == nil && equalAttributes(existing.Attributes, vertices[0].Attributes) {
		if old, edgeErr := g.Store.GetEdge(ctx, Namespace, edge.ID); edgeErr == nil && old.From == edge.From && old.To == edge.To {
			return nil
		}
	}
	_, err := g.Store.Apply(ctx, store.Transaction{Namespace: Namespace, Vertices: vertices, Edges: []store.Edge{edge}})
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
