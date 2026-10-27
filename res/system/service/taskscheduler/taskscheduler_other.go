//go:build !windows

package taskscheduler

import (
	"context"

	"github.com/webong/ext/res/system/service"
)

type Config struct {
	Directory string
	Run       service.CommandRunner
}

// Manager is a placeholder on hosts without Task Scheduler so importing the
// package stays portable and every lifecycle call fails explicitly.
type Manager struct{}

func New(Config) (*Manager, error) { return nil, ErrUnsupported }

func (*Manager) Platform() service.Platform { return service.PlatformWindows }
func (*Manager) Name() string               { return "taskscheduler" }

func (*Manager) Register(context.Context, service.Spec) (service.Registration, error) {
	return service.Registration{}, ErrUnsupported
}
func (*Manager) Unregister(context.Context, service.Spec) error { return ErrUnsupported }
func (*Manager) Start(context.Context, service.Spec) error      { return ErrUnsupported }
func (*Manager) Stop(context.Context, service.Spec) error       { return ErrUnsupported }
func (*Manager) Status(context.Context, service.Spec) (service.Status, error) {
	return service.Status{}, ErrUnsupported
}

var _ service.Manager = (*Manager)(nil)
