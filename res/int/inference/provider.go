// Package inference defines portable, transport-free contracts for inference
// through a caller-selected provider: generation, streaming generation,
// embeddings, transcription, and speech synthesis.
//
// The package owns the contracts only: it never chooses a provider, performs
// network I/O, loads a model, runs inference, authorizes access, or exposes a
// remote protocol. Provider adapters (local res/int/model and remote HTTP services)
// satisfy these interfaces. Concrete adapters live in adapters/<name> or in
// platform implementations of res/int/model.
//
// Zero values are safe where practical. Accept interfaces and return concrete
// values. All operations accept context.Context.
//
// Contract: inference.provider/v1 (recorded in docs/contract-names.md).
package inference

import (
	"errors"
	"fmt"
)

// Stability reports the maturity of a provider or a declared capability.
type Stability string

const (
	// StabilityExperimental is a new capability whose contract may change.
	StabilityExperimental Stability = "experimental"
	// StabilityStable is a supported capability with a frozen contract.
	StabilityStable Stability = "stable"
	// StabilityLegacy is still supported but superseded.
	StabilityLegacy Stability = "legacy"
	// StabilityDeprecated is scheduled for removal.
	StabilityDeprecated Stability = "deprecated"
)

// IsZero reports whether the stability value is unset.
func (s Stability) IsZero() bool { return s == "" }

// String renders the stability for logging and errors.
func (s Stability) String() string {
	if s == "" {
		return "unset"
	}
	return string(s)
}

// Valid reports whether s is a known stability value.
func (s Stability) Valid() bool {
	switch s {
	case StabilityExperimental, StabilityStable, StabilityLegacy, StabilityDeprecated:
		return true
	default:
		return false
	}
}

// Modality is one kind of input or output content.
type Modality string

const (
	// ModalityText is plain text.
	ModalityText Modality = "text"
	// ModalityImage is an image.
	ModalityImage Modality = "image"
	// ModalityAudio is audio.
	ModalityAudio Modality = "audio"
	// ModalityVideo is video.
	ModalityVideo Modality = "video"
	// ModalityFile is a file or explicitly approved reference.
	ModalityFile Modality = "file"
)

// Valid reports whether m is a known modality.
func (m Modality) Valid() bool {
	switch m {
	case ModalityText, ModalityImage, ModalityAudio, ModalityVideo, ModalityFile:
		return true
	default:
		return false
	}
}

// Capability is one operation a provider may advertise.
type Capability string

const (
	// CapabilityGenerate performs non-streaming generation.
	CapabilityGenerate Capability = "generate"
	// CapabilityStream performs streaming generation.
	CapabilityStream Capability = "stream"
	// CapabilityEmbed performs embeddings.
	CapabilityEmbed Capability = "embed"
	// CapabilityTranscribe performs transcription.
	CapabilityTranscribe Capability = "transcribe"
	// CapabilitySynthesize performs speech synthesis.
	CapabilitySynthesize Capability = "synthesize"
	// CapabilityStructuredOutput supports JSON Schema structured output.
	CapabilityStructuredOutput Capability = "structuredOutput"
	// CapabilityToolProposal supports tool-call proposals.
	CapabilityToolProposal Capability = "toolProposal"
	// CapabilityModelListing supports listing available models.
	CapabilityModelListing Capability = "modelListing"
)

// Valid reports whether c is a known capability.
func (c Capability) Valid() bool {
	switch c {
	case CapabilityGenerate, CapabilityStream, CapabilityEmbed, CapabilityTranscribe,
		CapabilitySynthesize, CapabilityStructuredOutput, CapabilityToolProposal,
		CapabilityModelListing:
		return true
	default:
		return false
	}
}

// Device is a compute device a provider may report.
type Device string

const (
	// DeviceCPU is a CPU.
	DeviceCPU Device = "cpu"
	// DeviceGPU is a GPU.
	DeviceGPU Device = "gpu"
	// DeviceNPU is an NPU.
	DeviceNPU Device = "npu"
	// DeviceNeuralEngine is an Apple Neural Engine.
	DeviceNeuralEngine Device = "neuralEngine"
)

