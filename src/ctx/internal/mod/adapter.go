package mod

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/webong/ext/pkg/plugin"
)

const APIVersion = "2.0"

const legacyAPIVersion = "1"
const legacyDecimalAPIVersion = "1.0"

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var validExecutable = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var validBrowserShareOperation = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
var validBrowserManagementOperation = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
var knownBrowserManagementOperations = map[string]bool{
	"extension.targets": true, "extension.capabilities": true, "extension.prepare": true,
	"extension.package": true, "extension.sign": true, "extension.stage": true,
	"extension.install": true, "extension.activate": true, "extension.store_install": true,
	"extension.convert": true, "extension.policy": true, "extension.store_remove": true, "extension.update_manifest": true, "userscript.prepare": true, "userscript.install": true,
	"userscript.update": true, "userscript.list": true, "userscript.describe": true,
	"userscript.enable": true, "userscript.disable": true, "userscript.uninstall": true,
	"userscript.activate": true, "bookmarklet.encode": true, "bookmarklet.decode": true,
	"bookmarklet.install_page": true,
	"session.targets":          true, "session.connect": true, "session.navigate": true, "session.inject": true, "session.replay": true,
}
var validEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var validComputerHookEvent = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

var reservedNames = map[string]bool{
	"browser": true, "container": true, "profile": true, "shell": true, "env": true, "image": true,
	"volume": true, "build": true, "adapter": true, "share": true, "computer": true, "manager": true,
	"credential": true,
}

type Manifest struct {
	APIVersion           string
	Name                 string
	Runtime              string
	Surfaces             []string
	Executable           string
	ExecutableWindows    string
	Description          string
	DisplayName          string
	Capabilities         []string
	Supports             []string
	SelectorKey          string
	Selectable           bool
	ExtraKeys            []string
	Commands             []string
	ComputerCommands     []string
	ComputerCapabilities []string
	ComputerHookEvents   []string
	ComputerHookDefaults []string
	ComputerHookSettings string
	ComputerHookTemplate string
	ShareSpaces          []string
	Dependencies         map[string]Dependency
	BrowserShare         []string
	BrowserQueryPriority int
	BrowserQueryAuto     bool
	OverrideEnv          []string
	DefaultProvider      bool
	SelfContained        bool
	BrowserManagement    []string
}

type Adapter struct {
	Directory string
	Manifest  Manifest
}

// DisplayLabel is presentation metadata, never an authentication identity.
func (a *Adapter) DisplayLabel() string {
	if a.Manifest.DisplayName != "" {
		return a.Manifest.DisplayName
	}
	return a.Manifest.Name
}

// Dependency binds a share space to a separately installed adapter using an
// exact adapter API version. The binding is platform-specific in the manifest.
type Dependency struct {
	Space      string
	Adapter    string
	APIVersion string
}

type Store struct {
	Home      string
	TrustFile string
}

func NewStore(home string) *Store {
	return &Store{Home: home, TrustFile: filepath.Join(home, "trust")}
}

func LoadDirectory(directory string) (*Adapter, error) {
	return LoadDirectoryForOS(directory, runtime.GOOS)
}

