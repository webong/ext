package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	native "github.com/webong/ext/pkg/ext-go/engine/ledger"
	"sort"
	"time"

	"github.com/webong/ext/res/system/capture/surface"
	"github.com/webong/ext/res/system/service/host"
	proc "github.com/webong/ext/res/system/service/process"
)

type OperationRequirements = native.OperationRequirements
type ShellRequirement = native.ShellRequirement
type FilesystemRequirement = native.FilesystemRequirement
type WebviewRequirement = native.WebviewRequirement
type HostSnapshot = native.HostSnapshot
type HostCandidates = native.HostCandidates
type PreparedHost = native.PreparedHost
type WebviewValidator = native.WebviewValidator
type HostRequirementError = native.HostRequirementError

func shellEnvironment(env environment, searchPath string) string {
	return digest(env.hostEnvironment() + "\x00" + searchPath + "\x00" + env.getenv("SHELL") + "\x00" + env.getenv("ComSpec"))
}

func webviewEnvironment(env environment) string {
	return digest(env.hostEnvironment() + "\x00" + env.getenv("LD_LIBRARY_PATH"))
}

// ReadHost reads host observations without refreshing or probing the OS.
func (g *Graph) ReadHost(ctx context.Context) (HostSnapshot, error) {
	snapshot, err := g.Store.Snapshot(ctx, Namespace)
	if err != nil {
		return HostSnapshot{}, err
	}
	result := HostSnapshot{HostInventory: HostInventory{Shells: []host.ShellInfo{}, Filesystems: []host.FilesystemInfo{}, Webviews: []host.WebviewInfo{}}, ObservedAt: map[string]time.Time{}, Environment: map[string]string{}}
	for _, vertex := range snapshot.Vertices {
		var target any
		switch vertex.Kind {
		case Namespace + "/host-inventory":
			kind, _ := vertex.Attributes["kind"].(string)
			date, _ := vertex.Attributes["observed_at"].(string)
			observed, err := time.Parse(time.RFC3339Nano, date)
			if err != nil {
				return HostSnapshot{}, fmt.Errorf("invalid %s inventory timestamp: %w", kind, err)
			}
			result.ObservedAt[kind] = observed
			result.Environment[kind], _ = vertex.Attributes["environment"].(string)
			if kind == "surface" {
				if result.Surfaces == nil {
					result.Surfaces = &surface.SurfaceSnapshot{Displays: []surface.DisplayInfo{}, Windows: []surface.WindowInfo{}}
				}
				result.Surfaces.ObservedAt = observed
				result.Surfaces.Platform, _ = vertex.Attributes["platform"].(string)
				result.Surfaces.CoordinateSpace, _ = vertex.Attributes["coordinate_space"].(string)
				for key, target := range map[string]*proc.CollectionStatus{"display_status": &result.Surfaces.DisplayStatus, "window_status": &result.Surfaces.WindowStatus} {
					data, err := json.Marshal(vertex.Attributes[key])
					if err != nil {
						return HostSnapshot{}, fmt.Errorf("read host surface %s: %w", key, err)
					}
					if err := json.Unmarshal(data, target); err != nil {
						return HostSnapshot{}, fmt.Errorf("read host surface %s: %w", key, err)
					}
				}
			}
			if kind == "accessibility" {
				if result.Accessibility == nil {
					result.Accessibility = &surface.AccessibilitySnapshot{Elements: []surface.AccessibilityElement{}}
				}
				result.Accessibility.ObservedAt = observed
				result.Accessibility.Platform, _ = vertex.Attributes["platform"].(string)
				data, err := json.Marshal(vertex.Attributes["status"])
				if err != nil {
					return HostSnapshot{}, fmt.Errorf("read host accessibility status: %w", err)
				}
				if err := json.Unmarshal(data, &result.Accessibility.Status); err != nil {
					return HostSnapshot{}, fmt.Errorf("read host accessibility status: %w", err)
				}
			}
			if kind == "permission" {
				if result.Permissions == nil {
					result.Permissions = &surface.PermissionSnapshot{}
				}
				result.Permissions.ObservedAt = observed
				result.Permissions.Platform, _ = vertex.Attributes["platform"].(string)
			}
			continue
		case Namespace + "/shell":
			result.Shells = append(result.Shells, host.ShellInfo{})
			target = &result.Shells[len(result.Shells)-1]
		case Namespace + "/filesystem":
			result.Filesystems = append(result.Filesystems, host.FilesystemInfo{})
			target = &result.Filesystems[len(result.Filesystems)-1]
		case Namespace + "/webview":
			result.Webviews = append(result.Webviews, host.WebviewInfo{})
			target = &result.Webviews[len(result.Webviews)-1]
		case Namespace + "/desktop":
			result.Desktop = &surface.DesktopInfo{}
			target = result.Desktop
		case Namespace + "/display":
			if result.Surfaces == nil {
				result.Surfaces = &surface.SurfaceSnapshot{Displays: []surface.DisplayInfo{}, Windows: []surface.WindowInfo{}}
			}
			result.Surfaces.Displays = append(result.Surfaces.Displays, surface.DisplayInfo{})
			target = &result.Surfaces.Displays[len(result.Surfaces.Displays)-1]
		case Namespace + "/window":
			if result.Surfaces == nil {
				result.Surfaces = &surface.SurfaceSnapshot{Displays: []surface.DisplayInfo{}, Windows: []surface.WindowInfo{}}
			}
			result.Surfaces.Windows = append(result.Surfaces.Windows, surface.WindowInfo{})
			target = &result.Surfaces.Windows[len(result.Surfaces.Windows)-1]
		case Namespace + "/accessibility-element":
			if result.Accessibility == nil {
				result.Accessibility = &surface.AccessibilitySnapshot{Elements: []surface.AccessibilityElement{}}
			}
			result.Accessibility.Elements = append(result.Accessibility.Elements, surface.AccessibilityElement{})
			target = &result.Accessibility.Elements[len(result.Accessibility.Elements)-1]
		case Namespace + "/permission":
			if result.Permissions == nil {
				result.Permissions = &surface.PermissionSnapshot{}
			}
			name, _ := vertex.Attributes["name"].(string)
			switch name {
			case "accessibility":
				target = &result.Permissions.Accessibility
			case "screen-recording":
				target = &result.Permissions.ScreenRecording
			default:
				continue
			}
		default:
			continue
		}
		data, err := json.Marshal(vertex.Attributes)
		if err != nil {
			return HostSnapshot{}, fmt.Errorf("read host record %s: %w", vertex.ID, err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			return HostSnapshot{}, fmt.Errorf("read host record %s: %w", vertex.ID, err)
		}
	}
	sort.Slice(result.Shells, func(i, j int) bool { return host.PathKey(result.Shells[i].Path) < host.PathKey(result.Shells[j].Path) })
	sort.Slice(result.Filesystems, func(i, j int) bool {
		return host.PathKey(result.Filesystems[i].MountPoint) < host.PathKey(result.Filesystems[j].MountPoint)
	})
	sort.Slice(result.Webviews, func(i, j int) bool {
		return host.WebviewIdentity(result.Webviews[i]) < host.WebviewIdentity(result.Webviews[j])
	})
	if result.Surfaces != nil {
		sort.Slice(result.Surfaces.Displays, func(i, j int) bool { return result.Surfaces.Displays[i].ID < result.Surfaces.Displays[j].ID })
		sort.Slice(result.Surfaces.Windows, func(i, j int) bool { return result.Surfaces.Windows[i].ID < result.Surfaces.Windows[j].ID })
	}
	if result.Accessibility != nil {
		sort.Slice(result.Accessibility.Elements, func(i, j int) bool { return result.Accessibility.Elements[i].ID < result.Accessibility.Elements[j].ID })
	}
	return result, nil
}

func (g *Graph) hostForRequirements(ctx context.Context, requirements OperationRequirements) (HostSnapshot, error) {
	if requirements.Shell == nil && requirements.Filesystem == nil && requirements.Webview == nil {
		return HostSnapshot{}, errors.New("operation needs at least one host requirement")
	}
	if requirements.Operation == "" {
		return HostSnapshot{}, errors.New("host requirements need an operation name")
	}
	snapshot, err := g.ReadHost(ctx)
	if err != nil {
		return HostSnapshot{}, err
	}
	env := g.environment()
	maxAge := requirements.MaxAge
	if maxAge == 0 {
		maxAge = 5 * time.Second
	}
	stale := func(kind string) bool {
		observed := snapshot.ObservedAt[kind]
		age := env.now().Sub(observed)
		return observed.IsZero() || age < 0 || maxAge < 0 || age > maxAge
	}
	refresh := HostInventory{}
	searchPath := env.getenv("PATH")
	if requirements.Shell != nil {
		if requirements.Shell.SearchPath != nil {
			searchPath = *requirements.Shell.SearchPath
		}
		if stale("shell") || snapshot.Environment["shell"] != shellEnvironment(env, searchPath) {
			refresh.Shells, err = host.DiscoverShellsOnPath(ctx, searchPath)
			if err != nil {
				return HostSnapshot{}, err
			}
			refresh.ShellSearchPath = &searchPath
		}
	}
	if requirements.Filesystem != nil && (stale("filesystem") || snapshot.Environment["filesystem"] != env.hostEnvironment()) {
		refresh.Filesystems, err = host.DiscoverFilesystems(ctx)
		if err != nil {
			return HostSnapshot{}, err
		}
	}
	if requirements.Webview != nil && (stale("webview") || snapshot.Environment["webview"] != webviewEnvironment(env)) {
		refresh.Webviews, err = host.DiscoverWebviews(ctx)
		if err != nil {
			return HostSnapshot{}, err
		}
	}
	if refresh.Shells != nil || refresh.Filesystems != nil || refresh.Webviews != nil {
		if err := g.ObserveHost(ctx, refresh); err != nil {
			return HostSnapshot{}, err
		}
		return g.ReadHost(ctx)
	}
	return snapshot, ctx.Err()
}

// ResolveHost refreshes stale requested categories and returns matching
// candidates. Webview candidates are installation evidence, not usable backends.
func (g *Graph) ResolveHost(ctx context.Context, requirements OperationRequirements) (HostCandidates, error) {
	return native.ResolveHostWith(ctx, requirements, g.hostForRequirements)
}

func (g *Graph) PrepareHost(ctx context.Context, requirements OperationRequirements, validateWebview WebviewValidator) (PreparedHost, error) {
	return native.PrepareHostWith(ctx, requirements, validateWebview, g.ResolveHost)
}
