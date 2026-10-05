//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"encoding/json"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/author"
	"github.com/webong/ctx/pkg/plugin/schema"
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
