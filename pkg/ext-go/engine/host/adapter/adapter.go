package adapter

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
	"github.com/webong/ext/pkg/ext-go/engine/package"
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
)

const APIVersion = "2.0"

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
	"session.targets":          true, "session.endpoint": true, "session.connect": true, "session.navigate": true, "session.inject": true, "session.replay": true,
}
var validEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var validComputerHookEvent = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

var reservedNames = map[string]bool{
	"browser": true, "container": true, "profile": true, "shell": true, "env": true, "image": true,
	"volume": true, "build": true, "adapter": true, "share": true, "computer": true, "manager": true,
	"credential": true,
}

// knownRuntimes are the runtimes whose manifest rules and behavior this library
// defines. Any other well-formed runtime name is accepted but inert: a product
// ignores adapters whose runtime it does not understand.
var knownRuntimes = map[string]bool{"computer": true, "manager": true, "browser": true}

// IsKnownRuntime reports whether this library defines the runtime.
func IsKnownRuntime(runtime string) bool { return knownRuntimes[runtime] }

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
	Engine               []string
	Role                 []string
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
	// Package is the ext.package/v1 document the Manifest was derived from: a
	// template in a source directory, sealed in an installed or packaged one.
	Package packagekit.Manifest
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
	return &Store{Home: home, TrustFile: filepath.Join(home, "package-trust.json")}
}

func LoadDirectory(directory string) (*Adapter, error) {
	return LoadDirectoryForOS(directory, runtime.GOOS)
}

// LoadDirectoryForOS validates a package for a specific target operating system.
func LoadDirectoryForOS(directory, goos string) (*Adapter, error) {
	return loadDirectory(directory, goos, true)
}

// LoadSource validates the plugin.json template of an adapter source directory
// without requiring its executable to exist yet: a Go adapter is built before it
// can run. Everything else is checked as LoadDirectoryForOS does.
func LoadSource(directory, goos string) (*Adapter, error) {
	return loadDirectory(directory, goos, false)
}

func loadDirectory(directory, goos string, requireExecutable bool) (*Adapter, error) {
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
	pm, err := readPackage(absolute)
	if err != nil {
		return nil, err
	}
	values, err := valuesFromPackage(pm)
	if err != nil {
		return nil, fmt.Errorf("adapter manifest %s: %w", filepath.Join(absolute, ManifestFileName), err)
	}
	return adapterFromValues(values, pm, absolute, goos, requireExecutable)
}

// adapterFromValues derives the adapter's Manifest from the key/value form of its
// package document, applies the defaults, and validates it against directory.
func adapterFromValues(values map[string]string, pm packagekit.Manifest, absolute, goos string, requireExecutable bool) (*Adapter, error) {
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
		Engine:               splitList(values["engine"]),
		Role:                 splitList(values["role"]),
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
	if err := validateManifest(manifest, absolute, goos, requireExecutable); err != nil {
		return nil, err
	}
	return &Adapter{Directory: absolute, Manifest: manifest, Package: pm}, nil
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
	// Installed adapters and other executable packages use the same sealed
	// package verifier. Adapter-specific manifest and trust rules remain here.
	if sealed(loaded.Package) {
		packages, err := store.New(s.Home, store.Limits{})
		if err != nil {
			return nil, err
		}
		// A changed artifact must remain loadable so Verify can report the
		// distinct "changed" trust state; invocation still requires trust.
		if _, err := packages.Load(name); err != nil && !errors.Is(err, plugin.ErrMismatch) {
			return nil, err
		}
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

// HasSupport reports whether the manifest declares the resource kind in
// supports, such as "container" or "jvm".
func (a *Adapter) HasSupport(kind string) bool { return contains(a.Manifest.Supports, kind) }

// HasEngine reports whether the manifest declares an engine, such as "evaluator".
func (a *Adapter) HasEngine(engine string) bool { return contains(a.Manifest.Engine, engine) }

// HasRole reports whether the manifest declares a role, such as "installer".
func (a *Adapter) HasRole(role string) bool { return contains(a.Manifest.Role, role) }

func (a *Adapter) HasBrowserShare(operation string) bool {
	return a.IsRuntime("browser") && contains(a.Manifest.BrowserShare, operation)
}

func (a *Adapter) HasBrowserManagement(operation string) bool {
	return a.IsRuntime("browser") && contains(a.Manifest.BrowserManagement, operation)
}

// IsKnownRuntime reports whether the adapter's runtime is one this library
// defines; products should skip adapters for which it is false.
func (a *Adapter) IsKnownRuntime() bool { return IsKnownRuntime(a.Manifest.Runtime) }

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
	trust, err := s.readTrust()
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
	return s.verifySealedEntrypoint(a)
}

func (s *Store) Trust(a *Adapter) error {
	if err := s.verifySealedEntrypoint(a); err != nil {
		return err
	}
	checksum, err := a.Checksum()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.TrustFile), 0o700); err != nil {
		return err
	}
	file, err := s.trustFile()
	if err != nil {
		return err
	}
	return file.Record(adapterTrustScope, a.Manifest.Name, checksum)
}

