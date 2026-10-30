// Package inference defines capability interfaces. A provider implements only
// the interfaces for the operations it supports and advertises them in
// ProviderInfo.Capabilities. The full Provider bundles Info and Close; every
// operation is optional.
package inference

import "context"

// Provider is the base interface every inference provider implements. It
// reports identity and metadata and releases resources. Every operation is
// provided by a separate capability interface.
type Provider interface {
	// Info returns protocol metadata about the provider. It must not expose
	// credentials.
	Info(context.Context) (ProviderInfo, error)
	// Close releases the provider and any sessions it owns. It is safe to call
	// after an error and is idempotent.
	Close() error
}

// Generator performs non-streaming generation.
type Generator interface {
	// Generate runs one generation pass and returns the complete response.
	Generate(context.Context, GenerationRequest) (GenerationResponse, error)
}

// Streamer performs streaming generation through an EventSink. The sink's emit
// error provides backpressure and cancellation.
type Streamer interface {
	// Stream streams generation events to sink and returns the final response.
	Stream(context.Context, GenerationRequest, EventSink) (GenerationResponse, error)
}

// Embedder performs embeddings.
type Embedder interface {
	// Embed returns embeddings for the inputs.
	Embed(context.Context, EmbeddingRequest) (EmbeddingResponse, error)
}

// Transcriber performs transcription.
type Transcriber interface {
	// Transcribe transcribes audio.
	Transcribe(context.Context, TranscriptionRequest) (TranscriptionResponse, error)
}

// Synthesizer performs speech synthesis, returning a bounded byte stream.
type Synthesizer interface {
	// Synthesize synthesizes text to audio.
	Synthesize(context.Context, SpeechRequest) (SpeechStream, error)
}

// ModelLister lists available models, where supported.
type ModelLister interface {
	// ListModels returns the models the provider can serve.
	ListModels(context.Context) ([]ModelInfo, error)
}

// ModelInfo describes one model a provider can serve.
type ModelInfo struct {
	// ID is the model identity used in requests.
	ID string `json:"id"`
	// Name is a human-readable name.
	Name string `json:"name,omitempty"`
	// Provider is the provider that serves the model.
	Provider string `json:"provider,omitempty"`
	// Capabilities are the operations the model supports.
	Capabilities []Capability `json:"capabilities,omitempty"`
	// Modalities are the input/output kinds the model supports.
	Modalities []Modality `json:"modalities,omitempty"`
	// Devices are the compute devices the model can use.
	Devices []Device `json:"devices,omitempty"`
	// Stability is the maturity of the model.
	Stability Stability `json:"stability,omitempty"`
}

// HasCapability reports whether the model advertises the capability.
func (m ModelInfo) HasCapability(c Capability) bool {
	for _, cap := range m.Capabilities {
		if cap == c {
			return true
		}
	}
	return false
}

// Supports reports whether the provider can perform the operation for the
// request. Operations map to capabilities: Generate→CapabilityGenerate,
// Stream→CapabilityStream, Embed→CapabilityEmbed, Transcribe→CapabilityTranscribe,
// Synthesize→CapabilitySynthesize.
func (p ProviderInfo) Supports(cap Capability) bool {
	return p.HasCapability(cap)
}
