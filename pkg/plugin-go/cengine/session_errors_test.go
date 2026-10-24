//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest/enginetest"
)

// session is the part of a session both implementations share.
type session interface {
	Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)
	State() plugin.State
	Close(context.Context) error
	Abort() error
}

// causes lists the classes an error can belong to; the harness holds the one definition.
func causes(err error) string { return enginetest.Causes(err) }