// verifySealedEntrypoint binds an adapter's argv launcher to the reviewed
// package. Legacy hand-copied templates remain loadable for migration.
func (s *Store) verifySealedEntrypoint(a *Adapter) error {
	if !sealed(a.Package) {
		return nil
	}
	packages, err := store.New(s.Home, store.Limits{})
	if err != nil {
		return err
	}
	digest, err := a.Checksum()
	if err != nil {
		return err
	}
	selected, err := packages.ResolveEntrypoint(a.Manifest.Name, "ext.adapter", APIVersion, Protocol, digest)
	if err != nil {
		return err
	}
	if filepath.Clean(selected.Path) != filepath.Clean(a.ExecutablePath()) {
		return plugin.ErrMismatch
	}
	return nil
}

func (s *Store) RemoveTrust(name string) error {
	file, err := s.trustFile()
	if err != nil {
		return err
	}
	return file.Revoke(adapterTrustScope, name)
}

func (s *Store) Install(source string) (*Adapter, error) {
	loaded, err := LoadDirectory(source)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(s.Home, ".prepare-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := stageForInstall(loaded, staging); err != nil {
		return nil, err
	}
	packages, err := store.New(s.Home, store.Limits{})
	if err != nil {
		return nil, err
	}
	if _, err := packages.InstallWithOptions(staging, store.CopyOptions{PreserveExecutableArtifacts: true}); err != nil {
		return nil, err
	}
	return s.Load(loaded.Manifest.Name)
}

