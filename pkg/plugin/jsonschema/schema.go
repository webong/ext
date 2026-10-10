// Package jsonschema validates JSON values against a bounded, offline subset of
// JSON Schema 2020-12 given as raw JSON. Unsupported assertion keywords fail
// compilation instead of being ignored. References are local JSON pointers
// only; compilation never fetches resources. Evaluation is bounded in depth and
// work, and values and schemas follow the plugin JSON rules (UTF-8, unique
// keys, no unpaired surrogates, 64 levels, at most plugin.MaxFrameBytes).
//
// This package validates arbitrary caller schemas. Package schema is the small
// typed vocabulary for plugin payload declarations in package manifests; the
// two are independent.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/webong/ext/pkg/plugin"
)

// ErrInvalid is wrapped by every validation failure; it also matches
// plugin.ErrInvalid.
var ErrInvalid = fmt.Errorf("%w: JSON Schema validation failed", plugin.ErrInvalid)

type Schema struct {
	root        any
	checkedRefs map[string]bool
}

var errEvaluationLimit = errors.New("schema evaluation limit")

// Compile supports type, properties, required, additionalProperties, items,
// prefixItems, enum, const, numeric/string/array/object bounds, uniqueItems,
// allOf/anyOf/oneOf/not, and local $ref/$defs. format is an annotation.
func Compile(raw json.RawMessage) (*Schema, error) {
	if len(raw) == 0 || len(raw) > maxSchemaBytes {
		return nil, errors.New("schema must be between 1 byte and 1 MiB")
	}
	root, err := decode(raw)
	if err != nil {
		return nil, err
	}
	s := &Schema{root: root, checkedRefs: map[string]bool{}}
	if err := s.check(root, 0); err != nil {
		return nil, err
	}
	s.checkedRefs = nil
	return s, nil
}

func Validate(schema, value json.RawMessage) error {
	s, err := Compile(schema)
	if err != nil {
		return err
	}
	return s.Validate(value)
}

func (s *Schema) Validate(raw json.RawMessage) error {
	if s == nil || len(raw) == 0 || len(raw) > plugin.MaxFrameBytes {
		return fmt.Errorf("%w: invalid value size", ErrInvalid)
	}
	v, err := decode(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid JSON", ErrInvalid)
	}
	budget := 100000
	return s.evaluate(s.root, v, "$", 0, &budget)
}

const maxSchemaBytes = 1 << 20

func decode(raw []byte) (any, error) {
	// Apply the shared JSON rules first: UTF-8, unique keys, surrogates, depth.
	var checked json.RawMessage
	if err := plugin.Decode(raw, &checked); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected one JSON value")
	}
	budget := 100000
	if err := boundedValue(v, 0, &budget); err != nil {
		return nil, err
	}
	return v, nil
}

