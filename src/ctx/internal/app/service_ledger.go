package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/res/system/service/user"
	"github.com/webong/ext/src/ctx/internal/graphapi"
)

type serviceLedger struct{ library *ledger.Library }

func newServiceLedger() (user.Definitions, error) {
	library, err := graphapi.Library()
	if err != nil {
		return nil, err
	}
	return serviceLedger{library: library}, nil
}

func serviceError(err error) error {
	if err == nil {
		return nil
	}
	switch ledger.Code(err) {
	case "invalid", "too_large":
		return fmt.Errorf("%w: %v", user.ErrInvalid, err)
	case "exists":
		return fmt.Errorf("%w: %v", user.ErrExists, err)
	case "changed":
		return fmt.Errorf("%w: %v", user.ErrStale, err)
	case "denied":
		return fmt.Errorf("%w: %v", user.ErrUnsafe, err)
	case "unsupported":
		return fmt.Errorf("%w: %v", user.ErrUnsupported, err)
	default:
		return err
	}
}

func (s serviceLedger) Render(ctx context.Context, spec user.Spec, platform user.Platform) (user.Definition, error) {
	definition, err := s.library.RenderService(ctx, string(platform), ledger.ServiceSpec{
		Name: spec.Name, Description: spec.Description, Executable: spec.Executable,
		Arguments: spec.Arguments, Environment: spec.Environment,
		WorkingDirectory: spec.WorkingDirectory, Restart: string(spec.Restart),
		RestartSeconds: spec.RestartSeconds, UserID: spec.UserID,
	})
	if err != nil {
		return user.Definition{}, serviceError(err)
	}
	return user.Definition{Platform: platform, Identity: definition.Identity,
		FileName: definition.FileName, Content: definition.Content, Digest: definition.Digest}, nil
}

func nativeDefinition(def user.Definition) ledger.ServiceDefinition {
	return ledger.ServiceDefinition{Platform: string(def.Platform), Identity: def.Identity,
		FileName: def.FileName, Content: def.Content, Digest: def.Digest}
}

func (s serviceLedger) Publish(ctx context.Context, def user.Definition, directory string) (user.Publication, error) {
	state, err := s.library.PublishService(ctx, nativeDefinition(def), directory)
	publication := user.Publication{Path: filepath.Join(directory, def.FileName), Created: state.Created, Existing: state.Existing}
	return publication, serviceError(err)
}

func (s serviceLedger) Inspect(ctx context.Context, def user.Definition, directory string) (user.Inspection, error) {
	state, err := s.library.InspectService(ctx, nativeDefinition(def), directory)
	return user.Inspection{Path: state.Path, Installed: state.Installed,
		Matches: state.Matches, Digest: state.Digest}, serviceError(err)
}

func (s serviceLedger) Remove(ctx context.Context, def user.Definition, directory string) error {
	_, err := s.library.RemoveService(ctx, nativeDefinition(def), directory)
	return serviceError(err)
}

var _ user.Definitions = serviceLedger{}
