//go:build darwin

// Package launchd manages a per-user macOS launch agent. It owns launchctl
// invocation only; the caller supplies the label, executable, arguments,
// environment, working directory, restart policy, definition directory, and any
// readiness probe.
package launchd

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/webong/ext/res/system/service"
)

type Config struct {
	// Directory is the caller-selected LaunchAgents directory.
	Directory string
	// UID is the non-root user whose GUI domain owns the agent.
	UID int
	// Run is optional; the default captures bounded native command output.
	Run service.CommandRunner
}

type Manager struct {
	directory string
	uid       int
	run       service.CommandRunner
}

func New(config Config) (*Manager, error) {
	if !filepath.IsAbs(config.Directory) {
		return nil, fmt.Errorf("%w: launch agent directory must be absolute", service.ErrInvalid)
	}
	if config.UID <= 0 {
		return nil, fmt.Errorf("%w: launch agents require a non-root user", service.ErrInvalid)
	}
	if config.Run == nil {
		config.Run = service.ExecRunner
	}
	return &Manager{directory: filepath.Clean(config.Directory), uid: config.UID, run: config.Run}, nil
}

func (m *Manager) Platform() service.Platform { return service.PlatformDarwin }
func (m *Manager) Name() string               { return "launchd" }

func (m *Manager) Register(ctx context.Context, spec service.Spec) (service.Registration, error) {
	def, err := service.Render(spec, service.PlatformDarwin)
	if err != nil {
		return service.Registration{}, err
	}
	if _, printErr := m.print(ctx, def.Identity); printErr == nil {
		return service.Registration{Definition: def}, service.ErrRegistered
	}
	publication, err := service.Publish(def, m.directory)
	registration := service.Registration{Definition: def, Publication: publication}
	if err != nil {
		return registration, err
	}
	registration.Definition.Path = publication.Path
	if _, err = m.run(ctx, "", "/bin/launchctl", "bootstrap", m.domain(), publication.Path); err != nil {
		return registration, fmt.Errorf("service definition was published but registration failed: %w", err)
	}
	registration.Registered = true
	return registration, nil
}

func (m *Manager) Unregister(ctx context.Context, spec service.Spec) error {
	def, err := service.Render(spec, service.PlatformDarwin)
	if err != nil {
		return err
	}
	if _, err = service.CheckDefinition(def, m.directory); err != nil {
		return err
	}
	if _, bootErr := m.run(ctx, "", "/bin/launchctl", "bootout", m.domain()+"/"+def.Identity); bootErr != nil {
		if _, printErr := m.print(ctx, def.Identity); printErr == nil {
			return fmt.Errorf("unregister launch agent: %w", bootErr)
		}
	}
	return service.Remove(def, m.directory)
}

func (m *Manager) Start(ctx context.Context, spec service.Spec) error {
	def, err := m.requireDefinition(spec)
	if err != nil {
		return err
	}
	if _, err = m.run(ctx, "", "/bin/launchctl", "kickstart", "-k", m.domain()+"/"+def.Identity); err != nil {
		return fmt.Errorf("start launch agent: %w", err)
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context, spec service.Spec) error {
	def, err := m.requireDefinition(spec)
	if err != nil {
		return err
	}
	domain := m.domain() + "/" + def.Identity
	if _, err = m.run(ctx, "", "/bin/launchctl", "kill", "SIGTERM", domain); err != nil {
		if _, printErr := m.print(ctx, def.Identity); printErr == nil {
			return fmt.Errorf("stop launch agent: %w", err)
		}
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, spec service.Spec) (service.Status, error) {
	def, err := service.Render(spec, service.PlatformDarwin)
	if err != nil {
		return service.Status{}, err
	}
	inspection, err := service.Inspect(def, m.directory)
	if err != nil {
		return service.Status{}, err
	}
	status := service.Status{
		Name: spec.Name, Manager: m.Name(), CheckedAt: time.Now().UTC(),
		DefinitionPath: inspection.Path, DefinitionDigest: inspection.Digest,
		Installed: inspection.Installed, Stale: inspection.Installed && !inspection.Matches,
	}
	output, printErr := m.print(ctx, def.Identity)
	if printErr == nil {
		status.Registered = true
		status.Active = strings.Contains(strings.ToLower(string(output)), "state = running")
		status.Detail = service.OutputText(output)
	} else if status.Installed {
		status.Detail = "service definition is installed but not registered"
	} else {
		status.Detail = "service definition is not installed"
	}
	return status, nil
}

func (m *Manager) requireDefinition(spec service.Spec) (service.Definition, error) {
	def, err := service.Render(spec, service.PlatformDarwin)
	if err != nil {
		return def, err
	}
	inspection, err := service.CheckDefinition(def, m.directory)
	if err != nil {
		return def, err
	}
	if !inspection.Installed {
		return def, service.ErrNotInstalled
	}
	return def, nil
}

func (m *Manager) print(ctx context.Context, label string) ([]byte, error) {
	return m.run(ctx, "", "/bin/launchctl", "print", m.domain()+"/"+label)
}

func (m *Manager) domain() string { return "gui/" + strconv.Itoa(m.uid) }

var _ service.Manager = (*Manager)(nil)