// Valid reports whether d is a known device.
func (d Device) Valid() bool {
	switch d {
	case DeviceCPU, DeviceGPU, DeviceNPU, DeviceNeuralEngine:
		return true
	default:
		return false
	}
}

// Limits are explicit output and input bounds for a request.
type Limits struct {
	// MaxOutputTokens bounds generated output tokens. Zero means unset.
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	// MaxInputTokens bounds input tokens. Zero means unset.
	MaxInputTokens int `json:"maxInputTokens,omitempty"`
	// MaxBytes bounds the encoded request or response body. Zero means unset.
	MaxBytes int64 `json:"maxBytes,omitempty"`
}

// Validate checks that set limits are non-negative.
func (l Limits) Validate() error {
	if l.MaxOutputTokens < 0 {
		return fmt.Errorf("%w: maxOutputTokens must not be negative", ErrInvalidRequest)
	}
	if l.MaxInputTokens < 0 {
		return fmt.Errorf("%w: maxInputTokens must not be negative", ErrInvalidRequest)
	}
	if l.MaxBytes < 0 {
		return fmt.Errorf("%w: maxBytes must not be negative", ErrInvalidRequest)
	}
	return nil
}

// ProviderInfo is protocol metadata about a provider. It must not expose
// credentials or secrets.
type ProviderInfo struct {
	// ID is the stable provider identity (for example "openai", "coreml").
	ID string `json:"id"`
	// Name is a human-readable display name.
	Name string `json:"name,omitempty"`
	// Version is the provider implementation version.
	Version string `json:"version,omitempty"`
	// Stability is the maturity of the provider.
	Stability Stability `json:"stability,omitempty"`
	// Modalities are the input/output kinds the provider supports.
	Modalities []Modality `json:"modalities,omitempty"`
	// Capabilities are the operations the provider supports.
	Capabilities []Capability `json:"capabilities,omitempty"`
	// Devices are the compute devices the provider reports, where relevant.
	Devices []Device `json:"devices,omitempty"`
	// Limits are the provider's default bounds. A request may tighten them.
	Limits Limits `json:"limits,omitempty"`
	// Protocol is protocol metadata that does not expose credentials.
	Protocol map[string]string `json:"protocol,omitempty"`
}

// HasCapability reports whether the provider advertises the capability.
func (p ProviderInfo) HasCapability(c Capability) bool {
	for _, cap := range p.Capabilities {
		if cap == c {
			return true
		}
	}
	return false
}

// HasModality reports whether the provider supports the modality.
func (p ProviderInfo) HasModality(m Modality) bool {
	for _, mod := range p.Modalities {
		if mod == m {
			return true
		}
	}
	return false
}

// HasDevice reports whether the provider reports the device.
func (p ProviderInfo) HasDevice(d Device) bool {
	for _, dev := range p.Devices {
		if dev == d {
			return true
		}
	}
	return false
}

// Validate checks provider identity and declared values.
func (p ProviderInfo) Validate() error {
	if p.ID == "" {
		return fmt.Errorf("%w: provider id is required", ErrInvalidRequest)
	}
	if err := validateName(p.ID, "provider id"); err != nil {
		return err
	}
	for _, m := range p.Modalities {
		if !m.Valid() {
			return fmt.Errorf("%w: unknown modality %q", ErrInvalidRequest, m)
		}
	}
	for _, c := range p.Capabilities {
		if !c.Valid() {
			return fmt.Errorf("%w: unknown capability %q", ErrInvalidRequest, c)
		}
	}
	for _, d := range p.Devices {
		if !d.Valid() {
			return fmt.Errorf("%w: unknown device %q", ErrInvalidRequest, d)
		}
	}
	return p.Limits.Validate()
}

func validateName(value, what string) error {
	if value == "" || len(value) > 128 {
		return fmt.Errorf("%w: %s is required and must be at most 128 bytes", ErrInvalidRequest, what)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%w: %s must not contain control characters", ErrInvalidRequest, what)
		}
	}
	return nil
}

var _ = errors.Unwrap
