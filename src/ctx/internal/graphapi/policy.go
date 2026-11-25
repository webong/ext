package graphapi

import (
	"context"
	"errors"
	"fmt"
	wire "github.com/webong/ext/pkg/ext-go/core/ledger/observation"
	"github.com/webong/ext/res/system/service/host"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ResolveHostWith applies caller requirements to an observation supplied by the
// owning engine. It contains selection policy, not graph storage or recording.
func ResolveHostWith(ctx context.Context, requirements OperationRequirements, observe func(context.Context, OperationRequirements) (HostSnapshot, error)) (HostCandidates, error) {
	snapshot, err := observe(ctx, requirements)
	if err != nil {
		return HostCandidates{}, err
	}
	result := HostCandidates{Operation: requirements.Operation, ObservedAt: map[string]time.Time{}}
	if required := requirements.Shell; required != nil {
		result.ObservedAt["shell"] = snapshot.ObservedAt["shell"]
		if required.Select != "" {
			searchPath := os.Getenv("PATH")
			if required.SearchPath != nil {
				searchPath = *required.SearchPath
			}
			selected, err := resolveShellExecutable(required.Select, searchPath)
			if err != nil {
				return HostCandidates{}, requirementCause(requirements, "shell", "", err)
			}
			if required.Name != "" && !sameShellName(selected.Name, required.Name) {
				return HostCandidates{}, requirementError(requirements, "shell", "explicit executable does not match requested shell name")
			}
			for _, observed := range snapshot.Shells {
				if host.PathKey(observed.Path) == host.PathKey(selected.Path) {
					selected.Sources = observed.Sources
					selected.ConfiguredDefault = observed.ConfiguredDefault
				}
			}
			result.Shells = []wire.ShellInfo{selected}
		} else {
			for _, shell := range snapshot.Shells {
				if required.Name == "" || sameShellName(shell.Name, required.Name) {
					result.Shells = append(result.Shells, shell)
				}
			}
		}
		if len(result.Shells) == 0 {
			return HostCandidates{}, requirementError(requirements, "shell", "no executable matches the requirement")
		}
	}
	if required := requirements.Filesystem; required != nil {
		result.ObservedAt["filesystem"] = snapshot.ObservedAt["filesystem"]
		items := snapshot.Filesystems
		if required.Path != "" {
			path, err := existingHostPath(required.Path)
			if err != nil {
				return HostCandidates{}, requirementCause(requirements, "filesystem", "", err)
			}
			filesystem, err := filesystemAt(ctx, path)
			if err != nil {
				return HostCandidates{}, requirementCause(requirements, "filesystem", "", err)
			}
			filesystem.Source = host.RedactSource(filesystem.Source)
			items = []wire.FilesystemInfo{filesystem}
		}
		for _, filesystem := range items {
			if required.Select != "" {
				selected, err := filepath.Abs(required.Select)
				if err != nil {
					return HostCandidates{}, err
				}
				if host.PathKey(filesystem.MountPoint) != host.PathKey(selected) {
					continue
				}
			}
			if filesystemMatches(filesystem, *required) {
				result.Filesystems = append(result.Filesystems, filesystem)
			}
		}
		if len(result.Filesystems) == 0 {
			return HostCandidates{}, requirementError(requirements, "filesystem", "no mount matches the selected path, type, writable status, and available space")
		}
	}
	if required := requirements.Webview; required != nil {
		result.ObservedAt["webview"] = snapshot.ObservedAt["webview"]
		for _, webview := range snapshot.Webviews {
			if webviewMatches(webview, *required) {
				result.Webviews = append(result.Webviews, webview)
			}
		}
		if len(result.Webviews) == 0 {
			return HostCandidates{}, requirementError(requirements, "webview", "no runtime matches the selected location, engine, API, ABI, version, and architecture")
		}
	}
	return result, ctx.Err()
}

// PrepareHost discovers current candidates, selects one for each requirement,
// and performs live checks. Ambiguous choices require an explicit selection.
// Webview preparation requires a validator from the consuming backend.
func PrepareHostWith(ctx context.Context, requirements OperationRequirements, validateWebview WebviewValidator, resolve func(context.Context, OperationRequirements) (HostCandidates, error)) (PreparedHost, error) {
	if requirements.Webview != nil && validateWebview == nil {
		return PreparedHost{}, requirementError(requirements, "webview", "preparation requires a validator from the embedding backend")
	}
	requirements.MaxAge = -1 // Preparation never relies on cached installation evidence.
	candidates, err := resolve(ctx, requirements)
	if err != nil {
		return PreparedHost{}, err
	}
	result := PreparedHost{Operation: requirements.Operation}
	if required := requirements.Shell; required != nil {
		selected, err := chooseShell(candidates.Shells, *required)
		if err != nil {
			return PreparedHost{}, requirementCause(requirements, "shell", "", err)
		}
		live, err := inspectShellExecutable(selected.Path)
		if err != nil {
			return PreparedHost{}, requirementCause(requirements, "shell", "", err)
		}
		if host.PathKey(live.ResolvedPath) != host.PathKey(selected.ResolvedPath) {
			return PreparedHost{}, requirementError(requirements, "shell", "executable changed during preparation")
		}
		result.Shell = &selected
	}
	if required := requirements.Filesystem; required != nil {
		if len(candidates.Filesystems) != 1 {
			return PreparedHost{}, requirementError(requirements, "filesystem", "multiple mounts match; specify a path or mount point")
		}
		selected := candidates.Filesystems[0]
		path := selected.MountPoint
		if required.Path != "" {
			path = required.Path
		}
		path, err := existingHostPath(path)
		if err != nil {
			return PreparedHost{}, requirementCause(requirements, "filesystem", "", err)
		}
		live, err := filesystemAt(ctx, path)
		if err != nil {
			return PreparedHost{}, requirementCause(requirements, "filesystem", "", err)
		}
		live.Source = host.RedactSource(live.Source)
		if !sameFilesystem(live, selected) || !filesystemMatches(live, *required) {
			return PreparedHost{}, requirementError(requirements, "filesystem", "mount or available space changed during preparation")
		}
		if required.Writable {
			info, err := os.Stat(path)
			if err != nil {
				return PreparedHost{}, err
			}
			if !info.IsDir() {
				path = filepath.Dir(path)
			}
			if err := host.DirectoryWritable(path); err != nil {
				return PreparedHost{}, requirementCause(requirements, "filesystem", "directory is not writable", err)
			}
		}
		result.Filesystem = &live
	}
	if requirements.Webview != nil {
		if len(candidates.Webviews) != 1 {
			return PreparedHost{}, requirementError(requirements, "webview", "multiple runtimes match; specify a location or narrower requirements")
		}
		selected := candidates.Webviews[0]
		if err := validateWebview(ctx, selected); err != nil {
			return PreparedHost{}, requirementCause(requirements, "webview", "backend validation failed", err)
		}
		result.Webview = &selected
	}
	if err := ctx.Err(); err != nil {
		return PreparedHost{}, err
	}
	result.ValidatedAt = time.Now().UTC()
	return result, nil
}

func requirementError(requirements OperationRequirements, kind, message string) error {
	return &HostRequirementError{Operation: requirements.Operation, Kind: kind, Reason: message}
}

func requirementCause(requirements OperationRequirements, kind, reason string, cause error) error {
	return &HostRequirementError{Operation: requirements.Operation, Kind: kind, Reason: reason, Cause: cause}
}

func sameShellName(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(strings.TrimSuffix(strings.ToLower(left), ".exe"), strings.TrimSuffix(strings.ToLower(right), ".exe"))
	}
	return left == right
}

