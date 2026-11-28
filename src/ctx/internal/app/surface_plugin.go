package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/home"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
	"github.com/webong/ext/src/ctx/system/capture/surface"
)

const surfaceGuestProtocol = surface.ControlProtocol

var surfaceGuestContract = plugin.ContractRef{Name: "ext.system.surface", Version: "v1"}

type surfaceGuestBackend struct{ session *plugin.Session }

func openSurfaceBackend(ctx context.Context) (surfaceGuestBackend, func(), error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return surfaceGuestBackend{}, nil, fmt.Errorf("%w: no surface resource guest is available for %s", plugin.ErrUnsupported, runtime.GOOS)
	}
	s, err := store.NewWithLayout(home.Packages(), store.Limits{}, storelayout.Ext())
	if err != nil {
		return surfaceGuestBackend{}, nil, err
	}
	selected, err := trustedSurfaceEntrypoint(s)
	if err != nil {
		return surfaceGuestBackend{}, nil, err
	}
	descriptor := selected.Package.Manifest.Descriptor
	session, err := plugin.Open(ctx, descriptor, plugin.Options{
		Verify: func(_ context.Context, current plugin.Descriptor) error {
			fresh, err := store.NewWithLayout(home.Packages(), store.Limits{}, storelayout.Ext())
			if err != nil {
				return err
			}
			again, err := trustedSurfaceEntrypoint(fresh)
			if err != nil || again.Digest != selected.Digest ||
				again.Artifact != selected.Artifact ||
				filepath.Clean(again.Path) != filepath.Clean(selected.Path) ||
				again.Package.Manifest.Descriptor.Identity != current.Identity {
				return plugin.ErrDenied
			}
			return nil
		},
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			return process.Start(ctx, process.Command{Path: selected.Path, Args: []string{"run"}, Env: os.Environ()})
		},
		Authorize: func(ctx context.Context, _ plugin.Request) error { return ctx.Err() },
		Timeout:   time.Hour,
	})
	if err != nil {
		return surfaceGuestBackend{}, nil, err
	}
	return surfaceGuestBackend{session: session}, func() { _ = session.Abort() }, nil
}

func trustedSurfaceEntrypoint(s *store.Store) (store.Entrypoint, error) {
	id := "system.surface." + runtime.GOOS
	installed, err := s.Load(id)
	if err != nil {
		return store.Entrypoint{}, fmt.Errorf("surface resource guest is not installed: %w", err)
	}
	digest, err := installed.Digest()
	if err != nil {
		return store.Entrypoint{}, err
	}
	file, err := trust.New(filepath.Join(home.Packages(), ".package-trust.json"))
	if err != nil {
		return store.Entrypoint{}, err
	}
	state, err := file.StateOf("ext.command/v1", id, digest)
	if err != nil {
		return store.Entrypoint{}, err
	}
	if state != trust.Trusted {
		return store.Entrypoint{}, fmt.Errorf("%w: surface resource guest requires ext package trust %s", plugin.ErrDenied, id)
	}
	return s.ResolveEntrypoint(id, surfaceGuestContract.Name, surfaceGuestContract.Version, surfaceGuestProtocol, digest)
}

func (b surfaceGuestBackend) call(ctx context.Context, operation string, request any, response any, timeout time.Duration) error {
	input, err := json.Marshal(request)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	output, err := b.session.Call(ctx, surfaceGuestContract, operation, input)
	if err != nil {
		return err
	}
	return json.Unmarshal(output, response)
}

func (b surfaceGuestBackend) Capture(ctx context.Context, request surface.CaptureRequest) (surface.SurfaceCapture, error) {
	var result surface.SurfaceCapture
	if err := request.Validate(); err != nil {
		return result, err
	}
	if err := b.call(ctx, "capture", request, &result, time.Duration(request.TimeoutMS)*time.Millisecond); err != nil {
		return result, err
	}
	return result, request.ValidateCapture(result)
}

func (b surfaceGuestBackend) Input(ctx context.Context, request surface.InputRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	var result struct{}
	return b.call(ctx, "input", request, &result, time.Duration(request.TimeoutMS)*time.Millisecond)
}
