// Package plugin defines the portable contract for independently provided
// capabilities. Consumers own domain semantics, authorization, installation
// locations, and native behavior. A descriptor is a declaration, not a grant.
package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
	"unicode/utf8"
)

const APIVersion = "ctx.plugin/v1"
const MaxFrameBytes = 24 << 20
const DefaultTimeout = 30 * time.Second

var (
	ErrInvalid     = errors.New("invalid plugin contract")
	ErrUnsupported = errors.New("unsupported plugin operation")
	ErrNotFound    = errors.New("no matching plugin")
	ErrAmbiguous   = errors.New("multiple matching plugins")
	ErrDenied      = errors.New("plugin admission denied")
	ErrMismatch    = errors.New("plugin identity or contract mismatch")
	ErrClosed      = errors.New("plugin session closed")
	ErrDraining    = errors.New("plugin session draining")
)

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,255}$`)
var revision = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+/-]{0,255}$`)

// Identity pins one immutable package selection. Revision changes whenever
// selected content or configuration changes, even at the same release Version.
// Version is optional release metadata; contract versions are independent.
type Identity struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Version  string `json:"version,omitempty"`
}

type ContractRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Operation names and surfaces belong to the consumer. Surface is an optional
// admission classification; it has no built-in vocabulary or authority.
type Operation struct {
	Name    string `json:"name"`
	Surface string `json:"surface,omitempty"`
}

type Contract struct {
	ContractRef
	Operations []Operation `json:"operations"`
}

type Descriptor struct {
	APIVersion string     `json:"apiVersion"`
	Identity   Identity   `json:"identity"`
	Contracts  []Contract `json:"contracts"`
}

func (id Identity) Validate() error {
	if !identifier.MatchString(id.ID) || !revision.MatchString(id.Revision) || (id.Version != "" && !revision.MatchString(id.Version)) {
		return fmt.Errorf("%w: identity requires a bounded ID and immutable revision", ErrInvalid)
	}
	return nil
}

func (ref ContractRef) Validate() error {
	if !identifier.MatchString(ref.Name) || !revision.MatchString(ref.Version) {
		return fmt.Errorf("%w: contract requires a name and exact version", ErrInvalid)
	}
	return nil
}

func (d Descriptor) Validate() error {
	if d.APIVersion != APIVersion {
		return fmt.Errorf("%w: unsupported apiVersion", ErrInvalid)
	}
	if err := d.Identity.Validate(); err != nil {
		return err
	}
	if len(d.Contracts) == 0 || len(d.Contracts) > 64 {
		return fmt.Errorf("%w: declare 1..64 contracts", ErrInvalid)
	}
	seen := map[ContractRef]bool{}
	for _, c := range d.Contracts {
		if err := c.ContractRef.Validate(); err != nil {
			return err
		}
		if seen[c.ContractRef] || len(c.Operations) == 0 || len(c.Operations) > 256 {
			return fmt.Errorf("%w: duplicate contract or invalid operation count", ErrInvalid)
		}
		seen[c.ContractRef] = true
		ops := map[string]bool{}
		for _, op := range c.Operations {
			if !identifier.MatchString(op.Name) || op.Name == "plugin.hello" || ops[op.Name] || (op.Surface != "" && !identifier.MatchString(op.Surface)) {
				return fmt.Errorf("%w: invalid, reserved, or duplicate operation", ErrInvalid)
			}
			ops[op.Name] = true
		}
	}
	return nil
}

// Lookup uses exact contract versions. Consumers resolve release ranges and
// preference policy before handing an immutable selection to the host.
func (d Descriptor) Lookup(ref ContractRef, name string) (Operation, error) {
	for _, c := range d.Contracts {
		if c.ContractRef == ref {
			for _, op := range c.Operations {
				if op.Name == name {
					return op, nil
				}
			}
		}
	}
	return Operation{}, ErrUnsupported
}

func (d Descriptor) Clone() Descriptor {
	d.Contracts = append([]Contract(nil), d.Contracts...)
	for i := range d.Contracts {
		d.Contracts[i].Operations = append([]Operation(nil), d.Contracts[i].Operations...)
	}
	return d
}

