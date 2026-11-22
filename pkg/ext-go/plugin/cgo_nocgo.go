//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package plugin

import (
	"encoding/json"

	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/guest/instance"
)

// Without the C engine linked, cgoengine is its pure-Go build: the go engine again, and neither
// EngineCall nor Instances exist in it. cgoEngine.Available is false here, so nothing reaches these.

func cgoEngineCall(string, json.RawMessage) (json.RawMessage, error) {
	return nil, engine.ErrUnsupported
}

func newCGoInstances[T any](e cgoEngine, _ instance.Options[T]) (Instances[T], error) {
	return nil, errUnsupported(e, "sessions")
}
