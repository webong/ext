// Package clipboard defines bounded, transport-free access to a native system
// clipboard. Hosts choose a backend and policy; this package performs no remote
// routing, approval UI, permission prompting, or implicit restoration.
package clipboard

import (
	"context"
	"errors"
	"fmt"
)

const (
	MaxPolicyBytes = 16 << 20
	MaxItems       = 64
	MaxFormats     = 32
)

var (
	ErrChanged = errors.New("clipboard generation changed")
	ErrDenied  = errors.New("clipboard operation denied")
	ErrInvalid = errors.New("invalid clipboard value")
	ErrLimit   = errors.New("clipboard limit exceeded")
)

// Policy is fixed by the embedding application before native access begins.
type Policy struct {
	MaxBytes       int64    `json:"maxBytes"`
	AllowedFormats []string `json:"allowedFormats"`
	AllowRead      bool     `json:"allowRead"`
	AllowWrite     bool     `json:"allowWrite"`
}

type Representation struct {
	Format string `json:"format"`
	Data   []byte `json:"data"`
}

type Item struct {
	Representations []Representation `json:"representations"`
}

// Snapshot is a finite copy. Generation is opaque and is only meaningful to
// the backend instance that produced it.
type Snapshot struct {
	Generation string `json:"generation"`
	Items      []Item `json:"items"`
}

type WriteRequest struct {
	ExpectedGeneration string `json:"expectedGeneration"`
	Items              []Item `json:"items"`
}

// Receipt distinguishes a dispatched native mutation from later verification.
type Receipt struct {
	Generation string `json:"generation"`
	Dispatch   string `json:"dispatch"`
}

// Backend serializes operations and compares ExpectedGeneration immediately
// before mutation. Native clipboard APIs may still lack cross-process atomicity.
type Backend interface {
	Generation(context.Context) (string, error)
	Read(context.Context, int64, []string) (Snapshot, error)
	Write(context.Context, WriteRequest, int64, []string) (Receipt, error)
}

type Clipboard struct {
	backend Backend
	policy  Policy
}

func New(backend Backend, policy Policy) (*Clipboard, error) {
	if backend == nil {
		return nil, fmt.Errorf("%w: backend is required", ErrInvalid)
	}
	if err := ValidatePolicy(policy); err != nil {
		return nil, err
	}
	policy.AllowedFormats = append([]string(nil), policy.AllowedFormats...)
	return &Clipboard{backend: backend, policy: policy}, nil
}

func ValidatePolicy(policy Policy) error {
	if policy.MaxBytes <= 0 || policy.MaxBytes > MaxPolicyBytes || len(policy.AllowedFormats) == 0 || len(policy.AllowedFormats) > MaxFormats {
		return fmt.Errorf("%w: clipboard needs a byte ceiling and 1-%d formats", ErrInvalid, MaxFormats)
	}
	seen := map[string]bool{}
	for _, format := range policy.AllowedFormats {
		if format == "" || len(format) > 256 || seen[format] {
			return fmt.Errorf("%w: invalid or repeated clipboard format", ErrInvalid)
		}
		seen[format] = true
	}
	return nil
}

func ValidateSnapshot(snapshot Snapshot, maxBytes int64, formats []string) error {
	if snapshot.Generation == "" {
		return fmt.Errorf("%w: generation is required", ErrInvalid)
	}
	return ValidateItems(snapshot.Items, maxBytes, formats)
}

func ValidateItems(items []Item, maxBytes int64, formats []string) error {
	if maxBytes <= 0 || maxBytes > MaxPolicyBytes || len(items) > MaxItems {
		return ErrLimit
	}
	allowed := make(map[string]bool, len(formats))
	for _, format := range formats {
		allowed[format] = true
	}
	var size int64
	for _, item := range items {
		if len(item.Representations) == 0 || len(item.Representations) > MaxFormats {
			return fmt.Errorf("%w: item representations", ErrInvalid)
		}
		seen := map[string]bool{}
		for _, representation := range item.Representations {
			if !allowed[representation.Format] || seen[representation.Format] {
				return fmt.Errorf("%w: format is not allowed or is repeated", ErrDenied)
			}
			seen[representation.Format] = true
			size += int64(len(representation.Data))
			if size > maxBytes {
				return ErrLimit
			}
		}
	}
	return nil
}

// Generation returns mutation metadata without reading clipboard contents. It
// lets a write-only client perform a conditional write without gaining read
// access.
func (c *Clipboard) Generation(ctx context.Context) (string, error) {
	generation, err := c.backend.Generation(ctx)
	if err != nil {
		return "", err
	}
	if generation == "" {
		return "", fmt.Errorf("%w: generation is required", ErrInvalid)
	}
	return generation, nil
}

func (c *Clipboard) Read(ctx context.Context) (Snapshot, error) {
	if !c.policy.AllowRead {
		return Snapshot{}, ErrDenied
	}
	snapshot, err := c.backend.Read(ctx, c.policy.MaxBytes, c.policy.AllowedFormats)
	if err != nil {
		return Snapshot{}, err
	}
	if err := ValidateSnapshot(snapshot, c.policy.MaxBytes, c.policy.AllowedFormats); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (c *Clipboard) Write(ctx context.Context, request WriteRequest) (Receipt, error) {
	if !c.policy.AllowWrite {
		return Receipt{}, ErrDenied
	}
	if request.ExpectedGeneration == "" {
		return Receipt{}, fmt.Errorf("%w: expected generation is required", ErrInvalid)
	}
	if err := ValidateItems(request.Items, c.policy.MaxBytes, c.policy.AllowedFormats); err != nil {
		return Receipt{}, err
	}
	receipt, err := c.backend.Write(ctx, request, c.policy.MaxBytes, c.policy.AllowedFormats)
	if err != nil {
		return Receipt{}, err
	}
	if receipt.Generation == "" || receipt.Dispatch != "dispatched" {
		return Receipt{}, fmt.Errorf("%w: native mutation returned an unknown dispatch", ErrInvalid)
	}
	return receipt, nil
}

func (c *Clipboard) Clear(ctx context.Context, expectedGeneration string) (Receipt, error) {
	return c.Write(ctx, WriteRequest{ExpectedGeneration: expectedGeneration, Items: []Item{}})
}