func inspectShellExecutable(path string) (wire.ShellInfo, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return wire.ShellInfo{}, err
	}
	if !host.IsExecutable(path) {
		return wire.ShellInfo{}, fmt.Errorf("shell executable is unavailable: %s", path)
	}
	if err := host.ExecutableAccess(path); err != nil {
		return wire.ShellInfo{}, fmt.Errorf("shell executable access: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return wire.ShellInfo{}, err
	}
	name := filepath.Base(path)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return wire.ShellInfo{Name: name, Path: path, ResolvedPath: resolved, Sources: []string{"explicit-selection"}}, nil
}

func resolveShellExecutable(selection, searchPath string) (wire.ShellInfo, error) {
	if filepath.IsAbs(selection) || strings.ContainsAny(selection, `/\`) {
		return inspectShellExecutable(selection)
	}
	names := []string{selection}
	if runtime.GOOS == "windows" && filepath.Ext(selection) == "" {
		names = []string{selection + ".exe"}
	}
	for _, directory := range filepath.SplitList(searchPath) {
		if !filepath.IsAbs(directory) {
			continue
		}
		for _, name := range names {
			path := filepath.Join(directory, name)
			if host.IsExecutable(path) {
				return inspectShellExecutable(path)
			}
		}
	}
	return wire.ShellInfo{}, fmt.Errorf("shell %q is not executable on the effective PATH", selection)
}

func chooseShell(items []wire.ShellInfo, required ShellRequirement) (wire.ShellInfo, error) {
	if len(items) == 1 {
		return items[0], nil
	}
	if required.Name != "" {
		searchPath := os.Getenv("PATH")
		if required.SearchPath != nil {
			searchPath = *required.SearchPath
		}
		if first, err := resolveShellExecutable(required.Name, searchPath); err == nil {
			for _, item := range items {
				if host.PathKey(first.Path) == host.PathKey(item.Path) {
					return item, nil
				}
			}
		}
	}
	var configured *wire.ShellInfo
	for i := range items {
		if items[i].ConfiguredDefault {
			if configured != nil {
				return wire.ShellInfo{}, errors.New("multiple configured shells match; specify an executable")
			}
			configured = &items[i]
		}
	}
	if configured != nil {
		return *configured, nil
	}
	return wire.ShellInfo{}, errors.New("multiple shells match; specify a name or executable")
}

func filesystemMatches(item wire.FilesystemInfo, required FilesystemRequirement) bool {
	if required.Writable && (item.ReadOnly || item.Type == "") {
		return false
	}
	if required.MinimumAvailableBytes > 0 && (item.Space == nil || item.Space.AvailableBytes < required.MinimumAvailableBytes) {
		return false
	}
	if len(required.Types) > 0 {
		found := false
		for _, name := range required.Types {
			if strings.EqualFold(item.Type, name) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sameFilesystem(left, right wire.FilesystemInfo) bool {
	return host.PathKey(left.MountPoint) == host.PathKey(right.MountPoint) && left.Source == right.Source && left.Type == right.Type
}

func webviewMatches(item wire.WebviewInfo, required WebviewRequirement) bool {
	return (required.Select == "" || host.PathKey(required.Select) == host.PathKey(item.Location)) &&
		(required.Engine == "" || item.Engine == required.Engine) && (required.API == "" || item.API == required.API) &&
		(required.ABIVersion == "" || item.ABIVersion == required.ABIVersion) && (required.Version == "" || item.Version == required.Version) &&
		(required.Architecture == "" || item.Architecture == required.Architecture)
}

func existingHostPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Lstat(path); err == nil {
			return filepath.EvalSymlinks(path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		path = parent
	}
}

func filesystemAt(ctx context.Context, path string) (wire.FilesystemInfo, error) {
	v, err := host.FilesystemAt(ctx, path)
	out := wire.FilesystemInfo{MountPoint: v.MountPoint, Source: v.Source, Type: v.Type, ReadOnly: v.ReadOnly, Label: v.Label, DriveKind: v.DriveKind, DetailError: v.DetailError}
	if v.Space != nil {
		out.Space = &wire.FilesystemSpace{TotalBytes: v.Space.TotalBytes, FreeBytes: v.Space.FreeBytes, AvailableBytes: v.Space.AvailableBytes}
	}
	return out, err
}
func (g *Graph) ResolveHost(ctx context.Context, r OperationRequirements) (HostCandidates, error) {
	return ResolveHostWith(ctx, r, g.ObserveRequirements)
}
func (g *Graph) PrepareHost(ctx context.Context, r OperationRequirements, v WebviewValidator) (PreparedHost, error) {
	return PrepareHostWith(ctx, r, v, g.ResolveHost)
}
