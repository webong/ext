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

	"github.com/webong/ext/res/system/service/user"
)

type Config struct {
	// Directory is the caller-selected directory for the task XML definition.
	Directory string
	// Run is optional; the default captures bounded native command output.
	Run user.CommandRunner
}

type Manager struct {
	directory string
	run       user.CommandRunner
}

func New(config Config) (*Manager, error) {
	if !filepath.IsAbs(config.Directory) {
		return nil, fmt.Errorf("%w: task definition directory must be absolute", user.ErrInvalid)
	}
	if config.Run == nil {
		config.Run = user.ExecRunner
	}
	return &Manager{directory: filepath.Clean(config.Directory), run: config.Run}, nil
}

func (m *Manager) Platform() user.Platform { return user.PlatformWindows }
func (m *Manager) Name() string            { return "taskscheduler" }

func (m *Manager) Register(ctx context.Context, spec user.Spec) (user.Registration, error) {
	def, err := user.Render(spec, user.PlatformWindows)
	if err != nil {
		return user.Registration{}, err
	}
	if _, queryErr := m.query(ctx, def.Identity); queryErr == nil {
		return user.Registration{Definition: def}, user.ErrRegistered
	}
	publication, err := user.Publish(def, m.directory)
	registration := user.Registration{Definition: def, Publication: publication}
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

func (m *Manager) Unregister(ctx context.Context, spec user.Spec) error {
	def, err := user.Render(spec, user.PlatformWindows)
	if err != nil {
		return err
	}
	if _, err = user.CheckDefinition(def, m.directory); err != nil {
		return err
	}
	if _, deleteErr := m.run(ctx, "", "schtasks.exe", "/Delete", "/TN", def.Identity, "/F"); deleteErr != nil {
		if _, queryErr := m.query(ctx, def.Identity); queryErr == nil {
			return fmt.Errorf("unregister scheduled task: %w", deleteErr)
		}
	}
	return user.Remove(def, m.directory)
}

func (m *Manager) Start(ctx context.Context, spec user.Spec) error {
	def, err := m.requireDefinition(spec)
	if err != nil {
		return err
	}
	if _, err = m.run(ctx, "", "schtasks.exe", "/Run", "/TN", def.Identity); err != nil {
		return fmt.Errorf("start scheduled task: %w", err)
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context, spec user.Spec) error {
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

func (m *Manager) Status(ctx context.Context, spec user.Spec) (user.Status, error) {
	def, err := user.Render(spec, user.PlatformWindows)
	if err != nil {
		return user.Status{}, err
	}
	inspection, err := user.Inspect(def, m.directory)
	if err != nil {
		return user.Status{}, err
	}
	status := user.Status{
		Name: spec.Name, Manager: m.Name(), CheckedAt: time.Now().UTC(),
		DefinitionPath: inspection.Path, DefinitionDigest: inspection.Digest,
		Installed: inspection.Installed, Stale: inspection.Installed && !inspection.Matches,
	}
	output, queryErr := m.query(ctx, def.Identity)
	if queryErr == nil {
		status.Registered = true
		status.Active = taskStatus(output)
		status.Detail = user.OutputText(output)
	} else if status.Installed {
		status.Detail = "service definition is installed but not registered"
	} else {
		status.Detail = "service definition is not installed"
	}
	return status, nil
}

func (m *Manager) requireDefinition(spec user.Spec) (user.Definition, error) {
	def, err := user.Render(spec, user.PlatformWindows)
	if err != nil {
		return def, err
	}
	inspection, err := user.CheckDefinition(def, m.directory)
	if err != nil {
		return def, err
	}
	if !inspection.Installed {
		return def, user.ErrNotInstalled
	}
	return def, nil
}

func (m *Manager) query(ctx context.Context, name string) ([]byte, error) {
	return m.run(ctx, "", "schtasks.exe", "/Query", "/TN", name, "/FO", "LIST", "/V")
}

var _ user.Manager = (*Manager)(nil)