// LoadDirectoryForOS validates a package for a specific target operating system.
func LoadDirectoryForOS(directory, goos string) (*Adapter, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("adapter directory not found: %s", directory)
	}
	if err := rejectLinks(absolute); err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(absolute, "adapter.toml")
	values, err := parseManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{
		APIVersion:           values["api_version"],
		Name:                 values["name"],
		Runtime:              values["runtime"],
		Surfaces:             splitList(values["surfaces"]),
		Executable:           values["executable"],
		ExecutableWindows:    values["executable_windows"],
		Description:          values["description"],
		DisplayName:          values["display_name"],
		Capabilities:         splitList(values["capabilities"]),
		Supports:             splitList(values["supports"]),
		SelectorKey:          values["selector_key"],
		Selectable:           values["selectable"] != "false",
		ExtraKeys:            splitList(values["extra_keys"]),
		Commands:             splitList(values["commands"]),
		ComputerCommands:     splitList(values["computer_commands"]),
		ComputerCapabilities: splitList(values["computer_capabilities"]),
		ComputerHookEvents:   splitList(values["computer_hook_events"]),
		ComputerHookDefaults: splitList(values["computer_hook_default_events"]),
		ComputerHookSettings: values["computer_hook_settings_file"],
		ComputerHookTemplate: values["computer_hook_template"],
		ShareSpaces:          splitList(values["share_spaces"]),
		BrowserShare:         splitList(values["browser_share"]),
		BrowserQueryPriority: 1000,
		BrowserQueryAuto:     values["browser_query_auto"] != "false",
		OverrideEnv:          splitList(values["override_env"]),
		DefaultProvider:      values["default_provider"] == "true",
		SelfContained:        values["self_contained"] == "true",
	}
	for _, platform := range []string{"darwin", "linux", "windows"} {
		dependencies, err := parseDependencies(values["dependencies_"+platform], manifest.Name)
		if err != nil {
			return nil, err
		}
		if platform == goos {
			manifest.Dependencies = dependencies
		}
	}
	if raw := values["browser_query_priority"]; raw != "" {
		priority, err := strconv.Atoi(raw)
		if err != nil || priority < 0 || priority > 1000 {
			return nil, fmt.Errorf("adapter %s has invalid browser_query_priority %q (expected 0..1000)", manifest.Name, raw)
		}
		manifest.BrowserQueryPriority = priority
	}
	if raw := values["browser_query_auto"]; raw != "" && raw != "true" && raw != "false" {
		return nil, fmt.Errorf("adapter %s has invalid browser_query_auto %q", manifest.Name, raw)
	}
	if manifest.APIVersion == legacyAPIVersion || manifest.APIVersion == legacyDecimalAPIVersion {
		if err := normalizeLegacyManifest(&manifest, values["kind"]); err != nil {
			return nil, err
		}
	} else if values["kind"] != "" {
		return nil, fmt.Errorf("adapter %s uses removed manifest field kind; declare runtime and surfaces", manifest.Name)
	}
	if manifest.Runtime != "browser" && (values["browser_query_priority"] != "" || values["browser_query_auto"] != "") {
		return nil, fmt.Errorf("adapter %s browser query preferences require the browser runtime", manifest.Name)
	}
	if manifest.SelectorKey == "" && manifest.Selectable && !hasComputerEndpoint(manifest) {
		if manifest.Runtime == "browser" {
			manifest.SelectorKey = "browser"
		} else {
			manifest.SelectorKey = manifest.Name
		}
	}
	if value := values["self_contained"]; value != "" && value != "true" && value != "false" {
		return nil, fmt.Errorf("adapter %s has invalid self_contained value %s", manifest.Name, value)
	}
	if value := values["selectable"]; value != "" && value != "true" && value != "false" {
		return nil, fmt.Errorf("adapter %s has invalid selectable value %s", manifest.Name, value)
	}
	if len(manifest.Commands) == 0 && manifest.SelectorKey != "" && manifest.Runtime != "browser" && !manifest.SelfContained {
		manifest.Commands = []string{manifest.Name}
	}
	if contains(manifest.Capabilities, "share") && len(manifest.ShareSpaces) == 0 {
		manifest.ShareSpaces = []string{manifest.Name}
	}
	manifest.BrowserManagement = splitList(values["browser_management"])
	if err := validateManifest(manifest, absolute, goos); err != nil {
		return nil, err
	}
	return &Adapter{Directory: absolute, Manifest: manifest}, nil
}

func parseDependencies(raw, owner string) (map[string]Dependency, error) {
	dependencies := map[string]Dependency{}
	for _, entry := range splitList(raw) {
		space, target, ok := strings.Cut(entry, ":")
		name, version, hasVersion := strings.Cut(target, "@")
		if !ok || !hasVersion || !validName.MatchString(space) || !validName.MatchString(name) ||
			reservedNames[name] || name == owner || version != APIVersion || dependencies[space].Space != "" {
			return nil, fmt.Errorf("adapter %s has invalid dependency %q; expected space:name@%s", owner, entry, APIVersion)
		}
		dependencies[space] = Dependency{Space: space, Adapter: name, APIVersion: version}
	}
	return dependencies, nil
}