// stageForInstall copies a loaded adapter into staging and leaves it sealed and
// verified. A source directory holds a template: its files are copied and sealed
// with their digests for this platform. A package that is already
// sealed is copied whole, and must match its manifest exactly.
func stageForInstall(loaded *Adapter, staging string) error {
	if sealed(loaded.Package) {
		if err := verifyPackage(loaded.Directory, loaded.Package); err != nil {
			return fmt.Errorf("adapter %s does not match its manifest: %w", loaded.Manifest.Name, err)
		}
		return copyDirectory(loaded.Directory, staging)
	}
	// An installed adapter runs on this machine, so only its launcher for this
	// platform is installed; the other, if the template names one, is left out.
	// Every other file in the directory is installed and listed in the sealed
	// manifest.
	target := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if err := copyDirectory(loaded.Directory, staging); err != nil {
		return err
	}
	if _, dropped := pruneToTarget(loaded.Package, target); dropped != "" {
		if err := os.Remove(filepath.Join(staging, filepath.FromSlash(dropped))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if _, err := Seal(staging, target); err != nil {
		return err
	}
	staged, err := LoadDirectory(staging)
	if err != nil {
		return err
	}
	return verifyPackage(staged.Directory, staged.Package)
}

// Replace validates and seals an adapter, then delegates the atomic swap to
// the shared package store. The previous package survives a failed swap.
func (s *Store) Replace(source string) (*Adapter, error) {
	loaded, err := LoadDirectory(source)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(s.Home, ".prepare-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := stageForInstall(loaded, staging); err != nil {
		return nil, err
	}
	if _, err := LoadDirectory(staging); err != nil {
		return nil, err
	}
	packages, err := store.New(s.Home, store.Limits{})
	if err != nil {
		return nil, err
	}
	if _, err := packages.ReplaceWithOptions(staging, store.CopyOptions{PreserveExecutableArtifacts: true}); err != nil {
		return nil, err
	}
	return s.Load(loaded.Manifest.Name)
}

func (s *Store) Remove(name string) error {
	loaded, err := s.Load(name)
	if err != nil {
		return err
	}
	if filepath.Clean(loaded.Directory) != filepath.Join(filepath.Clean(s.Home), name) {
		return errors.New("refusing to remove adapter outside adapter home")
	}
	if sealed(loaded.Package) {
		packages, err := store.New(s.Home, store.Limits{})
		if err != nil {
			return err
		}
		if err := packages.Remove(name); err != nil {
			return err
		}
	} else if err := os.RemoveAll(loaded.Directory); err != nil {
		return err
	}
	return s.RemoveTrust(name)
}

func validateManifest(manifest Manifest, directory, goos string, requireExecutable bool) error {
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
	if manifest.APIVersion != APIVersion {
		return fmt.Errorf("adapter %s uses unsupported API %s (expected %s)", manifest.Name, manifest.APIVersion, APIVersion)
	}
	if !validName.MatchString(manifest.Name) || reservedNames[manifest.Name] {
		return fmt.Errorf("invalid or reserved adapter name %s", manifest.Name)
	}
	if !IsKnownRuntime(manifest.Runtime) && (!validName.MatchString(manifest.Runtime) || reservedNames[manifest.Runtime]) {
		return fmt.Errorf("adapter %s has invalid runtime %s", manifest.Name, manifest.Runtime)
	}
	if !IsKnownRuntime(manifest.Runtime) && manifest.Selectable {
		// Selection semantics belong to the product that defines the runtime.
		// Until a runtime is added to this library, adapters that declare it are
		// inert to products that do not know it: installable and trustable, but
		// never offered as a selection.
		return fmt.Errorf("adapter %s uses runtime %s, which this host does not define; set selectable = \"false\"", manifest.Name, manifest.Runtime)
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
	// A self-contained adapter is its own implementation and has no native
	// executable for the host to find. Only a computer adapter may still name
	// the commands it answers to (its computer_commands).
	if manifest.SelfContained && (len(manifest.Commands) != 0 || (len(manifest.ComputerCommands) != 0 && manifest.Runtime != "computer")) {
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
	if requireExecutable {
		info, err := os.Stat(executable)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("adapter executable is missing: %s", executable)
		}
		if goos != "windows" && runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("adapter executable is not executable: %s", executable)
		}
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
		case "list", "observe", "programs", "configure", "validate", "run", "doctor", "open", "build", "share",
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
	for field, values := range map[string][]string{"engine": manifest.Engine, "role": manifest.Role} {
		seen := map[string]bool{}
		for _, value := range values {
			if !validName.MatchString(value) || seen[value] {
				return fmt.Errorf("adapter %s declares invalid or duplicate %s %s", manifest.Name, field, value)
			}
			seen[value] = true
		}
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

const adapterTrustScope = "ctx.adapter/v2"

// trustFile migrates the old adapter-only ledger once. The scoped ledger is
// authoritative thereafter, including after a record is revoked.
func (s *Store) trustFile() (*hosttrust.File, error) {
	file, err := hosttrust.New(s.TrustFile)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.TrustFile); err == nil {
		return file, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	legacy, err := readLegacyTrust(filepath.Join(s.Home, "trust"))
	if err != nil {
		return nil, err
	}
	if len(legacy) > 0 {
		if err := os.MkdirAll(filepath.Dir(s.TrustFile), 0o700); err != nil {
			return nil, err
		}
		if err := file.ImportLegacy(adapterTrustScope, legacy); err != nil {
			return nil, err
		}
	}
	return file, nil
}

func (s *Store) readTrust() (map[string]string, error) {
	file, err := s.trustFile()
	if err != nil {
		return nil, err
	}
	records, err := file.Records(adapterTrustScope)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(records))
	for _, record := range records {
		out[record.ID] = record.Digest
	}
	return out, nil
}

func readLegacyTrust(path string) (map[string]string, error) {
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
