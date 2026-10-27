//go:build windows

// Package taskscheduler manages a per-user Windows Task Scheduler task. It owns
// schtasks invocation only; the caller supplies the task name, user identity,
// executable, arguments, working directory, restart policy, and definition
// directory.
package taskscheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/webong/ext/res/system/service"
)

type Config struct {
	// Directory is the caller-selected directory for the task XML definition.
	Directory string
	// Run is optional; the default captures bounded native command output.
	Run service.CommandRunner
}

type Manager struct {
	directory string
	run       service.CommandRunner
}

func New(config Config) (*Manager, error) {
	if !filepath.IsAbs(config.Directory) {
		return nil, fmt.Errorf("%w: task definition directory must be absolute", service.ErrInvalid)
	}
	if config.Run == nil {
		config.Run = service.ExecRunner
	}
	return &Manager{directory: filepath.Clean(config.Directory), run: config.Run}, nil
}

func (m *Manager) Platform() service.Platform { return service.PlatformWindows }
func (m *Manager) Name() string               { return "taskscheduler" }

func (m *Manager) Register(ctx context.Context, spec service.Spec) (service.Registration, error) {
	def, err := service.Render(spec, service.PlatformWindows)
	if err != nil {
		return service.Registration{}, err
	}
	if _, queryErr := m.query(ctx, def.Identity); queryErr == nil {
		return service.Registration{Definition: def}, service.ErrRegistered
	}
	publication, err := service.Publish(def, m.directory)
	registration := service.Registration{Definition: def, Publication: publication}
	if err != nil {
		return registration, err
	}
	registration.Definition.Path = publication.Path
	// /Create without /F refuses an existing task instead of replacing it.
	if _, err = m.run(ctx, "", "schtasks.exe", "/Create", "/TN", def.Identity, "/XML", publication.Path); err != nil {
		return registration, fmt.Errorf("service definition was published but task registration failed: %w", err)
	}
	registration.Registered = true
	return registration, nil
}

func (m *Manager) Unregister(ctx context.Context, spec service.Spec) error {
	def, err := service.Render(spec, service.PlatformWindows)
	if err != nil {
		return err
	}
	if _, err = service.CheckDefinition(def, m.directory); err != nil {
		return err
	}
	if _, deleteErr := m.run(ctx, "", "schtasks.exe", "/Delete", "/TN", def.Identity, "/F"); deleteErr != nil {
		if _, queryErr := m.query(ctx, def.Identity); queryErr == nil {
			return fmt.Errorf("unregister scheduled task: %w", deleteErr)
		}
	}
	return service.Remove(def, m.directory)
}

func (m *Manager) Start(ctx context.Context, spec service.Spec) error {
	def, err := m.requireDefinition(spec)
	if err != nil {
		return err
	}
	if _, err = m.run(ctx, "", "schtasks.exe", "/Run", "/TN", def.Identity); err != nil {
		return fmt.Errorf("start scheduled task: %w", err)
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context, spec service.Spec) error {
	def, err := m.requireDefinition(spec)
	if err != nil {
		return err
	}
	if _, err = m.run(ctx, "", "schtasks.exe", "/End", "/TN", def.Identity); err != nil {
		if _, queryErr := m.query(ctx, def.Identity); queryErr == nil {
			return fmt.Errorf("stop scheduled task: %w", err)
		}
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, spec service.Spec) (service.Status, error) {
	def, err := service.Render(spec, service.PlatformWindows)
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
	output, queryErr := m.query(ctx, def.Identity)
	if queryErr == nil {
		status.Registered = true
		status.Active = taskStatus(output)
		status.Detail = service.OutputText(output)
	} else if status.Installed {
		status.Detail = "service definition is installed but not registered"
	} else {
		status.Detail = "service definition is not installed"
	}
	return status, nil
}

func (m *Manager) requireDefinition(spec service.Spec) (service.Definition, error) {
	def, err := service.Render(spec, service.PlatformWindows)
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

func (m *Manager) query(ctx context.Context, name string) ([]byte, error) {
	return m.run(ctx, "", "schtasks.exe", "/Query", "/TN", name, "/FO", "LIST", "/V")
}

var _ service.Manager = (*Manager)(nil)