// MatchHandshake rejects both expansion and reduction of the selected contract.
// Declaration ordering is insignificant.
func MatchHandshake(selected, actual Descriptor) error {
	if err := selected.Validate(); err != nil {
		return err
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if selected.Identity != actual.Identity || len(selected.Contracts) != len(actual.Contracts) {
		return ErrMismatch
	}
	for _, c := range selected.Contracts {
		found := false
		for _, candidate := range actual.Contracts {
			if c.ContractRef == candidate.ContractRef && len(c.Operations) == len(candidate.Operations) {
				found = true
				break
			}
		}
		if !found {
			return ErrMismatch
		}
		for _, op := range c.Operations {
			other, err := actual.Lookup(c.ContractRef, op.Name)
			if err != nil || other != op {
				return ErrMismatch
			}
		}
	}
	return nil
}

type Requirement struct {
	// Identity optionally pins the complete identity, never a partial match.
	Identity  *Identity
	Contract  ContractRef
	Operation string
}

// Select rejects ambiguity rather than choosing a provider by name or order.
// All candidates are validated; invalid discovery data is never silently used.
func Select(candidates []Descriptor, want Requirement) (Descriptor, error) {
	if err := want.Contract.Validate(); err != nil {
		return Descriptor{}, err
	}
	if !identifier.MatchString(want.Operation) {
		return Descriptor{}, ErrInvalid
	}
	if want.Identity != nil {
		if err := want.Identity.Validate(); err != nil {
			return Descriptor{}, err
		}
	}
	var selected Descriptor
	count := 0
	for _, d := range candidates {
		if err := d.Validate(); err != nil {
			return Descriptor{}, err
		}
		if want.Identity != nil && d.Identity != *want.Identity {
			continue
		}
		if _, err := d.Lookup(want.Contract, want.Operation); err != nil {
			continue
		}
		selected = d
		count++
	}
	if count == 0 {
		return Descriptor{}, ErrNotFound
	}
	if count != 1 {
		return Descriptor{}, ErrAmbiguous
	}
	return selected.Clone(), nil
}

type Request struct {
	APIVersion string          `json:"apiVersion"`
	ID         string          `json:"id"`
	Plugin     Identity        `json:"plugin"`
	Contract   ContractRef     `json:"contract"`
	Operation  string          `json:"operation"`
	Surface    string          `json:"surface,omitempty"`
	Deadline   time.Time       `json:"deadline"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

func (r Request) Clone() Request { r.Payload = append(json.RawMessage(nil), r.Payload...); return r }

// ValidateRequest enforces the selected descriptor, before domain admission.
// Payload semantics and any authorization references are consumer-owned.
func ValidateRequest(d Descriptor, r Request) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if r.APIVersion != APIVersion || !revision.MatchString(r.ID) || r.Deadline.IsZero() {
		return ErrInvalid
	}
	if r.Plugin != d.Identity {
		return ErrMismatch
	}
	op, err := d.Lookup(r.Contract, r.Operation)
	if err != nil {
		return err
	}
	if op.Surface != r.Surface {
		return ErrMismatch
	}
	if len(r.Payload) != 0 {
		var raw json.RawMessage
		if err := Decode(r.Payload, &raw); err != nil {
			return err
		}
	}
	return nil
}

// RemoteError carries a stable machine-readable category. Its text is
// untrusted diagnostic data and must not be projected into public state.
type RemoteError struct {
	Code                   string `json:"code"`
	Message                string `json:"message"`
	RetryAfterMilliseconds int64  `json:"retryAfterMilliseconds,omitempty"`
}

func (e *RemoteError) Error() string { return "plugin: " + e.Code + ": " + e.Message }

type Response struct {
	APIVersion string          `json:"apiVersion"`
	ID         string          `json:"id"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      *RemoteError    `json:"error,omitempty"`
}

func (r Response) Validate(requestID string) error {
	if r.APIVersion != APIVersion || r.ID != requestID {
		return ErrMismatch
	}
	if (r.Error == nil) == (len(r.Payload) == 0) {
		return ErrInvalid
	}
	if r.Error != nil {
		if !identifier.MatchString(r.Error.Code) || len(r.Error.Message) > 4096 || r.Error.RetryAfterMilliseconds < 0 {
			return ErrInvalid
		}
	} else {
		var raw json.RawMessage
		if err := Decode(r.Payload, &raw); err != nil {
			return err
		}
	}
	return nil
}

// Decode reads one bounded JSON value and rejects unknown fields and duplicate
// object keys (including in payloads), keeping admission and dispatch unambiguous.
func Decode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("%w: JSON must be UTF-8", ErrInvalid)
	}
	if len(data) > MaxFrameBytes {
		return fmt.Errorf("%w: frame too large", ErrInvalid)
	}
	if err := rejectUnpairedSurrogates(data); err != nil {
		return err
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := uniqueValue(check, 0); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := check.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON", ErrInvalid)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

// rejectUnpairedSurrogates refuses a \uD800-\uDFFF escape that is not part of a
// high/low pair. encoding/json would silently replace it with U+FFFD, changing
// the text a peer sent; the shared C engine rejects it, so both engines must.
func rejectUnpairedSurrogates(data []byte) error {
	hex := func(i int) (rune, bool) {
		if i+6 > len(data) || data[i] != '\\' || data[i+1] != 'u' {
			return 0, false
		}
		var r rune
		for _, c := range data[i+2 : i+6] {
			switch {
			case c >= '0' && c <= '9':
				r = r<<4 | rune(c-'0')
			case c >= 'a' && c <= 'f':
				r = r<<4 | rune(c-'a'+10)
			case c >= 'A' && c <= 'F':
				r = r<<4 | rune(c-'A'+10)
			default:
				return 0, false
			}
		}
		return r, true
	}
	inString := false
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case !inString:
			inString = c == '"'
		case c == '"':
			inString = false
		case c == '\\':
			if r, ok := hex(i); ok {
				switch {
				case r >= 0xd800 && r <= 0xdbff:
					if low, ok := hex(i + 6); !ok || low < 0xdc00 || low > 0xdfff {
						return fmt.Errorf("%w: unpaired surrogate escape", ErrInvalid)
					}
					i += 11
					continue
				case r >= 0xdc00 && r <= 0xdfff:
					return fmt.Errorf("%w: unpaired surrogate escape", ErrInvalid)
				}
				i += 5
				continue
			}
			i++ // skip the escaped character, including \\ and \"
		}
	}
	return nil
}

func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds 64 levels")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return errors.New("duplicate or invalid JSON key")
			}
			keys[name] = true
		}
		if err := uniqueValue(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