func (s *Store) Load(name string) (*Adapter, error) {
	if !validName.MatchString(name) || reservedNames[name] {
		return nil, fmt.Errorf("invalid adapter name %s", name)
	}
	loaded, err := LoadDirectory(filepath.Join(s.Home, name))
	if err != nil {
		return nil, err
	}
	if loaded.Manifest.Name != name {
		return nil, fmt.Errorf("adapter directory %s contains manifest for %s", name, loaded.Manifest.Name)
	}
	return loaded, nil
}

func (s *Store) List() ([]*Adapter, error) {
	entries, err := os.ReadDir(s.Home)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]*Adapter, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		loaded, err := s.Load(entry.Name())
		if err != nil {
			continue
		}
		result = append(result, loaded)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Manifest.Name < result[j].Manifest.Name })
	return result, nil
}

func (a *Adapter) HasCapability(capability string) bool {
	d := plugin.AdapterDescriptor(plugin.Identity{}, a.pluginOperations())
	_, err := d.Lookup(plugin.ContractRef{Name: plugin.AdapterContractName, Version: plugin.AdapterAPIVersion}, capability)
	return err == nil
}

// ValidateDependency checks the declared API and capability of a runtime
// dependency. Trust is checked separately against the installed package.
func ValidateDependency(dependency Dependency, target *Adapter) error {
	if target.Manifest.Name != dependency.Adapter || target.Manifest.APIVersion != dependency.APIVersion ||
		!target.HasCapability("share") || !contains(target.Manifest.ShareSpaces, dependency.Space) {
		return fmt.Errorf("adapter %s does not satisfy %s:%s@%s", target.Manifest.Name,
			dependency.Space, dependency.Adapter, dependency.APIVersion)
	}
	return nil
}

func (a *Adapter) HasBrowserShare(operation string) bool {
	return a.IsRuntime("browser") && contains(a.Manifest.BrowserShare, operation)
}

func (a *Adapter) HasBrowserManagement(operation string) bool {
	return a.IsRuntime("browser") && contains(a.Manifest.BrowserManagement, operation)
}

func (a *Adapter) IsRuntime(runtimeName string) bool {
	return a.Manifest.Runtime == runtimeName
}

func (a *Adapter) SupportsSurface(surface string) bool {
	return contains(a.Manifest.Surfaces, surface)
}

func (a *Adapter) IsSelectable() bool {
	return a.Manifest.SelectorKey != ""
}

func (a *Adapter) HasCommand(command string) bool {
	return contains(a.Manifest.Commands, command)
}

func (a *Adapter) HasComputerCommand(command string) bool {
	return contains(a.Manifest.ComputerCommands, command)
}

func (a *Adapter) HasComputerCapability(capability string) bool {
	return contains(a.Manifest.ComputerCapabilities, capability)
}

func (a *Adapter) IsComputerEndpoint() bool {
	return hasComputerEndpoint(a.Manifest)
}

func hasComputerEndpoint(manifest Manifest) bool {
	return len(manifest.ComputerCommands) > 0 || len(manifest.ComputerCapabilities) > 0
}

func normalizeLegacyManifest(manifest *Manifest, kind string) error {
	if kind == "" {
		if hasComputerEndpoint(*manifest) {
			manifest.Runtime = "computer"
			manifest.Surfaces = []string{"shell"}
			return nil
		}
		kind = "selector"
	}
	switch kind {
	case "selector":
		manifest.Runtime = "computer"
		manifest.Surfaces = []string{"shell"}
	case "browser":
		manifest.Runtime = "browser"
		manifest.Surfaces = []string{"web"}
	case "container":
		manifest.Runtime = "manager"
		manifest.Surfaces = []string{"shell"}
	default:
		return fmt.Errorf("adapter %s has invalid legacy kind %s", manifest.Name, kind)
	}
	return nil
}

