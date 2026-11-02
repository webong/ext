// Package systemd owns the native `systemctl --user` behavior behind the
// portable res/system/service/user contract. It is not a res/system platform
// implementation: Linux hosts may run a different service manager, so a
// consumer selects this backend explicitly.
package systemd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/webong/ext/res/system/service/user"
)

// Config names the caller's definition directory and, optionally, the systemctl
// executable. No product identity, unit name, or path is chosen here.
type Config struct {
	Directory string
	// Executable defaults to "systemctl" on PATH.
	Executable string
	// Run is optional; the default captures bounded native command output.
	Run user.CommandRunner
}

type Manager struct {
	directory  string
	executable string
	run        user.CommandRunner
}

func New(config Config) (*Manager, error) {
	if !filepath.IsAbs(config.Directory) {
		return nil, fmt.Errorf("%w: systemd unit directory must be absolute", user.ErrInvalid)
	}
	if config.Executable == "" {
		config.Executable = "systemctl"
	}
	if config.Run == nil {
		config.Run = user.ExecRunner
	}
	return &Manager{directory: filepath.Clean(config.Directory), executable: config.Executable, run: config.Run}, nil
}

func (m *Manager) Platform() user.Platform { return user.PlatformLinux }
func (m *Manager) Name() string               { return "systemd" }

func (m *Manager) Register(ctx context.Context, spec user.Spec) (user.Registration, error) {
	def, err := user.Render(spec, user.PlatformLinux)
	if err != nil {
		return user.Registration{}, err
	}
	if registered, _ := m.registered(ctx, def.Identity); registered {
		return user.Registration{Definition: def}, user.ErrRegistered
	}
	publication, err := user.Publish(def, m.directory)
	registration := user.Registration{Definition: def, Publication: publication}
	if err != nil {
		return registration, err
	}
	registration.Definition.Path = publication.Path
	if _, err = m.run(ctx, "", m.executable, "--user", "daemon-reload"); err != nil {
		return registration, fmt.Errorf("service definition was published but the unit manager did not reload: %w", err)
	}
	if _, err = m.run(ctx, "", m.executable, "--user", "enable", "--now", def.Identity); err != nil {
		return registration, fmt.Errorf("service definition was published but registration failed: %w", err)
	}
	registration.Registered = true
	return registration, nil
}

func (m *Manager) Unregister(ctx context.Context, spec user.Spec) error {
	def, err := user.Render(spec, user.PlatformLinux)
	if err != nil {
		return err
	}
	if _, err = user.CheckDefinition(def, m.directory); err != nil {
		return err
	}
	registered, stateErr := m.registered(ctx, def.Identity)
	if stateErr != nil {
		return stateErr
	}
	if registered {
		if _, err = m.run(ctx, "", m.executable, "--user", "disable", "--now", def.Identity); err != nil {
			return fmt.Errorf("unregister user unit: %w", err)
		}
	}
	if err = user.Remove(def, m.directory); err != nil {
		return err
	}
	_, reloadErr := m.run(ctx, "", m.executable, "--user", "daemon-reload")
	return reloadErr
}

func (m *Manager) Start(ctx context.Context, spec user.Spec) error {
	def, err := m.requireDefinition(spec)
	if err != nil {
		return err
	}
	if _, err = m.run(ctx, "", m.executable, "--user", "start", def.Identity); err != nil {
		return fmt.Errorf("start user unit: %w", err)
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context, spec user.Spec) error {
	def, err := m.requireDefinition(spec)
	if err != nil {
		return err
	}
	if _, err = m.run(ctx, "", m.executable, "--user", "stop", def.Identity); err != nil {
		return fmt.Errorf("stop user unit: %w", err)
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, spec user.Spec) (user.Status, error) {
	def, err := user.Render(spec, user.PlatformLinux)
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
	output, callErr := m.run(ctx, "", m.executable, "--user", "show", def.Identity,
		"--property=LoadState", "--property=ActiveState", "--property=FragmentPath")
	properties := parseProperties(output)
	if callErr == nil && properties["LoadState"] != "" {
		status.Registered = properties["LoadState"] != "not-found"
		status.Active = properties["ActiveState"] == "active"
		status.Detail = fmt.Sprintf("load=%s active=%s", properties["LoadState"], properties["ActiveState"])
	} else if status.Installed {
		status.Detail = "service definition is installed but not registered"
	} else {
		status.Detail = "service definition is not installed"
	}
	return status, nil
}

func (m *Manager) registered(ctx context.Context, unit string) (bool, error) {
	output, err := m.run(ctx, "", m.executable, "--user", "show", unit,
		"--property=LoadState", "--property=FragmentPath")
	properties := parseProperties(output)
	if err != nil && len(properties) == 0 {
		return false, nil
	}
	if properties["LoadState"] == "" {
		return false, nil
	}
	return properties["LoadState"] != "not-found", nil
}

func (m *Manager) requireDefinition(spec user.Spec) (user.Definition, error) {
	def, err := user.Render(spec, user.PlatformLinux)
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

func parseProperties(output []byte) map[string]string {
	properties := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && key != "" {
			properties[key] = value
		}
	}
	return properties
}

var _ user.Manager = (*Manager)(nil)

// ErrUnsupported reports a host without a systemd user manager.
var ErrUnsupported = errors.New("systemd user services are unavailable")