func boundedValue(v any, depth int, budget *int) error {
	*budget--
	if depth > 64 || *budget < 0 {
		return errors.New("JSON exceeds evaluation bounds")
	}
	switch x := v.(type) {
	case json.Number:
		if _, ok := number(x); !ok {
			return errors.New("JSON number exceeds precision bounds")
		}
	case []any:
		for _, item := range x {
			if err := boundedValue(item, depth+1, budget); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range x {
			if err := boundedValue(item, depth+1, budget); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Schema) check(node any, depth int) error {
	if depth > 64 {
		return errors.New("schema exceeds nesting limit")
	}
	if _, ok := node.(bool); ok {
		return nil
	}
	m, ok := node.(map[string]any)
	if !ok {
		return errors.New("schema must be an object or boolean")
	}
	for key, value := range m {
		switch key {
		case "$schema", "$id", "$comment", "title", "description", "default", "examples", "deprecated", "readOnly", "writeOnly", "format":
			// Annotations do not affect validation or reference resolution.
		case "$ref":
			ref, ok := value.(string)
			if !ok {
				return errors.New("$ref must be a local JSON pointer")
			}
			child, err := s.resolve(ref)
			if err != nil {
				return err
			}
			if !s.checkedRefs[ref] {
				s.checkedRefs[ref] = true
				if err := s.check(child, depth+1); err != nil {
					return err
				}
			}
		case "type":
			list := []any{value}
			if a, ok := value.([]any); ok {
				list = a
			}
			if len(list) == 0 {
				return errors.New("empty schema type")
			}
			for _, t := range list {
				switch t {
				case "object", "array", "string", "number", "integer", "boolean", "null":
				default:
					return errors.New("invalid schema type")
				}
			}
		case "properties", "$defs", "definitions":
			children, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must be an object", key)
			}
			for _, child := range children {
				if err := s.check(child, depth+1); err != nil {
					return err
				}
			}
		case "items", "additionalProperties", "not":
			if err := s.check(value, depth+1); err != nil {
				return err
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			list, ok := value.([]any)
			if !ok || len(list) == 0 {
				return fmt.Errorf("%s must be a nonempty array", key)
			}
			for _, child := range list {
				if err := s.check(child, depth+1); err != nil {
					return err
				}
			}
		case "required":
			list, ok := value.([]any)
			if !ok {
				return errors.New("required must be an array")
			}
			seen := map[string]bool{}
			for _, v := range list {
				name, ok := v.(string)
				if !ok || seen[name] {
					return errors.New("invalid required property")
				}
				seen[name] = true
			}
		case "enum":
			if a, ok := value.([]any); !ok || len(a) == 0 {
				return errors.New("enum must be a nonempty array")
			}
		case "const":
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			n, ok := number(value)
			if !ok || key == "multipleOf" && n.Sign() <= 0 {
				return fmt.Errorf("invalid %s", key)
			}
		case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
			n, ok := number(value)
			if !ok || !n.IsInt() || n.Sign() < 0 {
				return fmt.Errorf("invalid %s", key)
			}
		case "uniqueItems":
			if _, ok := value.(bool); !ok {
				return errors.New("uniqueItems must be boolean")
			}
		case "pattern":
			p, ok := value.(string)
			if !ok {
				return errors.New("pattern must be a string")
			}
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("unsupported pattern: %w", err)
			}
		default:
			return fmt.Errorf("unsupported schema keyword %q", key)
		}
	}
	return nil
}

func (s *Schema) resolve(ref string) (any, error) {
	if ref == "#" {
		return s.root, nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, errors.New("only local JSON pointer references are supported")
	}
	v := s.root
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("unresolved schema reference")
		}
		v, ok = m[part]
		if !ok {
			return nil, errors.New("unresolved schema reference")
		}
	}
	if _, ok := v.(map[string]any); !ok {
		if _, ok = v.(bool); !ok {
			return nil, errors.New("reference does not name a schema")
		}
	}
	return v, nil
}

