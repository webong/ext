package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/webong/ext/res/int/inference"
)

// Stream runs a generation with streamGenerateContent's server-sent events. It emits
// started, text and reasoning deltas, tool.completed for each function call (Gemini sends
// a call whole), usage, and a completed event carrying the final response. A stream that
// ends without a finish reason fails.
func (p *provider) Stream(ctx context.Context, r inference.GenerationRequest, sink inference.EventSink) (inference.GenerationResponse, error) {
	body, err := p.payload(r)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	reply, err := p.send(ctx, http.MethodPost, "/models/"+r.Model+":streamGenerateContent?alt=sse", body)
	if err != nil {
		return inference.GenerationResponse{}, err
	}
	defer reply.Body.Close()
	stop := context.AfterFunc(ctx, func() { _ = reply.Body.Close() })
	defer stop()

	emit := func(ev inference.GenerationEvent) error {
		if err := sink.Emit(ctx, ev); err != nil {
			return inference.NewRemoteError(inference.ErrCanceled, "stream canceled by sink", "gemini")
		}
		return nil
	}
	fail := func() (inference.GenerationResponse, error) {
		if ctx.Err() != nil {
			return inference.GenerationResponse{}, inference.NewRemoteError(inference.ErrCanceled, "operation canceled", "gemini")
		}
		return inference.GenerationResponse{}, inference.NewRemoteError(inference.ErrInvalidResponse, "malformed stream", "gemini")
	}

	out := inference.GenerationResponse{Model: r.Model, Message: inference.Message{Role: inference.RoleAssistant}}
	started := false
	finish := ""
	reader := bufio.NewReaderSize(io.LimitReader(reply.Body, (32<<20)+1), 64<<10)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			if err == io.EOF {
				break
			}
			return fail()
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var c chunk
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &c); err != nil {
			return fail()
		}
		if !started {
			started = true
			out.ID = c.ResponseID
			if err := emit(inference.GenerationEvent{Kind: inference.EventKindStarted}); err != nil {
				return inference.GenerationResponse{}, err
			}
		}
		if len(c.Candidates) > 0 {
			candidate := c.Candidates[0]
			for _, part := range candidate.Content.Parts {
				ev, err := appendPart(&out.Message, part)
				if err != nil {
					return inference.GenerationResponse{}, err
				}
				if ev.Kind != "" {
					if err := emit(ev); err != nil {
						return inference.GenerationResponse{}, err
					}
				}
			}
			if candidate.FinishReason != "" {
				finish = candidate.FinishReason
			}
		}
		if c.Usage != nil {
			out.Usage = c.usage()
			u := out.Usage
			if err := emit(inference.GenerationEvent{Kind: inference.EventKindUsage, Usage: &u}); err != nil {
				return inference.GenerationResponse{}, err
			}
		}
	}
	if finish == "" {
		return fail()
	}
	if err := setFinish(&out, finish); err != nil {
		return inference.GenerationResponse{}, err
	}
	if err := out.Validate(); err != nil {
		return inference.GenerationResponse{}, err
	}
	done := out
	if err := emit(inference.GenerationEvent{Kind: inference.EventKindCompleted, Response: &done}); err != nil {
		return inference.GenerationResponse{}, err
	}
	return out, nil
}

// embedOptions are the request options batchEmbedContents takes for each input.
var embedOptions = map[string]bool{"taskType": true, "title": true, "outputDimensionality": true}

// Embed embeds the inputs with batchEmbedContents, one request per input, in input order.
func (p *provider) Embed(ctx context.Context, r inference.EmbeddingRequest) (inference.EmbeddingResponse, error) {
	if err := r.Validate(); err != nil {
		return inference.EmbeddingResponse{}, err
	}
	if !modelName.MatchString(r.Model) {
		return inference.EmbeddingResponse{}, inference.ErrInvalidRequest
	}
	var options map[string]json.RawMessage
	if len(r.Options) > 0 {
		if err := json.Unmarshal(r.Options, &options); err != nil {
			return inference.EmbeddingResponse{}, inference.ErrInvalidRequest
		}
		for key := range options {
			if !embedOptions[key] {
				return inference.EmbeddingResponse{}, fmt.Errorf("%w: option %q", inference.ErrUnsupported, key)
			}
		}
	}
	requests := make([]map[string]any, 0, len(r.Inputs))
	for _, input := range r.Inputs {
		req := map[string]any{"model": "models/" + r.Model, "content": map[string]any{"parts": []map[string]any{{"text": input}}}}
		for key, value := range options {
			req[key] = value
		}
		requests = append(requests, req)
	}
	body, err := json.Marshal(map[string]any{"requests": requests})
	if err != nil {
		return inference.EmbeddingResponse{}, err
	}
	reply, err := p.send(ctx, http.MethodPost, "/models/"+r.Model+":batchEmbedContents", body)
	if err != nil {
		return inference.EmbeddingResponse{}, err
	}
	defer reply.Body.Close()
	var result struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(io.LimitReader(reply.Body, 64<<20)).Decode(&result); err != nil || len(result.Embeddings) != len(r.Inputs) {
		return inference.EmbeddingResponse{}, inference.ErrInvalidResponse
	}
	out := inference.EmbeddingResponse{Model: r.Model}
	for i, e := range result.Embeddings {
		out.Embeddings = append(out.Embeddings, inference.Embedding{Index: i, Vector: e.Values})
	}
	return out, out.Validate()
}

// ListModels lists the models the API key can use.
func (p *provider) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	reply, err := p.send(ctx, http.MethodGet, "/models?pageSize=1000", nil)
	if err != nil {
		return nil, err
	}
	defer reply.Body.Close()
	var result struct {
		Models []struct {
			Name        string   `json:"name"`
			DisplayName string   `json:"displayName"`
			Methods     []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(reply.Body, 8<<20)).Decode(&result); err != nil {
		return nil, inference.ErrInvalidResponse
	}
	models := make([]inference.ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		info := inference.ModelInfo{ID: strings.TrimPrefix(m.Name, "models/"), Name: m.DisplayName}
		for _, method := range m.Methods {
			switch method {
			case "generateContent":
				info.Capabilities = append(info.Capabilities, inference.CapabilityGenerate)
			case "streamGenerateContent":
				info.Capabilities = append(info.Capabilities, inference.CapabilityStream)
			case "embedContent", "batchEmbedContents":
				if !containsCapability(info.Capabilities, inference.CapabilityEmbed) {
					info.Capabilities = append(info.Capabilities, inference.CapabilityEmbed)
				}
			}
		}
		models = append(models, info)
	}
	return models, nil
}

func containsCapability(list []inference.Capability, c inference.Capability) bool {
	for _, x := range list {
		if x == c {
			return true
		}
	}
	return false
}
