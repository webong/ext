package host

import (
	"context"
	"fmt"
	"github.com/webong/ext/res/system/internal/identity"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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

type shellPath struct{ path, source string }

// DiscoverShells finds registered shells, standard shell executable names on
// PATH, platform shell locations, and a configured SHELL/ComSpec executable.
// Registered and configured paths can identify shells with arbitrary names.
func DiscoverShells(ctx context.Context) ([]ShellInfo, error) {
	return DiscoverShellsOnPath(ctx, os.Getenv("PATH"))
}

// DiscoverShellsOnPath is DiscoverShells with an explicit PATH-style search list in place of the process environment's.
func DiscoverShellsOnPath(ctx context.Context, searchPath string) ([]ShellInfo, error) {
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
		if !filepath.IsAbs(candidate.path) || !IsExecutable(candidate.path) {
			continue
		}
		path := filepath.Clean(candidate.path)
		key := PathKey(path)
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
	sort.Slice(result, func(i, j int) bool { return PathKey(result[i].Path) < PathKey(result[j].Path) })
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
		result[i].Source = RedactSource(result[i].Source)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MountPoint != result[j].MountPoint {
			return PathKey(result[i].MountPoint) < PathKey(result[j].MountPoint)
		}
		return result[i].Source+result[i].Type < result[j].Source+result[j].Type
	})
	return result, nil
}

// RedactSource removes URL credentials from a filesystem source.
func RedactSource(source string) string {
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

// PathKey returns the key under which a host path is compared and recorded: the cleaned path, lower-cased on Windows.
func PathKey(path string) string {
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

// Environment identifies the host environment the observations were made in.
// Graph records carry it so a consumer can tell when they came from somewhere else.
func Environment() string { return identity.HostEnvironment() }
