package wasm

import (
	"errors"
	"fmt"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	webwasm "github.com/webong/ext/res/web/wasm"
)

func engineError(err error) error {
	if err == nil {
		return nil
	}
	for _, mapping := range []struct {
		web    error
		engine error
	}{
		{webwasm.ErrInvalid, plugin.ErrInvalid},
		{webwasm.ErrUnsupported, plugin.ErrUnsupported},
		{webwasm.ErrMismatch, plugin.ErrMismatch},
		{webwasm.ErrClosed, plugin.ErrClosed},
	} {
		if errors.Is(err, mapping.web) {
			return fmt.Errorf("%w: %v", mapping.engine, err)
		}
	}
	return err
}
