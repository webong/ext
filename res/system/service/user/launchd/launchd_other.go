//go:build !darwin

package launchd

import (
	"context"

	"github.com/webong/ext/res/system/service/user"
)

type Config struct {
	Directory string
	UID       int
	Run       user.CommandRunner
}

// Manager is a placeholder on hosts without launchd so importing the package
// stays portable and every lifecycle call fails explicitly.
type Manager struct{}

func New(Config) (*Manager, error) { return nil, ErrUnsupported }

func (*Manager) Platform() user.Platform { return user.PlatformDarwin }
func (*Manager) Name() string               { return "launchd" }

func (*Manager) Register(context.Context, user.Spec) (user.Registration, error) {
	return user.Registration{}, ErrUnsupported
}
func (*Manager) Unregister(context.Context, user.Spec) error { return ErrUnsupported }
func (*Manager) Start(context.Context, user.Spec) error      { return ErrUnsupported }
func (*Manager) Stop(context.Context, user.Spec) error       { return ErrUnsupported }
func (*Manager) Status(context.Context, user.Spec) (user.Status, error) {
	return user.Status{}, ErrUnsupported
}

var _ user.Manager = (*Manager)(nil)
