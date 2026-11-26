package wasm

import (
	"context"

	"github.com/webong/ext/pkg/ext-go/wasm/runtime"
	webwasm "github.com/webong/ext/res/web/host/engine/wasm"
)

type CommandOptions = webwasm.CommandOptions
type Inspection = webwasm.Inspection
type Target = webwasm.Target

const (
	TargetNone      = webwasm.TargetNone
	TargetWASI      = webwasm.TargetWASI
	TargetHost      = webwasm.TargetHost
	TargetComponent = webwasm.TargetComponent
)

func RunCommand(ctx context.Context, module []byte, opts CommandOptions) (int, error) {
	code, err := runtime.RunCommand(ctx, module, opts)
	return code, engineError(err)
}

func Inspect(ctx context.Context, module []byte) (Inspection, error) {
	inspection, err := runtime.Inspect(ctx, module)
	return inspection, engineError(err)
}
