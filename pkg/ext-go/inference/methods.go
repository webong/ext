// Package inference guest-side operation declarations for inference.provider/v1.
package inference

import (
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/guest/stream"
	"github.com/webong/ext/res/inference"
)

func capabilitiesMethod() author.Method[struct{}, inference.ProviderInfo] {
	return author.Method[struct{}, inference.ProviderInfo]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpCapabilities, Surface: "observation"},
	}
}

func healthMethod() author.Method[struct{}, health] {
	return author.Method[struct{}, health]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpHealth, Surface: "observation"},
	}
}

func generateMethod() author.Method[inference.GenerationRequest, inference.GenerationResponse] {
	return author.Method[inference.GenerationRequest, inference.GenerationResponse]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpGenerate, Surface: "action"},
	}
}

func streamMethod() author.Method[inference.GenerationRequest, stream.OpenResponse] {
	return author.Method[inference.GenerationRequest, stream.OpenResponse]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpStream, Surface: "action", Progress: true},
	}
}

func embedMethod() author.Method[inference.EmbeddingRequest, inference.EmbeddingResponse] {
	return author.Method[inference.EmbeddingRequest, inference.EmbeddingResponse]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpEmbed, Surface: "action"},
	}
}

func transcribeMethod() author.Method[inference.TranscriptionRequest, inference.TranscriptionResponse] {
	return author.Method[inference.TranscriptionRequest, inference.TranscriptionResponse]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpTranscribe, Surface: "action"},
	}
}

func synthesizeMethod() author.Method[inference.SpeechRequest, stream.OpenResponse] {
	return author.Method[inference.SpeechRequest, stream.OpenResponse]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpSynthesize, Surface: "action", Progress: true},
	}
}

func modelsMethod() author.Method[struct{}, []inference.ModelInfo] {
	return author.Method[struct{}, []inference.ModelInfo]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpModels, Surface: "observation"},
	}
}

func closeMethod() author.Method[struct{}, struct{}] {
	return author.Method[struct{}, struct{}]{
		Contract: ContractRef,
		Operation: plugin.Operation{Name: OpClose, Surface: "action"},
	}
}