func (a *Adapter) ConfigKeys() []string {
	keys := append([]string(nil), a.Manifest.ExtraKeys...)
	if a.Manifest.SelectorKey != "" {
		keys = append([]string{a.Manifest.SelectorKey}, keys...)
	}
	return keys
}

func (a *Adapter) ExecutablePath() string {
	return a.ExecutablePathForOS(runtime.GOOS)
}

func (a *Adapter) ExecutablePathForOS(goos string) string {
	executable := a.Manifest.Executable
	if goos == "windows" && a.Manifest.ExecutableWindows != "" {
		executable = a.Manifest.ExecutableWindows
	}
	return filepath.Join(a.Directory, executable)
}

func (a *Adapter) Checksum() (string, error) {
	return plugin.DirectoryDigest(a.Directory)
}

func (s *Store) IsTrusted(a *Adapter) (bool, error) {
	descriptor, err := a.PluginDescriptor()
	if err != nil {
		return false, err
	}
	trust, err := readTrust(s.TrustFile)
	if err != nil {
		return false, err
	}
	return trust[a.Manifest.Name] == strings.TrimPrefix(descriptor.Identity.Revision, "sha256:"), nil
}

func (s *Store) AssertTrusted(a *Adapter) error {
	trusted, err := s.IsTrusted(a)
	if err != nil {
		return err
	}
	if !trusted {
		return fmt.Errorf("adapter %s is not trusted or changed; run ctx adapter trust %s after reviewing it", a.Manifest.Name, a.Manifest.Name)
	}
	return nil
}

func (s *Store) Trust(a *Adapter) error {
	checksum, err := a.Checksum()
	if err != nil {
		return err
	}
	trust, err := readTrust(s.TrustFile)
	if err != nil {
		return err
	}
	trust[a.Manifest.Name] = checksum
	return writeTrust(s.TrustFile, trust)
}

func (s *Store) RemoveTrust(name string) error {
	trust, err := readTrust(s.TrustFile)
	if err != nil {
		return err
	}
	delete(trust, name)
	return writeTrust(s.TrustFile, trust)
}

func (s *Store) Install(source string) (*Adapter, error) {
	loaded, err := LoadDirectory(source)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return nil, err
	}
	target := filepath.Join(s.Home, loaded.Manifest.Name)
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("adapter %s is already installed", loaded.Manifest.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	staging, err := os.MkdirTemp(s.Home, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := copyDirectory(loaded.Directory, staging); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, target); err != nil {
		return nil, err
	}
	return s.Load(loaded.Manifest.Name)
}

// Replace validates and stages an adapter before swapping it into the store.
// If the final rename fails, the previous adapter is restored.
func (s *Store) Replace(source string) (*Adapter, error) {
	loaded, err := LoadDirectory(source)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(s.Home, ".replace-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := copyDirectory(loaded.Directory, staging); err != nil {
		return nil, err
	}
	if _, err := LoadDirectory(staging); err != nil {
		return nil, err
	}

	target := filepath.Join(s.Home, loaded.Manifest.Name)
	backupRoot, err := os.MkdirTemp(s.Home, ".backup-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(backupRoot)
	backup := filepath.Join(backupRoot, loaded.Manifest.Name)
	hadTarget := false
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return nil, err
		}
		hadTarget = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(staging, target); err != nil {
		if hadTarget {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return nil, errors.Join(err, fmt.Errorf("restore previous adapter: %w", restoreErr))
			}
		}
		return nil, err
	}
	replaced, err := s.Load(loaded.Manifest.Name)
	if err == nil {
		return replaced, nil
	}
	if removeErr := os.RemoveAll(target); removeErr != nil {
		return nil, errors.Join(err, fmt.Errorf("remove invalid replacement: %w", removeErr))
	}
	if hadTarget {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return nil, errors.Join(err, fmt.Errorf("restore previous adapter: %w", restoreErr))
		}
	}
	return nil, err
}

