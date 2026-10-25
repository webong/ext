//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"encoding/json"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/guest/author"
	"github.com/webong/ext/pkg/plugin-go/engine/validate/schema"
)

type sharedSchemas struct{}

func (sharedSchemas) ValidateSchema(s schema.Schema) error                 { return ValidateSchema(s) }
func (sharedSchemas) CheckSchema(s schema.Schema, v json.RawMessage) error { return CheckSchema(s, v) }

// NewRegistry uses the shared C schema engine for registration and typed guest
// dispatch. Domain validators and Go decoding remain the application's Go code.
func NewRegistry(identity plugin.Identity) (*author.Registry, error) {
	return author.NewWithSchemaEngine(identity, sharedSchemas{})
}

// These methods make author.Call use the C schema engine for typed host calls.
func (*Host) ValidateSchema(s schema.Schema) error                 { return ValidateSchema(s) }
func (*Host) CheckSchema(s schema.Schema, v json.RawMessage) error { return CheckSchema(s, v) }

var _ author.SchemaEngine = (*Host)(nil)
