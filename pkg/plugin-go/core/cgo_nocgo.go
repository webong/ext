//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package core

import (
	"encoding/json"

	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/guest/instance"
)

// Without the C engine linked, cgoengine is its pure-Go build: the go engine again, and neither
// EngineCall nor Instances exist in it. cgoEngine.Available is false here, so nothing reaches these.

func cgoEngineCall(string, json.RawMessage) (json.RawMessage, error) {
	return nil, plugin.ErrUnsupported
}

func newCGoInstances[T any](e cgoEngine, _ instance.Options[T]) (Instances[T], error) {
	return nil, errUnsupported(e, "sessions")
}
