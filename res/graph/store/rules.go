package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,254}$`)
var secretKeyPattern = regexp.MustCompile(`(?i)(secret|password|passwd|token|credential|private.?key|api.?key)`)

func validateName(value string) error {
	if !namePattern.MatchString(value) || strings.Contains(value, "//") {
		return fmt.Errorf("%w: %q", ErrInvalidName, value)
	}
	return nil
}

// qualified reports whether name is a valid name under namespace: the form
// "<namespace>/<name>".
func qualified(namespace, name string) bool {
	return validateName(name) == nil && strings.HasPrefix(name, namespace+"/") && len(name) > len(namespace)+1
}

func validateAttributes(attrs map[string]any) error {
	for key, value := range attrs {
		if secretKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: attribute %q", ErrSecretValue, key)
		}
		if err := inspectValue(value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAttributes checks ordinary graph metadata before projection. It
// applies the same secret-marker and size rules as Store.Apply.
func ValidateAttributes(attrs map[string]any) error { return validateAttributes(attrs) }

func validateLabels(labels map[string]string) error {
	for key, value := range labels {
		if secretKeyPattern.MatchString(key) || isSecretMarker(value) {
			return fmt.Errorf("%w: label %q", ErrSecretValue, key)
		}
		if len(value) > MaxStringBytes {
			return fmt.Errorf("%w: label %q exceeds %d bytes", ErrTooLarge, key, MaxStringBytes)
		}
	}
	return nil
}

func validateProvenance(p Provenance) error {
	for _, value := range []string{p.Source, p.Operation} {
		if isSecretMarker(value) {
			return ErrSecretValue
		}
		if len(value) > MaxStringBytes {
			return fmt.Errorf("%w: provenance exceeds %d bytes", ErrTooLarge, MaxStringBytes)
		}
	}
	return nil
}

func isSecretMarker(value string) bool {
	value = strings.ToLower(value)
	return strings.HasPrefix(value, "secret-value:") || strings.HasPrefix(value, "private-key:")
}

func inspectValue(value any) error {
	switch v := value.(type) {
	case nil, bool, float64, json.Number:
		return nil
	case map[string]any:
		return validateAttributes(v)
	case []any:
		for _, item := range v {
			if err := inspectValue(item); err != nil {
				return err
			}
		}
		return nil
	case string:
		// Secret bytes cannot be recognized reliably. The public record model
		// rejects common credential forms; consumers should store only scoped,
		// opaque references (for example "secret-ref:..."), never secret bytes.
		if isSecretMarker(v) {
			return ErrSecretValue
		}
		if len(v) > MaxStringBytes {
			return fmt.Errorf("%w: string attribute exceeds %d bytes", ErrTooLarge, MaxStringBytes)
		}
		return nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("%w: graph attribute is not JSON serializable", ErrInvalid)
		}
		var decoded any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return fmt.Errorf("%w: graph attribute is not JSON serializable", ErrInvalid)
		}
		return inspectValue(decoded)
	}
}

// ValidateSchema checks a schema's names: the namespace, and every kind and
// relationship type and endpoint kind under it.
func ValidateSchema(schema Schema) error {
	if err := validateName(schema.Namespace); err != nil {
		return err
	}
	if schema.Version == "" || len(schema.Version) > 255 {
		return fmt.Errorf("%w: schema version is required", ErrInvalid)
	}
	for _, kind := range schema.Kinds {
		if !qualified(schema.Namespace, kind) {
			return fmt.Errorf("%w: kind %q is not under %s", ErrInvalidName, kind, schema.Namespace)
		}
	}
	for _, rel := range schema.Relationships {
		if !qualified(schema.Namespace, rel.Type) {
			return fmt.Errorf("%w: relationship %q is not under %s", ErrInvalidName, rel.Type, schema.Namespace)
		}
		for _, end := range []string{rel.From, rel.To} {
			if end != "" && !qualified(schema.Namespace, end) {
				return fmt.Errorf("%w: endpoint kind %q is not under %s", ErrInvalidName, end, schema.Namespace)
			}
		}
	}
	return nil
}

// NormalizeSchema sorts and deduplicates a schema's rules, so two schemas with
// the same rules have the same canonical form.
func NormalizeSchema(schema Schema) Schema {
	out := Schema{Namespace: schema.Namespace, Version: schema.Version}
	seen := map[string]bool{}
	for _, kind := range schema.Kinds {
		if !seen[kind] {
			seen[kind] = true
			out.Kinds = append(out.Kinds, kind)
		}
	}
	sort.Strings(out.Kinds)
	rels := map[Relationship]bool{}
	for _, rel := range schema.Relationships {
		if !rels[rel] {
			rels[rel] = true
			out.Relationships = append(out.Relationships, rel)
		}
	}
	sort.Slice(out.Relationships, func(i, j int) bool {
		a, b := out.Relationships[i], out.Relationships[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	return out
}

// SameSchema reports whether a and b declare the same rules at the same version.
func SameSchema(a, b Schema) bool {
	x, _ := Canonical(NormalizeSchema(a))
	y, _ := Canonical(NormalizeSchema(b))
	return bytes.Equal(x, y)
}

// Canonical encodes v as canonical JSON: the value's JSON form with object keys
// sorted by UTF-16 code units, no insignificant whitespace, numbers in the
// shortest form that round-trips (as ECMAScript prints them) and strings with
// only '"', '\\' and control characters escaped (RFC 8785). It is the form
// idempotency fingerprints, stored schemas and stored changes use.
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, decoded); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(f) // encoding/json formats float64 as ECMAScript does
		if err != nil {
			return err
		}
		buf.Write(encoded)
	case string:
		writeCanonicalString(buf, x)
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonicalString(buf, key)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical json: unexpected %T", v)
	}
	return nil
}

func utf16Less(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

func writeCanonicalString(buf *bytes.Buffer, s string) {
	const hexDigits = "0123456789abcdef"
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(`\u00`)
				buf.WriteByte(hexDigits[r>>4])
				buf.WriteByte(hexDigits[r&0xf])
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// Fingerprint is the lowercase hex SHA-256 of the transaction's canonical JSON.
// An idempotency key binds to it.
func Fingerprint(tx Transaction) (string, error) {
	canonical, err := Canonical(tx)
	if err != nil {
		return "", fmt.Errorf("%w: transaction is not JSON serializable", ErrInvalid)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