func (s *Schema) evaluate(node, value any, path string, depth int, budget *int) error {
	*budget--
	if depth > 64 || *budget < 0 {
		return fmt.Errorf("%w: %w", ErrInvalid, errEvaluationLimit)
	}
	fail := func(keyword string) error { return fmt.Errorf("%w: %s violates %s", ErrInvalid, path, keyword) }
	if b, ok := node.(bool); ok {
		if !b {
			return fail("false schema")
		}
		return nil
	}
	m, ok := node.(map[string]any)
	if !ok {
		return fail("invalid schema")
	}
	if ref, ok := m["$ref"].(string); ok {
		child, err := s.resolve(ref)
		if err != nil {
			return err
		}
		if err = s.evaluate(child, value, path, depth+1, budget); err != nil {
			return err
		}
	}
	if t, exists := m["type"]; exists {
		list := []any{t}
		if a, ok := t.([]any); ok {
			list = a
		}
		matched := false
		for _, kind := range list {
			matched = matched || hasType(value, kind.(string))
		}
		if !matched {
			return fail("type")
		}
	}
	if v, exists := m["const"]; exists && !equal(v, value) {
		return fail("const")
	}
	if list, ok := m["enum"].([]any); ok {
		matched := false
		for _, v := range list {
			matched = matched || equal(v, value)
		}
		if !matched {
			return fail("enum")
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if list, ok := m[key].([]any); ok {
			count := 0
			for _, child := range list {
				err := s.evaluate(child, value, path, depth+1, budget)
				if errors.Is(err, errEvaluationLimit) {
					return err
				}
				if err == nil {
					count++
				}
				if *budget < 0 {
					return fail("evaluation limit")
				}
			}
			if key == "allOf" && count != len(list) || key == "anyOf" && count == 0 || key == "oneOf" && count != 1 {
				return fail(key)
			}
		}
	}
	if child, exists := m["not"]; exists {
		err := s.evaluate(child, value, path, depth+1, budget)
		if errors.Is(err, errEvaluationLimit) {
			return err
		}
		if *budget < 0 {
			return fail("evaluation limit")
		}
		if err == nil {
			return fail("not")
		}
	}
	switch v := value.(type) {
	case map[string]any:
		if err := lengthBounds(m, len(v), "Properties", fail); err != nil {
			return err
		}
		if required, ok := m["required"].([]any); ok {
			for _, name := range required {
				if _, exists := v[name.(string)]; !exists {
					return fail("required")
				}
			}
		}
		properties, _ := m["properties"].(map[string]any)
		for name, item := range v {
			child, exists := properties[name]
			if !exists {
				child, exists = m["additionalProperties"]
			}
			if exists {
				if err := s.evaluate(child, item, path+"/"+name, depth+1, budget); err != nil {
					return err
				}
			}
		}
	case []any:
		if err := lengthBounds(m, len(v), "Items", fail); err != nil {
			return err
		}
		prefix, _ := m["prefixItems"].([]any)
		for i, item := range v {
			child, exists := m["items"]
			if i < len(prefix) {
				child, exists = prefix[i], true
			}
			if exists {
				if err := s.evaluate(child, item, fmt.Sprintf("%s/%d", path, i), depth+1, budget); err != nil {
					return err
				}
			}
		}
		if unique, _ := m["uniqueItems"].(bool); unique {
			seen := map[string]bool{}
			for _, item := range v {
				encoded := canonical(item)
				if seen[encoded] {
					return fail("uniqueItems")
				}
				seen[encoded] = true
			}
		}
	case string:
		if err := lengthBounds(m, utf8.RuneCountInString(v), "Length", fail); err != nil {
			return err
		}
		if p, ok := m["pattern"].(string); ok {
			re, err := regexp.Compile(p)
			if err != nil {
				return err
			}
			if !re.MatchString(v) {
				return fail("pattern")
			}
		}
	case json.Number:
		n, _ := number(v)
		for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
			if b, ok := number(m[key]); ok {
				cmp := n.Cmp(b)
				if key == "minimum" && cmp < 0 || key == "maximum" && cmp > 0 || key == "exclusiveMinimum" && cmp <= 0 || key == "exclusiveMaximum" && cmp >= 0 || key == "multipleOf" && !new(big.Rat).Quo(n, b).IsInt() {
					return fail(key)
				}
			}
		}
	}
	return nil
}

func lengthBounds(m map[string]any, length int, suffix string, fail func(string) error) error {
	n := big.NewRat(int64(length), 1)
	for _, prefix := range []string{"min", "max"} {
		key := prefix + suffix
		if b, ok := number(m[key]); ok {
			cmp := n.Cmp(b)
			if prefix == "min" && cmp < 0 || prefix == "max" && cmp > 0 {
				return fail(key)
			}
		}
	}
	return nil
}

func number(v any) (*big.Rat, bool) {
	n, ok := v.(json.Number)
	if !ok || len(n) > 128 {
		return nil, false
	}
	if i := strings.LastIndexAny(string(n), "eE"); i >= 0 {
		exponent, err := strconv.Atoi(string(n)[i+1:])
		if err != nil || exponent < -4096 || exponent > 4096 {
			return nil, false
		}
	}
	r, ok := new(big.Rat).SetString(string(n))
	return r, ok
}
func hasType(v any, kind string) bool {
	switch kind {
	case "null":
		return v == nil
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := number(v)
		return ok
	case "integer":
		n, ok := number(v)
		return ok && n.IsInt()
	}
	return false
}
func equal(a, b any) bool { return canonical(a) == canonical(b) }
func canonical(v any) string {
	// encoding/json sorts object keys. Normalize numbers for JSON numeric equality.
	switch x := v.(type) {
	case json.Number:
		if n, ok := number(x); ok {
			return "n:" + n.RatString()
		}
	case []any:
		parts := make([]string, len(x))
		for i, v := range x {
			parts[i] = canonical(v)
		}
		raw, _ := json.Marshal(parts)
		return "a:" + string(raw)
	case map[string]any:
		m := map[string]string{}
		for k, v := range x {
			m[k] = canonical(v)
		}
		raw, _ := json.Marshal(m)
		return "o:" + string(raw)
	}
	data, _ := json.Marshal(v)
	return fmt.Sprintf("%T:%s", v, data)
}
