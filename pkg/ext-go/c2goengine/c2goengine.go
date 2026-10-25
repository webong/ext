// Package c2goengine is the C engine's portable core, translated to Go by ccgo (modernc.org/ccgo)
// and linked into the program with no C compiler and no cgo. It answers as the engine does: the
// descriptor, request, response, frame, manifest, schema and endpoint rules of lib/plugin, through
// the same service calls pkg/ext-go/cgoengine makes into the C library. The pure-Go engine is
// the oracle it is held against (contract/plugintest/enginetest).
//
// The translation exists for the platforms listed in README.md. On any other, Available is false
// and EngineCall reports plugin.ErrUnsupported.
package c2goengine

import (
	"encoding/json"

	"github.com/webong/ext/pkg/ext-go/c2goengine/internal/ccgo"
)

// Available reports whether this build holds the translation.
const Available = ccgo.Available

// EngineCall runs one of the engine's pure services on bounded JSON, as cgoengine.EngineCall does.
// No plugin is executed and no policy is inferred. See lib/plugin/services.md for the services.
func EngineCall(operation string, input json.RawMessage) (json.RawMessage, error) {
	out, err := ccgo.Call(operation, input)
	if err != nil {
		return nil, err
	}
	return out, nil
}
