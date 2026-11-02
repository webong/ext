package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/webong/ext/res/system/graph"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/webong/ext/res/system/capture/surface"
	"github.com/webong/ext/res/system/service/host"
)

// HostInventory is discovered directly by the graph library, independently of
// adapters. Nil slices and pointers in ObserveHost preserve the corresponding
// inventory; non-nil empty slices remove previously observed entries of that kind.
type HostInventory struct {
	Shells          []host.ShellInfo               `json:"shells"`
	Filesystems     []host.FilesystemInfo          `json:"filesystems"`
	Webviews        []host.WebviewInfo             `json:"webviews"`
	Desktop         *surface.DesktopInfo           `json:"desktop,omitempty"`
	Surfaces        *surface.SurfaceSnapshot       `json:"surfaces,omitempty"`
	Accessibility   *surface.AccessibilitySnapshot `json:"accessibility,omitempty"`
	Permissions     *surface.PermissionSnapshot    `json:"permissions,omitempty"`
	Resources       *host.ResourceInfo             `json:"resources,omitempty"`
	shellSearchPath *string
}

// DiscoverHost returns local host inventory without opening a graph store.
func DiscoverHost(ctx context.Context) (HostInventory, error) {
	shells, err := host.DiscoverShells(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	filesystems, err := host.DiscoverFilesystems(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	webviews, err := host.DiscoverWebviews(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	desktop, err := surface.DiscoverDesktop(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	surfaces, err := surface.DiscoverSurfaces(ctx, surface.SurfaceOptions{})
	if err != nil {
		return HostInventory{}, err
	}
	accessibility, err := surface.DiscoverAccessibility(ctx, surface.AccessibilityOptions{})
	if err != nil {
		return HostInventory{}, err
	}
	permissions, err := surface.DiscoverPermissions(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	resources, err := host.DiscoverResources(ctx)
	if err != nil {
		return HostInventory{}, err
	}
	return HostInventory{Shells: shells, Filesystems: filesystems, Webviews: webviews, Desktop: &desktop, Surfaces: &surfaces, Accessibility: &accessibility, Permissions: &permissions, Resources: &resources}, nil
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
	if inventory.Shells == nil && inventory.Filesystems == nil && inventory.Webviews == nil && inventory.Desktop == nil && inventory.Surfaces == nil && inventory.Accessibility == nil && inventory.Permissions == nil && inventory.Resources == nil {
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
			add("host-shell/"+digest(host.PathKey(shell.Path)), "shell", "has-shell", map[string]any{
				"name": shell.Name, "path": shell.Path, "resolved_path": shell.ResolvedPath,
				"configured_default": shell.ConfiguredDefault, "sources": shell.Sources,
			})
		}
	}
	if inventory.Filesystems != nil {
		managedKinds[Namespace+"/filesystem"] = true
		managedTypes[Namespace+"/has-filesystem"] = true
		add("host-inventory/filesystems", "host-inventory", "has-host-inventory", map[string]any{"kind": "filesystem", "count": len(inventory.Filesystems), "observed_at": now.Format(time.RFC3339Nano), "environment": host.Environment()})
		for _, filesystem := range inventory.Filesystems {
			if !filepath.IsAbs(filesystem.MountPoint) {
				return errors.New("host filesystem requires an absolute mount point")
			}
			filesystem.Source = host.RedactSource(filesystem.Source)
			id := host.PathKey(filesystem.MountPoint) + "\x00" + filesystem.Source + "\x00" + filesystem.Type
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
			add("host-webview/"+digest(host.WebviewIdentity(webview)), "webview", "has-webview", attributes)
		}
	}
	if inventory.Desktop != nil {
		desktop := *inventory.Desktop
		if desktop.Platform == "" || desktop.ObservedAt.IsZero() || desktop.Recording.Status == "" || desktop.Recording.CheckedAt.IsZero() {
			return errors.New("host desktop observation is incomplete")
		}
		managedKinds[Namespace+"/desktop"] = true
		managedTypes[Namespace+"/has-desktop"] = true
		add("host-inventory/desktop", "host-inventory", "has-host-inventory", map[string]any{"kind": "desktop", "count": 1, "observed_at": desktop.ObservedAt.Format(time.RFC3339Nano), "environment": host.Environment()})
		attributes, err := jsonAttributes(desktop)
		if err != nil {
			return err
		}
		add("host-desktop/local", "desktop", "has-desktop", attributes)
	}
	if inventory.Surfaces != nil {
		surfaces := *inventory.Surfaces
		if surfaces.Platform == "" || surfaces.CoordinateSpace == "" || surfaces.ObservedAt.IsZero() || surfaces.DisplayStatus.State == "" || surfaces.WindowStatus.State == "" {
			return errors.New("host surface observation is incomplete")
		}
		managedKinds[Namespace+"/display"] = true
		managedKinds[Namespace+"/window"] = true
		managedTypes[Namespace+"/has-display"] = true
		managedTypes[Namespace+"/has-window"] = true
		inventoryAttributes := map[string]any{
			"kind": "surface", "count": len(surfaces.Displays) + len(surfaces.Windows),
			"display_count": len(surfaces.Displays), "window_count": len(surfaces.Windows),
			"platform": surfaces.Platform, "coordinate_space": surfaces.CoordinateSpace,
			"observed_at": surfaces.ObservedAt.Format(time.RFC3339Nano), "environment": host.Environment(),
		}
		displayStatus, err := jsonAttributes(surfaces.DisplayStatus)
		if err != nil {
			return err
		}
		windowStatus, err := jsonAttributes(surfaces.WindowStatus)
		if err != nil {
			return err
		}
		inventoryAttributes["display_status"] = displayStatus
		inventoryAttributes["window_status"] = windowStatus
		add("host-inventory/surfaces", "host-inventory", "has-host-inventory", inventoryAttributes)
		for _, display := range surfaces.Displays {
			if display.ID == "" || display.Bounds.Width <= 0 || display.Bounds.Height <= 0 {
				return errors.New("host display requires an opaque ID and positive bounds")
			}
			attributes, err := jsonAttributes(display)
			if err != nil {
				return err
			}
			add("host-display/"+digest(display.ID), "display", "has-display", attributes)
		}
		for _, window := range surfaces.Windows {
			if window.ID == "" || window.PID <= 0 || window.ProcessStartID == "" || window.Bounds.Width <= 0 || window.Bounds.Height <= 0 {
				return errors.New("host window requires an opaque ID, process generation, and positive bounds")
			}
			attributes, err := jsonAttributes(window)
			if err != nil {
				return err
			}
			add("host-window/"+digest(window.ID), "window", "has-window", attributes)
		}
	}
	if inventory.Accessibility != nil {
		accessibility := *inventory.Accessibility
		if accessibility.Platform == "" || accessibility.ObservedAt.IsZero() || accessibility.Status.State == "" {
			return errors.New("host accessibility observation is incomplete")
		}
		managedKinds[Namespace+"/accessibility-element"] = true
		managedTypes[Namespace+"/has-accessibility-element"] = true
		status, err := jsonAttributes(accessibility.Status)
		if err != nil {
			return err
		}
		add("host-inventory/accessibility", "host-inventory", "has-host-inventory", map[string]any{
			"kind": "accessibility", "count": len(accessibility.Elements), "platform": accessibility.Platform,
			"status": status, "observed_at": accessibility.ObservedAt.Format(time.RFC3339Nano), "environment": host.Environment(),
		})
		for _, element := range accessibility.Elements {
			if element.ID == "" || element.PID <= 0 || element.ProcessStartID == "" || element.Role == "" {
				return errors.New("host accessibility element requires an opaque ID, process generation, and role")
			}
			attributes, err := jsonAttributes(element)
			if err != nil {
				return err
			}
			add("host-accessibility/"+digest(element.ID), "accessibility-element", "has-accessibility-element", attributes)
		}
	}
	if inventory.Resources != nil {
		resources := *inventory.Resources
		if resources.OS == "" || resources.Architecture == "" || resources.ObservedAt.IsZero() ||
			resources.LogicalCPUs <= 0 || resources.CPUStatus.State == "" ||
			resources.MemoryStatus.State == "" || resources.UptimeStatus.State == "" ||
			resources.Container.State == "" {
			return errors.New("host resource observation is incomplete")
		}
		managedKinds[Namespace+"/host-resource"] = true
		managedTypes[Namespace+"/has-host-resource"] = true
		add("host-inventory/resources", "host-inventory", "has-host-inventory", map[string]any{
			"kind": "resource", "count": 1, "platform": resources.OS,
			"observed_at": resources.ObservedAt.Format(time.RFC3339Nano), "environment": host.Environment(),
		})
		attributes, err := jsonAttributes(resources)
		if err != nil {
			return err
		}
		add("host-resources/local", "host-resource", "has-host-resource", attributes)
	}
	if inventory.Permissions != nil {
		permissions := *inventory.Permissions
		if permissions.Platform == "" || permissions.ObservedAt.IsZero() || permissions.Accessibility.State == "" || permissions.ScreenRecording.State == "" {
			return errors.New("host permission observation is incomplete")
		}
		managedKinds[Namespace+"/permission"] = true
		managedTypes[Namespace+"/has-permission"] = true
		add("host-inventory/permissions", "host-inventory", "has-host-inventory", map[string]any{
			"kind": "permission", "count": 2, "platform": permissions.Platform,
			"observed_at": permissions.ObservedAt.Format(time.RFC3339Nano), "environment": host.Environment(),
		})
		for name, permission := range map[string]surface.PermissionInfo{"accessibility": permissions.Accessibility, "screen-recording": permissions.ScreenRecording} {
			attributes, err := jsonAttributes(permission)
			if err != nil {
				return err
			}
			attributes["name"] = name
			add("host-permission/"+name, "permission", "has-permission", attributes)
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

func jsonAttributes(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var attributes map[string]any
	if err := json.Unmarshal(data, &attributes); err != nil {
		return nil, err
	}
	return attributes, nil
}
