// Package ml inference defines bounded, transport-free interfaces for running
// on-device models through a caller-selected provider. The package owns the
// contracts only: it never loads a model, chooses a provider, allocates a
// tensor, or runs inference itself. Native providers ship beside these
// contracts as platform implementations (iOS CoreML in res/ml/ios, Android
// NNAPI/TensorFlow Lite in res/ml/android) or as product adapters.
package ml

import (
	"context"
	"errors"
	"fmt"
	"math"
)

var (
	// ErrUnavailable reports that a provider or session cannot perform the
	// requested operation on this platform.
	ErrUnavailable = errors.New("inference provider unavailable")
	// ErrNotLoaded reports that a session is not bound to a loaded model.
	ErrNotLoaded = errors.New("model is not loaded")
)

// Provider is a caller-selected native model inference backend. Name reports
// the provider identity (for example "coreml", "nnapi", "onnxruntime",
// "directml"); Capabilities lists the model kinds it can load; Load binds a
// verified installed model to a Session.
type Provider interface {
	// Name returns the provider identity.
	Name() string
	// Capabilities returns the model kinds this provider can load.
	Capabilities() []string
	// Load binds an installed model to a new session. The session is bound to
	// the exact manifest and directory returned by the store.
	Load(ctx context.Context, model *InstalledModel) (Session, error)
}

// Session runs bounded inference against one loaded model.
type Session interface {
	// Infer runs one inference pass over the input tensors and returns the
	// output tensors. Inputs and outputs are bounded by the provider.
	Infer(ctx context.Context, inputs []Tensor) ([]Tensor, error)
	// Close releases the model and any native resources. It is safe to call
	// after an error and idempotent.
	Close() error
}

// Tensor is one bounded multi-dimensional array. Shape is the dimension
// lengths; Data is the row-major little-endian byte buffer; Format names the
// element encoding (for example "float32").
type Tensor struct {
	Shape  []int  `json:"shape"`
	Data   []byte `json:"data"`
	Format string `json:"format"`
}

// Validate checks tensor shape, format, and data alignment.
func (t Tensor) Validate() error {
	if t.Format == "" {
		return fmt.Errorf("%w: tensor format is required", ErrInvalid)
	}
	if t.Format != "float32" {
		return fmt.Errorf("%w: tensor format %q is not supported", ErrInvalid, t.Format)
	}
	if len(t.Shape) < 1 || len(t.Shape) > 8 {
		return fmt.Errorf("%w: tensor must have between 1 and 8 dimensions", ErrInvalid)
	}
	elements := 1
	for _, dim := range t.Shape {
		if dim < 1 || dim > 1<<30 {
			return fmt.Errorf("%w: tensor dimension %d is out of range", ErrInvalid, dim)
		}
		if elements > (1<<30)/dim {
			return fmt.Errorf("%w: tensor element count overflows", ErrInvalid)
		}
		elements *= dim
	}
	expected := elements * 4
	if len(t.Data) != expected {
		return fmt.Errorf("%w: tensor data is %d bytes, expected %d bytes for shape %v", ErrInvalid, len(t.Data), expected, t.Shape)
	}
	return nil
}

// Elements returns the total element count of the tensor shape.
func (t Tensor) Elements() int {
	count := 1
	for _, dim := range t.Shape {
		count *= dim
	}
	return count
}

// Float32 returns the tensor data as float32 values. The caller must not
// mutate the returned slice if the tensor is shared.
func (t Tensor) Float32() []float32 {
	if len(t.Data)%4 != 0 {
		return nil
	}
	values := make([]float32, len(t.Data)/4)
	for i := range values {
		values[i] = float32FromBytes(t.Data[i*4 : i*4+4])
	}
	return values
}

// FromFloat32 builds a float32 tensor from a shape and values. The values are
// copied into little-endian bytes.
func FromFloat32(shape []int, values []float32) (Tensor, error) {
	data := make([]byte, len(values)*4)
	for i, value := range values {
		float32ToBytes(value, data[i*4:])
	}
	tensor := Tensor{Shape: shape, Data: data, Format: "float32"}
	if err := tensor.Validate(); err != nil {
		return Tensor{}, err
	}
	return tensor, nil
}

func float32ToBytes(value float32, out []byte) {
	bits := math.Float32bits(value)
	out[0] = byte(bits)
	out[1] = byte(bits >> 8)
	out[2] = byte(bits >> 16)
	out[3] = byte(bits >> 24)
}

func float32FromBytes(b []byte) float32 {
	bits := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return math.Float32frombits(bits)
}

var _ = errors.Unwrap
