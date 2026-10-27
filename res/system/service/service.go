// Package service defines portable per-user OS service definitions and their
// registration lifecycle. Rendering is pure; publication never replaces an
// existing definition; registration, start, stop, and status are delegated to a
// caller-selected manager.
//
// The package owns no product name, installation layout, data directory, port,
// key provider, endpoint, or readiness protocol. The consumer supplies every
// value that identifies its service.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Platform selects the native definition format. It is never inferred from the
// runtime inside this package.
type Platform string

const (
	PlatformDarwin  Platform = "darwin"
	PlatformLinux   Platform = "linux"
	PlatformWindows Platform = "windows"
)

// RestartPolicy is the caller's restart choice. Platforms that cannot express a
// policy exactly fail instead of silently substituting another behavior.
type RestartPolicy string

const (
	RestartNever     RestartPolicy = "never"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

var (
	// ErrInvalid reports a malformed specification or definition.
	ErrInvalid = errors.New("invalid service definition")
	// ErrExists reports a different definition already at the target path.
	ErrExists = errors.New("a different service definition already exists")
	// ErrStale reports that the published definition no longer matches the
	// specification the caller asked to act on.
	ErrStale = errors.New("published service definition changed")
	// ErrUnsafe reports a link, non-directory, or broadly writable location.
	ErrUnsafe = errors.New("unsafe service path")
	// ErrUnsupported reports a platform or capability the backend cannot honor.
	ErrUnsupported = errors.New("unsupported service platform")
	// ErrRegistered reports a service the manager already knows.
	ErrRegistered = errors.New("service is already registered")
	// ErrNotInstalled reports lifecycle work on a definition nobody published.
	ErrNotInstalled = errors.New("service definition is not installed")
)

const (
	maxNameLength         = 128
	maxDefinitionBytes    = 1 << 20
	maxEnvironmentEntries = 128
	maxArguments          = 256
)

// Spec is the complete caller-supplied service definition.
type Spec struct {
	// Name is the service identity: a launchd label, a systemd unit name, or a
	// Task Scheduler task name.
	Name string
	// Description is human-readable metadata; it defaults to Name.
	Description string
	// Executable is an absolute local path.
	Executable string
	// Arguments are passed verbatim to the executable.
	Arguments []string
	// Environment is rendered only on platforms that support it. Windows Task
	// Scheduler has no environment field, so a non-empty map fails there.
	Environment map[string]string
	// WorkingDirectory is an optional absolute local path.
	WorkingDirectory string
	// Restart selects the manager's restart behavior.
	Restart RestartPolicy
	// RestartSeconds delays a restart. Zero selects five seconds.
	RestartSeconds int
	// UserID is the explicit Windows task account name or SID.
	UserID string
}

// Definition is a rendered native service definition. Path is empty until the
// definition is published.
type Definition struct {
	Platform Platform
	Identity string
	FileName string
	Content  string
	Digest   string
	Path     string
}

// Publication reports a bounded, non-overwriting publish attempt.
type Publication struct {
	Path     string
	Created  bool
	Existing bool
}

// Registration reports publication and manager registration separately so a
// caller can recover when the manager rejects an otherwise valid definition.
type Registration struct {
	Definition  Definition
	Publication Publication
	Registered  bool
}

// Inspection is the on-disk state of one expected definition.
type Inspection struct {
	Path      string
	Installed bool
	Matches   bool
	Digest    string
}

// Status is a point-in-time projection. Ready is filled by a Service wrapper's
// readiness callback; a manager never infers readiness from being active.
type Status struct {
	Name             string            `json:"name"`
	Manager          string            `json:"manager"`
	DefinitionPath   string            `json:"definition_path,omitempty"`
	DefinitionDigest string            `json:"definition_digest,omitempty"`
	Installed        bool              `json:"installed"`
	Stale            bool              `json:"stale"`
	Registered       bool              `json:"registered"`
	Active           bool              `json:"active"`
	Detail           string            `json:"detail"`
	CheckedAt        time.Time         `json:"checked_at"`
	Ready            *bool             `json:"ready,omitempty"`
	Attributes       map[string]string `json:"attributes,omitempty"`
}

// CommandRunner is the native command boundary. Output is captured, not
// inherited, so a manager cannot write into the host application's streams.
type CommandRunner func(ctx context.Context, directory, executable string, args ...string) ([]byte, error)

// Manager is implemented by a per-user service backend. It never selects its
// own identity, directory, or executable.
type Manager interface {
	Platform() Platform
	Name() string
	Register(ctx context.Context, spec Spec) (Registration, error)
	Unregister(ctx context.Context, spec Spec) error
	Start(ctx context.Context, spec Spec) error
	Stop(ctx context.Context, spec Spec) error
	Status(ctx context.Context, spec Spec) (Status, error)
}

// Readiness is an optional caller probe. The library has no protocol, port, or
// endpoint of its own.
type Readiness func(context.Context) error

// Service binds one specification to one manager and an optional readiness
// probe.
type Service struct {
	manager   Manager
	spec      Spec
	readiness Readiness
}

func New(manager Manager, spec Spec, readiness Readiness) (*Service, error) {
	if manager == nil {
		return nil, fmt.Errorf("%w: manager is required", ErrInvalid)
	}
	spec, err := ValidateSpec(spec, manager.Platform())
	if err != nil {
		return nil, err
	}
	return &Service{manager: manager, spec: spec, readiness: readiness}, nil
}

func (s *Service) Spec() Spec { return s.spec }

func (s *Service) Register(ctx context.Context) (Registration, error) {
	return s.manager.Register(ctx, s.spec)
}

func (s *Service) Unregister(ctx context.Context) error {
	return s.manager.Unregister(ctx, s.spec)
}

func (s *Service) Start(ctx context.Context) error { return s.manager.Start(ctx, s.spec) }
func (s *Service) Stop(ctx context.Context) error  { return s.manager.Stop(ctx, s.spec) }

// Status inspects the manager and then, when configured, the caller's readiness
// probe. A probe error reports not ready and is included in the detail.
func (s *Service) Status(ctx context.Context) (Status, error) {
	status, err := s.manager.Status(ctx, s.spec)
	if err != nil {
		return status, err
	}
	if s.readiness != nil {
		ready := s.readiness(ctx) == nil
		status.Ready = &ready
	}
	return status, nil
}

// ValidateSpec resolves defaults and checks every value the renderer uses. It is
// exported so a backend can validate before touching the filesystem.
func ValidateSpec(spec Spec, platform Platform) (Spec, error) {
	if platform != PlatformDarwin && platform != PlatformLinux && platform != PlatformWindows {
		return spec, fmt.Errorf("%w: unknown platform %q", ErrUnsupported, platform)
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if !validServiceName(spec.Name, platform) {
		return spec, fmt.Errorf("%w: service name %q is not valid for %s", ErrInvalid, spec.Name, platform)
	}
	if spec.Description == "" {
		spec.Description = spec.Name
	}
	if !validText(spec.Description) || len(spec.Description) > 1024 {
		return spec, fmt.Errorf("%w: description must be valid text of at most 1024 bytes", ErrInvalid)
	}
	if !absolutePath(platform, spec.Executable) {
		return spec, fmt.Errorf("%w: executable must be an absolute local path", ErrInvalid)
	}
	if spec.WorkingDirectory != "" && !absolutePath(platform, spec.WorkingDirectory) {
		return spec, fmt.Errorf("%w: working directory must be an absolute local path", ErrInvalid)
	}
	if len(spec.Arguments) > maxArguments {
		return spec, fmt.Errorf("%w: at most %d arguments are supported", ErrInvalid, maxArguments)
	}
	for _, argument := range spec.Arguments {
		if !validText(argument) || len(argument) > 4096 {
			return spec, fmt.Errorf("%w: arguments must be valid text of at most 4096 bytes", ErrInvalid)
		}
	}
	if len(spec.Environment) > maxEnvironmentEntries {
		return spec, fmt.Errorf("%w: at most %d environment entries are supported", ErrInvalid, maxEnvironmentEntries)
	}
	for key, value := range spec.Environment {
		if !validEnvironmentName(key) {
			return spec, fmt.Errorf("%w: invalid environment name %q", ErrInvalid, key)
		}
		if !validText(value) || len(value) > 4096 {
			return spec, fmt.Errorf("%w: invalid environment value for %q", ErrInvalid, key)
		}
	}
	if platform == PlatformWindows && len(spec.Environment) > 0 {
		return spec, fmt.Errorf("%w: Task Scheduler cannot express environment variables", ErrUnsupported)
	}
	switch spec.Restart {
	case "":
		spec.Restart = RestartOnFailure
	case RestartNever, RestartOnFailure, RestartAlways:
	default:
		return spec, fmt.Errorf("%w: unknown restart policy %q", ErrInvalid, spec.Restart)
	}
	if spec.RestartSeconds == 0 {
		spec.RestartSeconds = 5
	}
	if spec.RestartSeconds < 0 || spec.RestartSeconds > 3600 {
		return spec, fmt.Errorf("%w: restart delay must be between 0 and 3600 seconds", ErrInvalid)
	}
	if platform == PlatformWindows {
		if !validText(spec.UserID) || len(spec.UserID) > 256 {
			return spec, fmt.Errorf("%w: Windows service rendering requires a user ID", ErrInvalid)
		}
	}
	return spec, nil
}

// Render produces the native definition for a platform without touching the
// filesystem.
func Render(spec Spec, platform Platform) (Definition, error) {
	spec, err := ValidateSpec(spec, platform)
	if err != nil {
		return Definition{}, err
	}
	switch platform {
	case PlatformDarwin:
		return renderLaunchd(spec)
	case PlatformLinux:
		return renderSystemd(spec)
	case PlatformWindows:
		return renderTaskScheduler(spec)
	}
	return Definition{}, fmt.Errorf("%w: unknown platform %q", ErrUnsupported, platform)
}

func definition(platform Platform, identity, fileName, content string) (Definition, error) {
	if len(content) > maxDefinitionBytes {
		return Definition{}, fmt.Errorf("%w: definition exceeds %d bytes", ErrInvalid, maxDefinitionBytes)
	}
	digest := sha256.Sum256([]byte(content))
	return Definition{
		Platform: platform, Identity: identity, FileName: fileName,
		Content: content, Digest: hex.EncodeToString(digest[:]),
	}, nil
}

func renderLaunchd(spec Spec) (Definition, error) {
	var out strings.Builder
	out.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\"><dict>\n")
	pair := func(key, value string) {
		fmt.Fprintf(&out, "<key>%s</key><string>%s</string>\n", xmlText(key), xmlText(value))
	}
	pair("Label", spec.Name)
	out.WriteString("<key>ProgramArguments</key><array>")
	for _, argument := range append([]string{spec.Executable}, spec.Arguments...) {
		fmt.Fprintf(&out, "<string>%s</string>", xmlText(argument))
	}
	out.WriteString("</array>\n")
	if spec.WorkingDirectory != "" {
		pair("WorkingDirectory", spec.WorkingDirectory)
	}
	if len(spec.Environment) > 0 {
		out.WriteString("<key>EnvironmentVariables</key><dict>\n")
		for _, key := range sortedKeys(spec.Environment) {
			pair(key, spec.Environment[key])
		}
		out.WriteString("</dict>\n")
	}
	out.WriteString("<key>RunAtLoad</key><true/>\n")
	switch spec.Restart {
	case RestartAlways:
		out.WriteString("<key>KeepAlive</key><true/>\n")
	case RestartOnFailure:
		out.WriteString("<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n")
	default:
		out.WriteString("<key>KeepAlive</key><false/>\n")
	}
	fmt.Fprintf(&out, "<key>ThrottleInterval</key><integer>%d</integer>\n", spec.RestartSeconds)
	out.WriteString("</dict></plist>\n")
	return definition(PlatformDarwin, spec.Name, spec.Name+".plist", out.String())
}

func renderSystemd(spec Spec) (Definition, error) {
	unit := systemdUnitName(spec.Name)
	var out strings.Builder
	out.WriteString("[Unit]\n")
	fmt.Fprintf(&out, "Description=%s\n\n[Service]\n", systemdValue(spec.Description))
	command := make([]string, 0, len(spec.Arguments)+1)
	command = append(command, spec.Executable)
	command = append(command, spec.Arguments...)
	for index := range command {
		command[index] = systemdQuote(command[index])
	}
	fmt.Fprintf(&out, "ExecStart=%s\n", strings.Join(command, " "))
	if spec.WorkingDirectory != "" {
		fmt.Fprintf(&out, "WorkingDirectory=%s\n", systemdQuote(spec.WorkingDirectory))
	}
	for _, key := range sortedKeys(spec.Environment) {
		fmt.Fprintf(&out, "Environment=%s\n", systemdQuote(key+"="+spec.Environment[key]))
	}
	restart := map[RestartPolicy]string{RestartNever: "no", RestartOnFailure: "on-failure", RestartAlways: "always"}[spec.Restart]
	fmt.Fprintf(&out, "Restart=%s\nRestartSec=%d\n\n[Install]\nWantedBy=default.target\n", restart, spec.RestartSeconds)
	return definition(PlatformLinux, unit, unit, out.String())
}

func renderTaskScheduler(spec Spec) (Definition, error) {
	arguments := make([]string, 0, len(spec.Arguments))
	for _, argument := range spec.Arguments {
		arguments = append(arguments, windowsArgument(argument))
	}
	restart := ""
	if spec.Restart != RestartNever {
		restart = fmt.Sprintf("<RestartOnFailure><Interval>PT%dS</Interval><Count>999</Count></RestartOnFailure>", spec.RestartSeconds)
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<RegistrationInfo><URI>\%s</URI><Description>%s</Description></RegistrationInfo>
<Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
<Principals><Principal id="User"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><ExecutionTimeLimit>PT0S</ExecutionTimeLimit>%s</Settings>
<Actions Context="User"><Exec><Command>%s</Command><Arguments>%s</Arguments>%s</Exec></Actions>
</Task>
`, xmlText(spec.Name), xmlText(spec.Description), xmlText(spec.UserID), xmlText(spec.UserID),
		restart, xmlText(spec.Executable), xmlText(strings.Join(arguments, " ")),
		workingDirectoryElement(spec.WorkingDirectory))
	return definition(PlatformWindows, spec.Name, spec.Name+".xml", content)
}

func workingDirectoryElement(value string) string {
	if value == "" {
		return ""
	}
	return "<WorkingDirectory>" + xmlText(value) + "</WorkingDirectory>"
}

// Publish writes a complete definition into an existing directory without
// replacing anything. A hard link makes publication atomic and fails closed on
// filesystems that cannot link. An identical existing file is reported rather
// than rewritten.
func Publish(def Definition, directory string) (Publication, error) {
	if err := validateDefinition(def); err != nil {
		return Publication{}, err
	}
	root, info, err := openServiceDirectory(directory)
	if err != nil {
		return Publication{}, err
	}
	defer root.Close()
	_ = info
	publication := Publication{Path: filepath.Join(directory, def.FileName)}
	if existing, err := inspectRoot(root, def); err != nil {
		return publication, err
	} else if existing.Installed {
		publication.Existing = true
		if existing.Matches {
			return publication, nil
		}
		return publication, ErrExists
	}
	temporary := ".ext-service-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return publication, fmt.Errorf("%w: cannot stage service definition", ErrUnsafe)
	}
	defer root.Remove(temporary)
	if _, err = io.WriteString(file, def.Content); err != nil {
		file.Close()
		return publication, fmt.Errorf("%w: cannot write service definition", ErrUnsafe)
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return publication, fmt.Errorf("%w: cannot sync service definition", ErrUnsafe)
	}
	if err = file.Close(); err != nil {
		return publication, fmt.Errorf("%w: cannot close service definition", ErrUnsafe)
	}
	if err = root.Link(temporary, def.FileName); err != nil {
		if os.IsExist(err) {
			existing, inspectErr := inspectRoot(root, def)
			if inspectErr != nil {
				return publication, inspectErr
			}
			publication.Existing = true
			if existing.Matches {
				return publication, nil
			}
			return publication, ErrExists
		}
		return publication, fmt.Errorf("%w: cannot publish service definition without replacement", ErrUnsafe)
	}
	publication.Created = true
	def.Path = publication.Path
	return publication, nil
}

// Inspect reports whether the expected definition is installed and unchanged.
func Inspect(def Definition, directory string) (Inspection, error) {
	if err := validateDefinition(def); err != nil {
		return Inspection{}, err
	}
	root, _, err := openServiceDirectory(directory)
	if err != nil {
		return Inspection{}, err
	}
	defer root.Close()
	return inspectRoot(root, def)
}

// Remove deletes only the regular definition file this specification names.
func Remove(def Definition, directory string) error {
	if err := validateDefinition(def); err != nil {
		return err
	}
	root, _, err := openServiceDirectory(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	inspection, err := inspectRoot(root, def)
	if err != nil {
		return err
	}
	if !inspection.Installed {
		return nil
	}
	return root.Remove(def.FileName)
}

func validateDefinition(def Definition) error {
	if def.Platform != PlatformDarwin && def.Platform != PlatformLinux && def.Platform != PlatformWindows {
		return fmt.Errorf("%w: unknown platform", ErrInvalid)
	}
	if def.FileName == "" || def.FileName != path.Base(def.FileName) ||
		strings.ContainsAny(def.FileName, `/\`) || def.FileName == "." || def.FileName == ".." {
		return fmt.Errorf("%w: definition file name is not portable", ErrInvalid)
	}
	if def.Content == "" || len(def.Content) > maxDefinitionBytes {
		return fmt.Errorf("%w: definition content is empty or oversized", ErrInvalid)
	}
	return nil
}

func openServiceDirectory(directory string) (*os.Root, os.FileInfo, error) {
	if directory == "" || !filepath.IsAbs(directory) {
		return nil, nil, fmt.Errorf("%w: service directory must be absolute", ErrInvalid)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%w: service directory must be an existing non-symlink directory", ErrUnsafe)
	}
	if os.PathSeparator == '/' && info.Mode().Perm()&0o022 != 0 {
		return nil, nil, fmt.Errorf("%w: service directory must not be writable by other users", ErrUnsafe)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: cannot open service directory", ErrUnsafe)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, nil, fmt.Errorf("%w: service directory changed during inspection", ErrUnsafe)
	}
	return root, opened, nil
}

func inspectRoot(root *os.Root, def Definition) (Inspection, error) {
	result := Inspection{Path: filepath.Join(root.Name(), def.FileName)}
	info, err := root.Lstat(def.FileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		return result, fmt.Errorf("%w: cannot inspect service definition", ErrUnsafe)
	}
	result.Installed = true
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("%w: service definition is not a regular file", ErrUnsafe)
	}
	if os.PathSeparator == '/' && info.Mode().Perm()&0o022 != 0 {
		return result, fmt.Errorf("%w: service definition is writable by other users", ErrUnsafe)
	}
	file, err := root.Open(def.FileName)
	if err != nil {
		return result, fmt.Errorf("%w: cannot read service definition", ErrUnsafe)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return result, fmt.Errorf("%w: service definition changed during inspection", ErrUnsafe)
	}
	value, err := io.ReadAll(io.LimitReader(file, int64(len(def.Content))+1))
	if err != nil {
		return result, fmt.Errorf("%w: cannot read service definition", ErrUnsafe)
	}
	digest := sha256.Sum256(value)
	result.Digest = hex.EncodeToString(digest[:])
	result.Matches = string(value) == def.Content
	return result, nil
}

// CheckDefinition rejects a status operation when the on-disk definition is
// missing or no longer the one the caller rendered.
func CheckDefinition(def Definition, directory string) (Inspection, error) {
	inspection, err := Inspect(def, directory)
	if err != nil {
		return inspection, err
	}
	if inspection.Installed && !inspection.Matches {
		return inspection, ErrStale
	}
	return inspection, nil
}

func validServiceName(name string, platform Platform) bool {
	if name == "" || len(name) > maxNameLength || !utf8.ValidString(name) {
		return false
	}
	allowed := func(value rune) bool {
		return value == '.' || value == '_' || value == '-' || value == ':' || value == '@' ||
			value == ' ' && platform == PlatformWindows ||
			value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
	}
	for index, value := range name {
		if !allowed(value) {
			return false
		}
		if index == 0 && (value == '.' || value == '-' || value == ':') {
			return false
		}
	}
	return strings.TrimSpace(name) == name
}

func systemdUnitName(name string) string {
	if strings.HasSuffix(name, ".service") {
		return name
	}
	return name + ".service"
}

func systemdQuote(value string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`, "\t", `\t`,
	).Replace(value)
	return `"` + escaped + `"`
}

func systemdValue(value string) string {
	return strings.NewReplacer("%", "%%", "\n", " ").Replace(value)
}

func xmlText(value string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

func windowsArgument(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	slashes := 0
	for _, character := range value {
		if character == '\\' {
			slashes++
			continue
		}
		if character == '"' {
			out.WriteString(strings.Repeat(`\`, slashes*2+1))
		} else {
			out.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		out.WriteRune(character)
	}
	out.WriteString(strings.Repeat(`\`, slashes*2))
	out.WriteByte('"')
	return out.String()
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validText(value string) bool {
	return utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) == -1
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for index, character := range value {
		if index == 0 {
			if character != '_' && !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') {
				return false
			}
			continue
		}
		if character != '_' && !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func absolutePath(platform Platform, value string) bool {
	if value == "" || !validText(value) {
		return false
	}
	if platform == PlatformWindows {
		if len(value) < 3 || value[1] != ':' ||
			!((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) ||
			(value[2] != '\\' && value[2] != '/') ||
			strings.ContainsAny(value[2:], `:*?"<>|%`) {
			return false
		}
		return true
	}
	if !strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) {
		return false
	}
	return path.Clean(value) != ""
}
