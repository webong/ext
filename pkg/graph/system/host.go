package systemgraph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/webong/ctx/pkg/graph"
)

// ShellInfo describes an executable available to this host user. Discovery
// checks paths and permissions without launching the executable. The configured
// default is an environment declaration, not proof of the current process.
type ShellInfo struct {
	Name              string   `json:"name"`
	Path              string   `json:"path"`
	ResolvedPath      string   `json:"resolved_path"`
	ConfiguredDefault bool     `json:"configured_default"`
	Sources           []string `json:"sources"`
}

// FilesystemSpace is a point-in-time capacity observation, in bytes. Available
// space is what the calling user can allocate; it can differ from free space.
// On Windows, total space is also limited by any quota on the calling user.
type FilesystemSpace struct {
	TotalBytes     uint64 `json:"total_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// FilesystemInfo describes a mounted filesystem visible to this process. Space
// is nil when the OS cannot provide it. This is not a list of unmounted disks or
// a filesystem-driver catalog. Source never includes URL credentials.
type FilesystemInfo struct {
	MountPoint  string           `json:"mount_point"`
	Source      string           `json:"source"`
	Type        string           `json:"type"`
	ReadOnly    bool             `json:"read_only"`
	Space       *FilesystemSpace `json:"space,omitempty"`
	Label       string           `json:"label,omitempty"`
	DriveKind   string           `json:"drive_kind,omitempty"`
	DetailError string           `json:"detail_error,omitempty"`
}

// HostInventory is discovered directly by the graph library, independently of
// adapters. Nil slices in ObserveHost preserve the corresponding inventory;
// non-nil empty slices remove previously observed entries of that kind.
type HostInventory struct {
	Shells          []ShellInfo      `json:"shells"`
	Filesystems     []FilesystemInfo `json:"filesystems"`
	Webviews        []WebviewInfo    `json:"webviews"`
	shellSearchPath *string
}

type shellPath struct{ path, source string }

// DiscoverShells finds registered shells, standard shell executable names on
// PATH, platform shell locations, and a configured SHELL/ComSpec executable.
// Registered and configured paths can identify shells with arbitrary names.
func DiscoverShells(ctx context.Context) ([]ShellInfo, error) {
	return discoverShells(ctx, os.Getenv("PATH"))
}

func discoverShells(ctx context.Context, searchPath string) ([]ShellInfo, error) {
	candidates, err := platformShellPaths(ctx)
	if err != nil {
		return nil, err
	}
	defaultPath := os.Getenv("SHELL")
	if runtime.GOOS == "windows" {
		defaultPath = os.Getenv("ComSpec")
	}
	if defaultPath != "" {
		candidates = append(candidates, shellPath{defaultPath, "configured-default"})
	}
	for _, directory := range filepath.SplitList(searchPath) {
		if !filepath.IsAbs(directory) {
			continue
		}
		for _, name := range []string{"sh", "bash", "dash", "ash", "zsh", "ksh", "mksh", "fish", "csh", "tcsh", "pwsh", "powershell", "cmd", "nu", "elvish", "xonsh", "osh", "ysh"} {
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			candidates = append(candidates, shellPath{filepath.Join(directory, name), "path"})
		}
	}
	found := map[string]ShellInfo{}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(candidate.path) || !hostExecutable(candidate.path) {
			continue
		}
		path := filepath.Clean(candidate.path)
		key := hostPathKey(path)
		item, exists := found[key]
		if !exists {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				continue
			}
			name := filepath.Base(path)
			if runtime.GOOS == "windows" {
				name = strings.TrimSuffix(name, filepath.Ext(name))
			}
			item = ShellInfo{Name: name, Path: path, ResolvedPath: resolved, Sources: []string{}}
		}
		if !containsHostSource(item.Sources, candidate.source) {
			item.Sources = append(item.Sources, candidate.source)
		}
		if candidate.source == "configured-default" {
			item.ConfiguredDefault = true
		}
		sort.Strings(item.Sources)
		found[key] = item
	}
	result := make([]ShellInfo, 0, len(found))
	for _, item := range found {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return hostPathKey(result[i].Path) < hostPathKey(result[j].Path) })
	return result, nil
}

// DiscoverFilesystems reads the native mount inventory and available space.
// It observes the caller's mount namespace and permissions, including when CTX
// itself runs inside a container. It does not traverse filesystem contents.
func DiscoverFilesystems(ctx context.Context) ([]FilesystemInfo, error) {
	result, err := platformFilesystems(ctx)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = []FilesystemInfo{}
	}
	for i := range result {
		result[i].Source = redactFilesystemSource(result[i].Source)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MountPoint != result[j].MountPoint {
			return hostPathKey(result[i].MountPoint) < hostPathKey(result[j].MountPoint)
		}
		return result[i].Source+result[i].Type < result[j].Source+result[j].Type
	})
	return result, nil
}

// DiscoverHost returns local host inventory without opening a graph store.
func DiscoverHost(ctx context.Context) (HostInventory, error) {
	shells, err := DiscoverShells(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	filesystems, err := DiscoverFilesystems(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	webviews, err := DiscoverWebviews(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	return HostInventory{Shells: shells, Filesystems: filesystems, Webviews: webviews}, nil
}

// ScanHost refreshes all host inventories in the graph and returns the facts.
func (g *Graph) ScanHost(ctx context.Context) (HostInventory, error) {
	inventory, err := DiscoverHost(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	return inventory, g.ObserveHost(ctx, inventory)
}

// ObserveHost reconciles host-owned records and their machine relationships.
// Adapter inventory, shell sessions, and other namespaces are preserved.
func (g *Graph) ObserveHost(ctx context.Context, inventory HostInventory) error {
	if inventory.Shells == nil && inventory.Filesystems == nil && inventory.Webviews == nil {
		return nil
	}
	now := time.Now().UTC()
	hostname, _ := os.Hostname()
	vertices := map[string]graph.Vertex{
		"machine/local": {ID: "machine/local", Kind: Namespace + "/machine", Attributes: map[string]any{"hostname": hostname, "os": runtime.GOOS, "architecture": runtime.GOARCH}, Provenance: graph.Provenance{Source: "ctx", Operation: "host-observed", At: now}},
	}
	edges := map[string]graph.Edge{}
	managedKinds := map[string]bool{}
	managedTypes := map[string]bool{}
	add := func(id, kind, relation string, attributes map[string]any) {
		vertices[id] = graph.Vertex{ID: id, Kind: Namespace + "/" + kind, Attributes: attributes, Provenance: graph.Provenance{Source: "ctx", Operation: "host-observed", At: now}}
		edgeID := "machine-" + id
		edges[edgeID] = graph.Edge{ID: edgeID, From: "machine/local", To: id, Type: Namespace + "/" + relation}
	}
	if inventory.Shells != nil {
		managedKinds[Namespace+"/shell"] = true
		managedTypes[Namespace+"/has-shell"] = true
		searchPath := os.Getenv("PATH")
		if inventory.shellSearchPath != nil {
			searchPath = *inventory.shellSearchPath
		}
		add("host-inventory/shells", "host-inventory", "has-host-inventory", map[string]any{"kind": "shell", "count": len(inventory.Shells), "observed_at": now.Format(time.RFC3339Nano), "environment": shellEnvironment(searchPath)})
		for _, shell := range inventory.Shells {
			if !filepath.IsAbs(shell.Path) || shell.Name == "" {
				return errors.New("host shell requires a name and absolute executable path")
			}
			add("host-shell/"+digest(hostPathKey(shell.Path)), "shell", "has-shell", map[string]any{
				"name": shell.Name, "path": shell.Path, "resolved_path": shell.ResolvedPath,
				"configured_default": shell.ConfiguredDefault, "sources": shell.Sources,
			})
		}
	}
	if inventory.Filesystems != nil {
		managedKinds[Namespace+"/filesystem"] = true
		managedTypes[Namespace+"/has-filesystem"] = true
		add("host-inventory/filesystems", "host-inventory", "has-host-inventory", map[string]any{"kind": "filesystem", "count": len(inventory.Filesystems), "observed_at": now.Format(time.RFC3339Nano), "environment": hostEnvironment()})
		for _, filesystem := range inventory.Filesystems {
			if !filepath.IsAbs(filesystem.MountPoint) {
				return errors.New("host filesystem requires an absolute mount point")
			}
			filesystem.Source = redactFilesystemSource(filesystem.Source)
			id := hostPathKey(filesystem.MountPoint) + "\x00" + filesystem.Source + "\x00" + filesystem.Type
			attributes := map[string]any{"mount_point": filesystem.MountPoint, "source": filesystem.Source, "type": filesystem.Type, "read_only": filesystem.ReadOnly}
			if filesystem.Space != nil {
				attributes["space"] = map[string]any{
					"total_bytes": filesystem.Space.TotalBytes, "free_bytes": filesystem.Space.FreeBytes,
					"available_bytes": filesystem.Space.AvailableBytes,
				}
			}
			for key, value := range map[string]string{"label": filesystem.Label, "drive_kind": filesystem.DriveKind, "detail_error": filesystem.DetailError} {
				if value != "" {
					attributes[key] = value
				}
			}
			add("host-filesystem/"+digest(id), "filesystem", "has-filesystem", attributes)
		}
	}
	if inventory.Webviews != nil {
		managedKinds[Namespace+"/webview"] = true
		managedTypes[Namespace+"/has-webview"] = true
		add("host-inventory/webviews", "host-inventory", "has-host-inventory", map[string]any{"kind": "webview", "count": len(inventory.Webviews), "observed_at": now.Format(time.RFC3339Nano), "environment": webviewEnvironment()})
		for _, webview := range inventory.Webviews {
			if webview.Name == "" || webview.Engine == "" || webview.API == "" || webview.Location == "" || webview.Scope == "" || webview.Source == "" {
				return errors.New("host webview requires a name, engine, API, location, scope, and source")
			}
			attributes := map[string]any{"name": webview.Name, "engine": webview.Engine, "api": webview.API, "location": webview.Location, "scope": webview.Scope, "source": webview.Source}
			for key, value := range map[string]string{"version": webview.Version, "abi_version": webview.ABIVersion, "architecture": webview.Architecture, "resolved_path": webview.ResolvedPath, "detail_error": webview.DetailError} {
				if value != "" {
					attributes[key] = value
				}
			}
			add("host-webview/"+digest(webviewIdentity(webview)), "webview", "has-webview", attributes)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := g.Store.Snapshot(ctx, Namespace)
		if err != nil {
			return err
		}
		tx := graph.Transaction{Namespace: Namespace, ExpectedRevision: &snapshot.Revision, DeleteIncidentEdges: true}
		for _, vertex := range snapshot.Vertices {
			if managedKinds[vertex.Kind] {
				if _, keep := vertices[vertex.ID]; !keep {
					tx.DeleteVertices = append(tx.DeleteVertices, graph.VertexRef{Namespace: Namespace, ID: vertex.ID})
				}
			}
		}
		for _, edge := range snapshot.Edges {
			if managedTypes[edge.Type] {
				if _, keep := edges[edge.ID]; !keep {
					tx.DeleteEdges = append(tx.DeleteEdges, graph.EdgeRef{Namespace: Namespace, ID: edge.ID})
				}
			}
		}
		for _, vertex := range vertices {
			tx.Vertices = append(tx.Vertices, vertex)
		}
		for _, edge := range edges {
			tx.Edges = append(tx.Edges, edge)
		}
		_, err = g.Store.Apply(ctx, tx)
		if errors.Is(err, graph.ErrConflict) {
			continue
		}
		return err
	}
	return fmt.Errorf("host inventory changed during observation")
}

func redactFilesystemSource(source string) string {
	// Strip userinfo directly, including malformed URLs that a URL parser
	// would reject. Only the authority segment can contain URL credentials.
	start := 0
	if strings.HasPrefix(source, "//") {
		start = 2
	} else if _, rest, ok := strings.Cut(source, "://"); ok {
		start = len(source) - len(rest)
	} else {
		return source
	}
	end := strings.IndexAny(source[start:], "/?#")
	if end < 0 {
		end = len(source) - start
	}
	if at := strings.LastIndexByte(source[start:start+end], '@'); at >= 0 {
		return source[:start] + source[start+at+1:]
	}
	return source
}

func hostPathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func containsHostSource(sources []string, source string) bool {
	for _, item := range sources {
		if item == source {
			return true
		}
	}
	return false
}

func fmtHostReadError(kind string, err error) error {
	return fmt.Errorf("discover %s: %w", kind, err)
}

func filesystemSpace(blocks, free, available, size uint64) *FilesystemSpace {
	if size == 0 || blocks > ^uint64(0)/size || free > ^uint64(0)/size || available > ^uint64(0)/size {
		return nil
	}
	return &FilesystemSpace{TotalBytes: blocks * size, FreeBytes: free * size, AvailableBytes: available * size}
}
