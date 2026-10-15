package systemgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// OperationRequirements describes host resources needed by a caller. Non-nil
// requirements are conjunctive. Select pins a resource: failure never causes a
// different resource to be substituted. MaxAge defaults to five seconds; a
// negative value forces discovery. No adapter or backend is chosen by name.
type OperationRequirements struct {
	Operation  string                 `json:"operation"`
	Shell      *ShellRequirement      `json:"shell,omitempty"`
	Filesystem *FilesystemRequirement `json:"filesystem,omitempty"`
	Webview    *WebviewRequirement    `json:"webview,omitempty"`
	MaxAge     time.Duration          `json:"-"`
}

type ShellRequirement struct {
	Name       string  `json:"name,omitempty"`
	Select     string  `json:"select,omitempty"` // Executable name or path.
	SearchPath *string `json:"-"`                // Optional effective PATH, without changing the process environment.
}

type FilesystemRequirement struct {
	Select                string   `json:"select,omitempty"` // Mount point.
	Path                  string   `json:"path,omitempty"`   // Existing file/directory or a future output path.
	Types                 []string `json:"types,omitempty"`
	Writable              bool     `json:"writable,omitempty"`
	MinimumAvailableBytes uint64   `json:"minimum_available_bytes,omitempty"`
}

type WebviewRequirement struct {
	Select       string `json:"select,omitempty"` // Discovery location.
	Engine       string `json:"engine,omitempty"`
	API          string `json:"api,omitempty"`
	ABIVersion   string `json:"abi_version,omitempty"`
	Version      string `json:"version,omitempty"` // Exact runtime version, when known.
	Architecture string `json:"architecture,omitempty"`
}

// HostSnapshot is the typed graph view with separate freshness per category.
type HostSnapshot struct {
	HostInventory
	ObservedAt  map[string]time.Time `json:"observed_at"`
	environment map[string]string
}

type HostCandidates struct {
	Operation   string               `json:"operation"`
	Shells      []ShellInfo          `json:"shells,omitempty"`
	Filesystems []FilesystemInfo     `json:"filesystems,omitempty"`
	Webviews    []WebviewInfo        `json:"webviews,omitempty"`
	ObservedAt  map[string]time.Time `json:"observed_at"`
}

// PreparedHost contains one validated resource per requirement. This does not
// authorize execution, reserve capacity, or eliminate races with OS changes.
type PreparedHost struct {
	Operation   string          `json:"operation"`
	Shell       *ShellInfo      `json:"shell,omitempty"`
	Filesystem  *FilesystemInfo `json:"filesystem,omitempty"`
	Webview     *WebviewInfo    `json:"webview,omitempty"`
	ValidatedAt time.Time       `json:"validated_at"`
}

// WebviewValidator belongs to the embedding backend. It must establish that
// this backend can use the selected runtime, including its ABI and dependencies.
type WebviewValidator func(context.Context, WebviewInfo) error

// HostRequirementError identifies a failed operation requirement. Cause, when
// present, preserves OS, context, and backend errors for errors.Is/errors.As.
type HostRequirementError struct {
	Operation string
	Kind      string
	Reason    string
	Cause     error
}

func (err *HostRequirementError) Error() string {
	message := err.Reason
	if err.Cause != nil {
		if message != "" {
			message += ": "
		}
		message += err.Cause.Error()
	}
	return fmt.Sprintf("%s: %s requirement: %s", err.Operation, err.Kind, message)
}

func (err *HostRequirementError) Unwrap() error { return err.Cause }

func shellEnvironment(searchPath string) string {
	return digest(hostEnvironment() + "\x00" + searchPath + "\x00" + os.Getenv("SHELL") + "\x00" + os.Getenv("ComSpec"))
}

func webviewEnvironment() string {
	return digest(hostEnvironment() + "\x00" + os.Getenv("LD_LIBRARY_PATH"))
}

func hostEnvironment() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "hostname-unavailable"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "home-unavailable"
	}
	identity := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", hostname, runtime.GOOS, runtime.GOARCH, os.Getuid(), home)
	if runtime.GOOS == "linux" {
		for _, path := range []string{"/proc/self/ns/mnt", "/proc/self/ns/user"} {
			namespace, err := os.Readlink(path)
			if err != nil {
				namespace = "namespace-unavailable"
			}
			identity += "\x00" + namespace
		}
	}
	return digest(identity)
}

