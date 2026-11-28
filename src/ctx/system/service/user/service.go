// Package user defines the Go contract for per-user service managers. The C
// ledger renders and safely publishes definitions through an injected
// Definitions implementation. A selected manager handles registration and
// lifecycle commands.
//
// The package owns no product name, installation layout, data directory, port,
// key provider, endpoint, or readiness protocol. The consumer supplies every
// value that identifies its service.
package user

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	// The selected ledger-backed manager validates the specification when it is
	// rendered for a concrete platform operation.
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

// Definitions is supplied by the C ledger binding. Native managers use it for
// portable rendering and safe definition files without implementing those rules.
type Definitions interface {
	Render(context.Context, Spec, Platform) (Definition, error)
	Publish(context.Context, Definition, string) (Publication, error)
	Inspect(context.Context, Definition, string) (Inspection, error)
	Remove(context.Context, Definition, string) error
}

// CheckDefinition rejects lifecycle work on a changed on-disk definition.
func CheckDefinition(ctx context.Context, definitions Definitions, def Definition, directory string) (Inspection, error) {
	inspection, err := definitions.Inspect(ctx, def, directory)
	if err != nil {
		return inspection, err
	}
	if inspection.Installed && !inspection.Matches {
		return inspection, ErrStale
	}
	return inspection, nil
}
