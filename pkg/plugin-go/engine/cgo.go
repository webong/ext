package engine

import (
	"context"
	"encoding/json"

	"github.com/webong/ext/pkg/plugin-go/cgoengine"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// cgoEngine is the C engine through cgo. cgoengine serves the same API in pure Go when it is built
// without cgo, the ext_cengine tag or the C library; that is the go engine again, so here the engine
// is available only where cgoengine.CEngine says the C engine is linked.
type cgoEngine struct{}

func (cgoEngine) sealed()         {}
func (cgoEngine) Name() Name      { return CGo }
func (cgoEngine) Available() bool { return cgoengine.CEngine }
func (e cgoEngine) Features() Feature {
	if !e.Available() {
		return 0
	}
	return Services | Sessions | Progress | Reverse
}

func (e cgoEngine) Call(operation string, input json.RawMessage) (json.RawMessage, error) {
	if !e.Available() {
		return nil, errUnsupported(e, "services")
	}
	return cgoEngineCall(operation, input)
}

func (e cgoEngine) Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (Session, error) {
	if !e.Available() {
		return nil, errUnsupported(e, "sessions")
	}
	s, err := cgoengine.Open(ctx, d, options)
	if err != nil {
		return nil, err
	}
	return session{CGo, s}, nil
}

func (e cgoEngine) OpenProcess(ctx context.Context, d plugin.Descriptor, launch Launch, options plugin.Options) (Session, error) {
	if !e.Available() {
		return nil, errUnsupported(e, "sessions")
	}
	s, err := cgoengine.OpenProcess(ctx, d, cgoengine.LaunchOptions{
		ProcessOptions: cgoengine.ProcessOptions{Executable: launch.Executable, Arguments: launch.Arguments, Environment: launch.Environment},
		Directory:      launch.Directory,
		StderrBytes:    launch.StderrBytes,
	}, options)
	if err != nil {
		return nil, err
	}
	return session{CGo, s}, nil
}
