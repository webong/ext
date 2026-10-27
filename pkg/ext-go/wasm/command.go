package wasm

import (
	"context"

	webwasm "github.com/webong/ext/res/web/wasm"
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
	code, err := webwasm.RunCommand(ctx, module, opts)
	return code, engineError(err)
}

func Inspect(ctx context.Context, module []byte) (Inspection, error) {
	inspection, err := webwasm.Inspect(ctx, module)
	return inspection, engineError(err)
}
