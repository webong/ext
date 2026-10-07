// Package schema defines a deliberately bounded schema vocabulary for plugin
// payloads and configuration. It is not a complete JSON Schema implementation.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/webong/ext/pkg/plugin"
)

const Version = "ext.schema/v1"

// Schema rejects undeclared object properties by default. Types are object,
// array, string, number, integer, boolean and null. Constraints inappropriate
// for a type are rejected. Optional fields may be absent; null is a distinct type.
type Schema struct {
	Type                 string            `json:"type"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Required             []string          `json:"required,omitempty"`
	AdditionalProperties bool              `json:"additionalProperties,omitempty"`
	Items                *Schema           `json:"items,omitempty"`
	Enum                 []string          `json:"enum,omitempty"`
	Minimum              *float64          `json:"minimum,omitempty"`
	Maximum              *float64          `json:"maximum,omitempty"`
	MinLength            int               `json:"minLength,omitempty"`
	MaxLength            int               `json:"maxLength,omitempty"`
	MinItems             int               `json:"minItems,omitempty"`
	MaxItems             int               `json:"maxItems,omitempty"`
}

func (s Schema) Validate() error { budget := 1024; return s.validate(0, &budget) }
func (s Schema) validate(depth int, budget *int) error {
	*budget--
	if depth > 16 || *budget < 0 {
		return fmt.Errorf("%w: schema complexity", plugin.ErrInvalid)
	}
	if s.Type != "object" && (len(s.Properties) > 0 || len(s.Required) > 0 || s.AdditionalProperties) {
		return plugin.ErrInvalid
	}
	if s.Type != "array" && (s.Items != nil || s.MinItems != 0 || s.MaxItems != 0) {
		return plugin.ErrInvalid
	}
	if s.Type != "string" && (len(s.Enum) > 0 || s.MinLength != 0 || s.MaxLength != 0) {
		return plugin.ErrInvalid
	}
	if s.Type != "integer" && s.Type != "number" && (s.Minimum != nil || s.Maximum != nil) {
		return plugin.ErrInvalid
	}
	for _, n := range []*float64{s.Minimum, s.Maximum} {
		if n != nil && (math.IsNaN(*n) || math.IsInf(*n, 0)) {
			return plugin.ErrInvalid
		}
	}
	if s.Minimum != nil && s.Maximum != nil && *s.Minimum > *s.Maximum {
		return plugin.ErrInvalid
	}
	if s.MinLength < 0 || s.MaxLength < 0 || (s.MaxLength > 0 && s.MinLength > s.MaxLength) || s.MinItems < 0 || s.MaxItems < 0 || (s.MaxItems > 0 && s.MinItems > s.MaxItems) {
		return plugin.ErrInvalid
	}
	switch s.Type {
	case "object":
		if len(s.Properties) > 256 {
			return plugin.ErrInvalid
		}
		for name, field := range s.Properties {
			if name == "" || len(name) > 256 {
				return plugin.ErrInvalid
			}
			if err := field.validate(depth+1, budget); err != nil {
				return err
			}
		}
		seen := map[string]bool{}
		for _, name := range s.Required {
			if _, ok := s.Properties[name]; !ok || seen[name] {
				return plugin.ErrInvalid
			}
			seen[name] = true
		}
	case "array":
		if s.Items == nil {
			return plugin.ErrInvalid
		}
		return s.Items.validate(depth+1, budget)
	case "string":
		if len(s.Enum) > 256 {
			return plugin.ErrInvalid
		}
		seen := map[string]bool{}
		for _, value := range s.Enum {
			if len(value) > 4096 || seen[value] {
				return plugin.ErrInvalid
			}
			seen[value] = true
		}
	case "number", "integer", "boolean", "null":
	default:
		return fmt.Errorf("%w: unsupported schema type", plugin.ErrInvalid)
	}
	return nil
}

// Check validates JSON without including payload values in errors.
func (s Schema) Check(data json.RawMessage) error {
	if err := s.Validate(); err != nil {
		return err
	}
	var raw json.RawMessage
	if err := plugin.Decode(data, &raw); err != nil {
		return fmt.Errorf("%w: malformed payload", plugin.ErrInvalid)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return plugin.ErrInvalid
	}
	return s.check(value)
}
func (s Schema) check(v any) error {
	invalid := fmt.Errorf("%w: payload does not satisfy schema", plugin.ErrInvalid)
	switch s.Type {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return invalid
		}
		for _, key := range s.Required {
			if _, ok := obj[key]; !ok {
				return invalid
			}
		}
		for key, value := range obj {
			field, ok := s.Properties[key]
			if !ok {
				if !s.AdditionalProperties {
					return invalid
				}
				continue
			}
			if err := field.check(value); err != nil {
				return err
			}
		}
	case "array":
		items, ok := v.([]any)
		if !ok || len(items) < s.MinItems || (s.MaxItems > 0 && len(items) > s.MaxItems) {
			return invalid
		}
		for _, item := range items {
			if err := s.Items.check(item); err != nil {
				return err
			}
		}
	case "string":
		value, ok := v.(string)
		if !ok {
			return invalid
		}
		n := utf8.RuneCountInString(value)
		if n < s.MinLength || (s.MaxLength > 0 && n > s.MaxLength) {
			return invalid
		}
		if len(s.Enum) > 0 {
			found := false
			for _, option := range s.Enum {
				if value == option {
					found = true
				}
			}
			if !found {
				return invalid
			}
		}
	case "number", "integer":
		value, ok := v.(json.Number)
		if !ok {
			return invalid
		}
		n, err := value.Float64()
		if err != nil || math.IsInf(n, 0) || (s.Type == "integer" && math.Trunc(n) != n) || (s.Minimum != nil && n < *s.Minimum) || (s.Maximum != nil && n > *s.Maximum) {
			return invalid
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return invalid
		}
	case "null":
		if v != nil {
			return invalid
		}
	}
	return nil
}

// Clone detaches caller-owned maps and slices after validation.
func (s Schema) Clone() Schema {
	c := s
	c.Required = append([]string(nil), s.Required...)
	c.Enum = append([]string(nil), s.Enum...)
	if s.Properties != nil {
		c.Properties = make(map[string]Schema, len(s.Properties))
		for k, v := range s.Properties {
			c.Properties[k] = v.Clone()
		}
	}
	if s.Items != nil {
		v := s.Items.Clone()
		c.Items = &v
	}
	if s.Minimum != nil {
		v := *s.Minimum
		c.Minimum = &v
	}
	if s.Maximum != nil {
		v := *s.Maximum
		c.Maximum = &v
	}
	return c
}