func (s *Store) Remove(name string) error {
	loaded, err := s.Load(name)
	if err != nil {
		return err
	}
	if filepath.Clean(loaded.Directory) != filepath.Join(filepath.Clean(s.Home), name) {
		return errors.New("refusing to remove adapter outside adapter home")
	}
	if err := os.RemoveAll(loaded.Directory); err != nil {
		return err
	}
	return s.RemoveTrust(name)
}

func parseManifest(path string) (map[string]string, error) {
	input, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("adapter manifest not found: %s", path)
	}
	defer input.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		raw := strings.TrimSpace(parts[1])
		value, err := strconv.Unquote(raw)
		if err != nil {
			continue
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func validateManifest(manifest Manifest, directory, goos string) error {
	if manifest.DisplayName != "" {
		if !utf8.ValidString(manifest.DisplayName) || utf8.RuneCountInString(manifest.DisplayName) > 80 ||
			strings.TrimSpace(manifest.DisplayName) != manifest.DisplayName {
			return fmt.Errorf("adapter %s has invalid display_name", manifest.Name)
		}
		for _, character := range manifest.DisplayName {
			if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
				return fmt.Errorf("adapter %s has invalid display_name", manifest.Name)
			}
		}
	}
	if manifest.APIVersion != APIVersion && manifest.APIVersion != legacyAPIVersion && manifest.APIVersion != legacyDecimalAPIVersion {
		return fmt.Errorf("adapter %s uses unsupported API %s (expected %s)", manifest.Name, manifest.APIVersion, APIVersion)
	}
	if !validName.MatchString(manifest.Name) || reservedNames[manifest.Name] {
		return fmt.Errorf("invalid or reserved adapter name %s", manifest.Name)
	}
	if manifest.Runtime != "computer" && manifest.Runtime != "manager" && manifest.Runtime != "browser" {
		return fmt.Errorf("adapter %s has invalid runtime %s", manifest.Name, manifest.Runtime)
	}
	if len(manifest.Surfaces) == 0 {
		return fmt.Errorf("adapter %s must declare at least one surface", manifest.Name)
	}
	seenSurfaces := map[string]bool{}
	for _, surface := range manifest.Surfaces {
		if surface != "shell" && surface != "web" || seenSurfaces[surface] {
			return fmt.Errorf("adapter %s has invalid or duplicate surface %s", manifest.Name, surface)
		}
		seenSurfaces[surface] = true
	}
	if hasComputerEndpoint(manifest) && manifest.Runtime != "computer" {
		return fmt.Errorf("adapter %s computer endpoint requires the computer runtime", manifest.Name)
	}
	if manifest.DefaultProvider && manifest.Runtime != "manager" {
		return fmt.Errorf("adapter %s can only be a default provider for the manager runtime", manifest.Name)
	}
	if manifest.SelfContained && (len(manifest.Commands) != 0 || len(manifest.ComputerCommands) != 0) {
		return fmt.Errorf("adapter %s cannot declare native commands when self_contained is true", manifest.Name)
	}
	if manifest.Selectable && !hasComputerEndpoint(manifest) && manifest.SelectorKey == "" {
		return fmt.Errorf("adapter %s must declare a selector key", manifest.Name)
	}
	if !manifest.Selectable && manifest.SelectorKey != "" {
		return fmt.Errorf("adapter %s cannot declare selector_key when selectable is false", manifest.Name)
	}
	if manifest.SelectorKey != "" && !validName.MatchString(manifest.SelectorKey) {
		return fmt.Errorf("adapter %s has invalid selector key %s", manifest.Name, manifest.SelectorKey)
	}
	for _, key := range append(append(append([]string{}, manifest.ExtraKeys...), manifest.Commands...), manifest.ComputerCommands...) {
		if !validName.MatchString(key) {
			return fmt.Errorf("adapter %s has invalid key or command %s", manifest.Name, key)
		}
	}
	seenComputerCapabilities := map[string]bool{}
	for _, capability := range manifest.ComputerCapabilities {
		if capability != "hook" && capability != "plugin" || seenComputerCapabilities[capability] {
			return fmt.Errorf("adapter %s has invalid or duplicate computer capability %s", manifest.Name, capability)
		}
		seenComputerCapabilities[capability] = true
	}
	if manifest.ComputerHookSettings != "" || manifest.ComputerHookTemplate != "" || len(manifest.ComputerHookEvents) > 0 || len(manifest.ComputerHookDefaults) > 0 {
		if manifest.Runtime != "computer" || !contains(manifest.ComputerCapabilities, "hook") ||
			manifest.ComputerHookSettings == "" || manifest.ComputerHookTemplate == "" || len(manifest.ComputerHookEvents) == 0 {
			return fmt.Errorf("adapter %s project hook setup requires a computer hook capability, settings file, template, and events", manifest.Name)
		}
		if !validPackagePath(manifest.ComputerHookSettings) {
			return fmt.Errorf("adapter %s has an invalid computer hook settings path", manifest.Name)
		}
		if !validPackagePath(manifest.ComputerHookTemplate) {
			return fmt.Errorf("adapter %s has an invalid computer hook template path", manifest.Name)
		}
		templatePath := filepath.Join(directory, filepath.FromSlash(manifest.ComputerHookTemplate))
		if info, err := os.Stat(templatePath); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("adapter %s hook template is missing or not a regular file", manifest.Name)
		}
		seenEvents := map[string]bool{}
		for _, event := range manifest.ComputerHookEvents {
			if !validComputerHookEvent.MatchString(event) || seenEvents[event] {
				return fmt.Errorf("adapter %s has invalid or duplicate computer hook event %s", manifest.Name, event)
			}
			seenEvents[event] = true
		}
		seenDefaults := map[string]bool{}
		for _, event := range manifest.ComputerHookDefaults {
			if !seenEvents[event] || seenDefaults[event] {
				return fmt.Errorf("adapter %s has an invalid or duplicate default computer hook event %s", manifest.Name, event)
			}
			seenDefaults[event] = true
		}
		contents, err := os.ReadFile(templatePath)
		if err != nil {
			return fmt.Errorf("read adapter %s hook template: %w", manifest.Name, err)
		}
		if !strings.Contains(string(contents), "{{event}}") || !strings.Contains(string(contents), "{{adapter}}") {
			return fmt.Errorf("adapter %s hook template must use {{event}} and {{adapter}} placeholders", manifest.Name)
		}
		rendered := strings.NewReplacer("{{event}}", manifest.ComputerHookEvents[0], "{{adapter}}", manifest.Name).Replace(string(contents))
		var templateJSON map[string]any
		if err := json.Unmarshal([]byte(rendered), &templateJSON); err != nil {
			return fmt.Errorf("adapter %s hook template is not a JSON object: %w", manifest.Name, err)
		}
	}
	if len(manifest.ShareSpaces) > 0 && !contains(manifest.Capabilities, "share") {
		return fmt.Errorf("adapter %s declares share spaces without the share capability", manifest.Name)
	}
	if len(manifest.BrowserShare) > 0 && (manifest.Runtime != "browser" || !contains(manifest.Capabilities, "share")) {
		return fmt.Errorf("adapter %s browser_share requires a browser runtime with share capability", manifest.Name)
	}
	if len(manifest.BrowserManagement) > 0 && (manifest.Runtime != "browser" || !contains(manifest.Capabilities, "share")) {
		return fmt.Errorf("adapter %s browser_management requires a browser runtime with share capability", manifest.Name)
	}
	seenBrowserManagement := map[string]bool{}
	for _, operation := range manifest.BrowserManagement {
		if !validBrowserManagementOperation.MatchString(operation) || !knownBrowserManagementOperations[operation] || seenBrowserManagement[operation] {
			return fmt.Errorf("adapter %s has invalid or duplicate browser management operation %s", manifest.Name, operation)
		}
		seenBrowserManagement[operation] = true
	}
	seenBrowserShare := map[string]bool{}
	for _, operation := range manifest.BrowserShare {
		if !validBrowserShareOperation.MatchString(operation) || seenBrowserShare[operation] {
			return fmt.Errorf("adapter %s has invalid browser share operation %s", manifest.Name, operation)
		}
		seenBrowserShare[operation] = true
	}
	seenShareSpaces := map[string]bool{}
	for _, space := range manifest.ShareSpaces {
		if !validName.MatchString(space) || space == "manager" || space == "container" || (space == "browser" && manifest.Runtime != "browser") || space == "computer" || seenShareSpaces[space] {
			return fmt.Errorf("adapter %s has invalid or duplicate share space %s", manifest.Name, space)
		}
		seenShareSpaces[space] = true
	}
	for _, key := range manifest.OverrideEnv {
		if !validEnvironmentName.MatchString(key) {
			return fmt.Errorf("adapter %s has invalid override environment variable %s", manifest.Name, key)
		}
	}
	if !validExecutableName(manifest.Executable) ||
		(manifest.ExecutableWindows != "" && !validExecutableName(manifest.ExecutableWindows)) {
		return fmt.Errorf("adapter %s has an invalid executable name", manifest.Name)
	}
	executableName := manifest.Executable
	if goos == "windows" && manifest.ExecutableWindows != "" {
		executableName = manifest.ExecutableWindows
	}
	executable := filepath.Join(directory, executableName)
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("adapter executable is missing: %s", executable)
	}
	if goos != "windows" && runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("adapter executable is not executable: %s", executable)
	}
	if !contains(manifest.Capabilities, "validate") || !contains(manifest.Capabilities, "doctor") {
		return fmt.Errorf("adapter %s must provide validate and doctor", manifest.Name)
	}
	if !contains(manifest.Capabilities, "run") && !contains(manifest.Capabilities, "open") && !contains(manifest.Capabilities, "share") {
		return fmt.Errorf("adapter %s must provide run, open, or share", manifest.Name)
	}
	if contains(manifest.Capabilities, "run") && !contains(manifest.Surfaces, "shell") {
		return fmt.Errorf("adapter %s provides run without the shell surface", manifest.Name)
	}
	if contains(manifest.Capabilities, "open") && !contains(manifest.Surfaces, "web") {
		return fmt.Errorf("adapter %s provides open without the web surface", manifest.Name)
	}
	if (len(manifest.Commands) > 0 || len(manifest.ComputerCommands) > 0) && !contains(manifest.Surfaces, "shell") {
		return fmt.Errorf("adapter %s declares commands without the shell surface", manifest.Name)
	}
	if len(manifest.ComputerCommands) > 0 && !contains(manifest.Capabilities, "run") {
		return fmt.Errorf("adapter %s declares computer commands without the run capability", manifest.Name)
	}
	for _, capability := range manifest.Capabilities {
		switch capability {
		case "list", "observe", "configure", "validate", "run", "doctor", "open", "build", "share",
			"image_push", "image_pull", "image_save", "image_load",
			"volume_exists", "volume_create", "volume_export", "volume_import":
		default:
			return fmt.Errorf("adapter %s declares unknown capability %s", manifest.Name, capability)
		}
	}
	seenSupports := map[string]bool{}
	for _, kind := range manifest.Supports {
		if !validName.MatchString(kind) || seenSupports[kind] {
			return fmt.Errorf("adapter %s declares invalid or duplicate support %s", manifest.Name, kind)
		}
		seenSupports[kind] = true
	}
	return nil
}

func validExecutableName(name string) bool {
	return validExecutable.MatchString(name)
}

func validPackagePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func rejectLinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("adapter packages cannot contain symbolic links: %s", path)
		}
		return nil
	})
}

func copyDirectory(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported adapter package entry: %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			input.Close()
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		return closeErr
	})
}

func readTrust(path string) (map[string]string, error) {
	result := map[string]string{}
	input, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	defer input.Close()
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 {
			result[fields[0]] = fields[1]
		}
	}
	return result, scanner.Err()
}

func writeTrust(path string, trust map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".trust-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	names := make([]string, 0, len(trust))
	for name := range trust {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := fmt.Fprintf(temporary, "%s %s\n", name, trust[name]); err != nil {
			temporary.Close()
			return err
		}
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, path)
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