// ReadHost reads host observations without refreshing or probing the OS.
func (g *Graph) ReadHost(ctx context.Context) (HostSnapshot, error) {
	snapshot, err := g.Store.Snapshot(ctx, Namespace)
	if err != nil {
		return HostSnapshot{}, err
	}
	result := HostSnapshot{HostInventory: HostInventory{Shells: []ShellInfo{}, Filesystems: []FilesystemInfo{}, Webviews: []WebviewInfo{}}, ObservedAt: map[string]time.Time{}, environment: map[string]string{}}
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
			result.environment[kind], _ = vertex.Attributes["environment"].(string)
			continue
		case Namespace + "/shell":
			result.Shells = append(result.Shells, ShellInfo{})
			target = &result.Shells[len(result.Shells)-1]
		case Namespace + "/filesystem":
			result.Filesystems = append(result.Filesystems, FilesystemInfo{})
			target = &result.Filesystems[len(result.Filesystems)-1]
		case Namespace + "/webview":
			result.Webviews = append(result.Webviews, WebviewInfo{})
			target = &result.Webviews[len(result.Webviews)-1]
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
	sort.Slice(result.Shells, func(i, j int) bool { return hostPathKey(result.Shells[i].Path) < hostPathKey(result.Shells[j].Path) })
	sort.Slice(result.Filesystems, func(i, j int) bool {
		return hostPathKey(result.Filesystems[i].MountPoint) < hostPathKey(result.Filesystems[j].MountPoint)
	})
	sort.Slice(result.Webviews, func(i, j int) bool { return webviewIdentity(result.Webviews[i]) < webviewIdentity(result.Webviews[j]) })
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
	maxAge := requirements.MaxAge
	if maxAge == 0 {
		maxAge = 5 * time.Second
	}
	stale := func(kind string) bool {
		observed := snapshot.ObservedAt[kind]
		age := time.Since(observed)
		return observed.IsZero() || age < 0 || maxAge < 0 || age > maxAge
	}
	refresh := HostInventory{}
	searchPath := os.Getenv("PATH")
	if requirements.Shell != nil {
		if requirements.Shell.SearchPath != nil {
			searchPath = *requirements.Shell.SearchPath
		}
		if stale("shell") || snapshot.environment["shell"] != shellEnvironment(searchPath) {
			refresh.Shells, err = discoverShells(ctx, searchPath)
			if err != nil {
				return HostSnapshot{}, err
			}
			refresh.shellSearchPath = &searchPath
		}
	}
	if requirements.Filesystem != nil && (stale("filesystem") || snapshot.environment["filesystem"] != hostEnvironment()) {
		refresh.Filesystems, err = DiscoverFilesystems(ctx)
		if err != nil {
			return HostSnapshot{}, err
		}
	}
	if requirements.Webview != nil && (stale("webview") || snapshot.environment["webview"] != webviewEnvironment()) {
		refresh.Webviews, err = DiscoverWebviews(ctx)
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
	snapshot, err := g.hostForRequirements(ctx, requirements)
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
				if hostPathKey(observed.Path) == hostPathKey(selected.Path) {
					selected.Sources = observed.Sources
					selected.ConfiguredDefault = observed.ConfiguredDefault
				}
			}
			result.Shells = []ShellInfo{selected}
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
			filesystem, err := platformFilesystemAt(ctx, path)
			if err != nil {
				return HostCandidates{}, requirementCause(requirements, "filesystem", "", err)
			}
			filesystem.Source = redactFilesystemSource(filesystem.Source)
			items = []FilesystemInfo{filesystem}
		}
		for _, filesystem := range items {
			if required.Select != "" {
				selected, err := filepath.Abs(required.Select)
				if err != nil {
					return HostCandidates{}, err
				}
				if hostPathKey(filesystem.MountPoint) != hostPathKey(selected) {
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
func (g *Graph) PrepareHost(ctx context.Context, requirements OperationRequirements, validateWebview WebviewValidator) (PreparedHost, error) {
	if requirements.Webview != nil && validateWebview == nil {
		return PreparedHost{}, requirementError(requirements, "webview", "preparation requires a validator from the embedding backend")
	}
	requirements.MaxAge = -1 // Preparation never relies on cached installation evidence.
	candidates, err := g.ResolveHost(ctx, requirements)
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
		if hostPathKey(live.ResolvedPath) != hostPathKey(selected.ResolvedPath) {
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
		live, err := platformFilesystemAt(ctx, path)
		if err != nil {
			return PreparedHost{}, requirementCause(requirements, "filesystem", "", err)
		}
		live.Source = redactFilesystemSource(live.Source)
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
			if err := hostDirectoryWritable(path); err != nil {
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

func inspectShellExecutable(path string) (ShellInfo, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return ShellInfo{}, err
	}
	if !hostExecutable(path) {
		return ShellInfo{}, fmt.Errorf("shell executable is unavailable: %s", path)
	}
	if err := hostExecutableAccess(path); err != nil {
		return ShellInfo{}, fmt.Errorf("shell executable access: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ShellInfo{}, err
	}
	name := filepath.Base(path)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return ShellInfo{Name: name, Path: path, ResolvedPath: resolved, Sources: []string{"explicit-selection"}}, nil
}

func resolveShellExecutable(selection, searchPath string) (ShellInfo, error) {
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
			if hostExecutable(path) {
				return inspectShellExecutable(path)
			}
		}
	}
	return ShellInfo{}, fmt.Errorf("shell %q is not executable on the effective PATH", selection)
}

func chooseShell(items []ShellInfo, required ShellRequirement) (ShellInfo, error) {
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
				if hostPathKey(first.Path) == hostPathKey(item.Path) {
					return item, nil
				}
			}
		}
	}
	var configured *ShellInfo
	for i := range items {
		if items[i].ConfiguredDefault {
			if configured != nil {
				return ShellInfo{}, errors.New("multiple configured shells match; specify an executable")
			}
			configured = &items[i]
		}
	}
	if configured != nil {
		return *configured, nil
	}
	return ShellInfo{}, errors.New("multiple shells match; specify a name or executable")
}

func filesystemMatches(item FilesystemInfo, required FilesystemRequirement) bool {
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

func sameFilesystem(left, right FilesystemInfo) bool {
	return hostPathKey(left.MountPoint) == hostPathKey(right.MountPoint) && left.Source == right.Source && left.Type == right.Type
}

func webviewMatches(item WebviewInfo, required WebviewRequirement) bool {
	return (required.Select == "" || hostPathKey(required.Select) == hostPathKey(item.Location)) &&
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
